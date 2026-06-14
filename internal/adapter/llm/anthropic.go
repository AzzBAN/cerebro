package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/azhar/cerebro/internal/agent"
	"github.com/azhar/cerebro/internal/port"
)

const defaultAnthropicBase = "https://api.anthropic.com/v1/messages"
const anthropicVersion = "2023-06-01"

// AnthropicAdapter implements port.LLM using the Anthropic Claude API.
type AnthropicAdapter struct {
	apiKey  string
	modelID string
	temp    float64
	maxToks int
	baseURL string
	client  *http.Client
}

// NewAnthropic creates a Claude LLM adapter.
// Set baseURL="" to use the standard Anthropic API endpoint.
func NewAnthropic(apiKey, baseURL, modelID string, temperature float64, maxOutputTokens int) *AnthropicAdapter {
	if baseURL == "" {
		baseURL = defaultAnthropicBase
	}
	return &AnthropicAdapter{
		apiKey:  apiKey,
		modelID: modelID,
		temp:    temperature,
		maxToks: maxOutputTokens,
		baseURL: baseURL,
		client:  &http.Client{Timeout: defaultHTTPTimeout},
	}
}

func (a *AnthropicAdapter) Provider() string { return "anthropic" }
func (a *AnthropicAdapter) ModelID() string  { return a.modelID }

// anthropicMessage represents a single turn in an Anthropic conversation.
type anthropicMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"` // string or []contentBlock
}

type contentBlock struct {
	Type         string          `json:"type"`
	Text         string          `json:"text,omitempty"`
	ID           string          `json:"id,omitempty"`
	Name         string          `json:"name,omitempty"`
	Input        json.RawMessage `json:"input,omitempty"`
	CacheControl *cacheControl   `json:"cache_control,omitempty"`
}

// cacheControl marks a content/tool block as eligible for Anthropic's
// prompt caching (5-minute ephemeral cache by default). Hitting the cache
// drops the input-token price of that block to 10% of list.
type cacheControl struct {
	Type string `json:"type"` // always "ephemeral" today
}

type toolResultBlock struct {
	Type      string `json:"type"`
	ToolUseID string `json:"tool_use_id"`
	Content   string `json:"content"`
}

// systemBlock represents one segment of the system prompt. Breaking the
// system prompt into blocks lets us attach cache_control to the large,
// static portion while keeping small volatile strings uncached.
type systemBlock struct {
	Type         string        `json:"type"` // "text"
	Text         string        `json:"text"`
	CacheControl *cacheControl `json:"cache_control,omitempty"`
}

type anthropicRequest struct {
	Model     string             `json:"model"`
	MaxTokens int                `json:"max_tokens"`
	System    []systemBlock      `json:"system,omitempty"`
	Messages  []anthropicMessage `json:"messages"`
	Tools     []anthropicTool    `json:"tools,omitempty"`
}

type anthropicTool struct {
	Name         string         `json:"name"`
	Description  string         `json:"description"`
	InputSchema  map[string]any `json:"input_schema"`
	CacheControl *cacheControl  `json:"cache_control,omitempty"`
}

type anthropicResponse struct {
	Content    []contentBlock `json:"content"`
	StopReason string         `json:"stop_reason"`

	// Usage is the per-turn token accounting. Anthropic returns input +
	// output token counts on every response; cached-read tokens are
	// surfaced separately so the caller can estimate cost accurately.
	Usage anthropicUsage `json:"usage"`

	// Proxies sometimes return content as a plain string instead of blocks.
	ContentStr string `json:"text"`

	// OpenAI-compatible proxies return choices instead of content blocks.
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices,omitempty"`

	// Some proxies return output[] instead of content[].
	Output []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"output,omitempty"`
}

// anthropicUsage matches the fields we care about from the `usage` block.
// CacheReadInputTokens is the portion of the input that hit the prompt
// cache (priced at ~10% of uncached input). CacheCreationInputTokens is
// the one-time write cost to seed the cache (priced at ~1.25× list); it
// is rolled into the uncached total for pricing purposes.
type anthropicUsage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
}

// proxyErrorResponse detects non-standard proxy error payloads wrapped in HTTP 200.
type proxyErrorResponse struct {
	Code    int    `json:"code"`
	Msg     string `json:"msg"`
	Success *bool  `json:"success"`
}

// Complete runs the Anthropic tool-calling loop.
func (a *AnthropicAdapter) Complete(
	ctx context.Context,
	systemPrompt string,
	userMessage string,
	tools map[string]port.Tool,
) (string, error) {
	const defaultMaxTurns = 12
	maxTurns := agent.MaxTurnsFromCtx(ctx, defaultMaxTurns)

	msgs := []anthropicMessage{
		{Role: "user", Content: userMessage},
	}

	anthropicTools := buildAnthropicTools(tools)
	systemBlocks := buildSystemBlocks(systemPrompt)
	turnTimeout := agent.TurnTimeoutFromCtx(ctx)
	maxToks := agent.MaxTokensFromCtx(ctx, a.maxToks)

	for turn := 0; turn < maxTurns; turn++ {
		reqBody := anthropicRequest{
			Model:     a.modelID,
			MaxTokens: maxToks,
			System:    systemBlocks,
			Messages:  msgs,
		}
		if len(anthropicTools) > 0 {
			reqBody.Tools = anthropicTools
		}
		bodyBytes, _ := json.Marshal(reqBody)

		slog.Debug("anthropic request", "url", a.baseURL, "model", a.modelID, "body", truncateForLog(string(bodyBytes)))

		respBody, status, err := a.doTurn(ctx, turnTimeout, bodyBytes)
		if err != nil {
			return "", fmt.Errorf("%w: anthropic: %w", ErrLLMCall, err)
		}
		if status != http.StatusOK {
			return "", fmt.Errorf("%w: anthropic: status %d: %s", ErrLLMCall, status, truncateForLog(string(respBody)))
		}

		rawBody := respBody
		slog.Debug("anthropic raw response", "url", a.baseURL, "status", status, "body", truncateForLog(string(rawBody)))

		// Some Anthropic-compatible proxies stream by default and return a
		// Server-Sent Events body (event:/data: lines) even when stream was
		// not requested. Detect that shape and reassemble it; otherwise fall
		// back to the standard single-object JSON decode path.
		var apiResp anthropicResponse
		if looksLikeSSE(rawBody) {
			parsed, perr := parseAnthropicSSE(rawBody)
			if perr != nil {
				return "", fmt.Errorf("%w: anthropic: sse decode: %v (body=%s)", ErrLLMCall, perr, truncateForLog(string(rawBody)))
			}
			apiResp = parsed
		} else {
			// Detect proxy error responses wrapped in HTTP 200.
			var proxyErr proxyErrorResponse
			if json.Unmarshal(rawBody, &proxyErr) == nil && proxyErr.Msg != "" {
				return "", fmt.Errorf("%w: anthropic: proxy error (url=%s model=%s): %s",
					ErrLLMCall, a.baseURL, a.modelID, proxyErr.Msg)
			}
			if err := json.Unmarshal(rawBody, &apiResp); err != nil {
				return "", fmt.Errorf("%w: anthropic: decode: %v (body=%s)", ErrLLMCall, err, truncateForLog(string(rawBody)))
			}
		}

		// Record per-turn usage so the CostTracker sees the full loop,
		// not just the last turn. Cache-creation is billed as input;
		// cache-read is the cheaper cached portion.
		if u := agent.UsageFromCtx(ctx); u != nil {
			totalIn := apiResp.Usage.InputTokens +
				apiResp.Usage.CacheReadInputTokens +
				apiResp.Usage.CacheCreationInputTokens
			u.Add(totalIn, apiResp.Usage.OutputTokens, apiResp.Usage.CacheReadInputTokens)
		}

		// Add assistant turn.
		msgs = append(msgs, anthropicMessage{Role: "assistant", Content: apiResp.Content})

		if apiResp.StopReason != "tool_use" {
			// 1. Standard Anthropic: content[].text blocks.
			var texts []string
			for _, block := range apiResp.Content {
				if block.Type == "text" && block.Text != "" {
					texts = append(texts, block.Text)
				}
			}
			if len(texts) > 0 {
				return stripReasoning(strings.Join(texts, "\n")), nil
			}
			// 2. Some proxies return content as a plain string.
			if apiResp.ContentStr != "" {
				return stripReasoning(apiResp.ContentStr), nil
			}
			// 3. OpenAI-compatible proxies: choices[].message.content.
			if len(apiResp.Choices) > 0 && apiResp.Choices[0].Message.Content != "" {
				return stripReasoning(apiResp.Choices[0].Message.Content), nil
			}
			// 4. Some proxies use output[].text instead of content[].
			for _, o := range apiResp.Output {
				if o.Text != "" {
					return stripReasoning(o.Text), nil
				}
			}
			return "", fmt.Errorf("%w: anthropic: model returned no text (stop=%s blocks=%d raw=%s)",
				ErrLLMCall, apiResp.StopReason, len(apiResp.Content), truncateForLog(string(rawBody)))
		}

		// Dispatch tool calls.
		var toolResults []toolResultBlock
		for _, block := range apiResp.Content {
			if block.Type != "tool_use" {
				continue
			}
			tool, ok := tools[block.Name]
			var result string
			if !ok {
				result = fmt.Sprintf(`{"error":"unknown tool %q"}`, block.Name)
			} else {
				res, err := tool.Handler(ctx, block.Input)
				if err != nil {
					result = fmt.Sprintf(`{"error":%q}`, err.Error())
				} else {
					result = string(res)
				}
			}
			toolResults = append(toolResults, toolResultBlock{
				Type:      "tool_result",
				ToolUseID: block.ID,
				Content:   result,
			})
		}
		msgs = append(msgs, anthropicMessage{Role: "user", Content: toolResults})
	}
	return "", fmt.Errorf("%w: anthropic: exceeded max turns (%d)", ErrLLMCall, maxTurns)
}

// doTurn performs a single Anthropic Messages API request under an optional
// per-turn deadline. Pulled out of Complete's loop so the per-turn cancel
// runs immediately after each iteration instead of stacking via defer.
func (a *AnthropicAdapter) doTurn(
	ctx context.Context,
	turnTimeout time.Duration,
	body []byte,
) ([]byte, int, error) {
	callCtx := ctx
	var cancel context.CancelFunc
	if turnTimeout > 0 {
		callCtx, cancel = context.WithTimeout(ctx, turnTimeout)
		defer cancel()
	}

	httpReq, err := http.NewRequestWithContext(callCtx, http.MethodPost, a.baseURL, bytes.NewReader(body))
	if err != nil {
		return nil, 0, fmt.Errorf("build request: %w", err)
	}
	httpReq.Header.Set("x-api-key", a.apiKey)
	httpReq.Header.Set("anthropic-version", anthropicVersion)
	httpReq.Header.Set("content-type", "application/json")

	resp, err := a.client.Do(httpReq)
	if err != nil {
		return nil, 0, fmt.Errorf("http: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("read body: %w", err)
	}
	return raw, resp.StatusCode, nil
}

// looksLikeSSE reports whether the body is a Server-Sent Events stream
// rather than a single JSON object. Anthropic-compatible proxies sometimes
// stream by default and return event:/data: lines even when stream was not
// requested. We sniff the leading non-space bytes for the SSE "event:" or
// "data:" prefix; a normal JSON response begins with '{' or '['.
func looksLikeSSE(body []byte) bool {
	trimmed := bytes.TrimLeft(body, " \t\r\n")
	return bytes.HasPrefix(trimmed, []byte("event:")) || bytes.HasPrefix(trimmed, []byte("data:"))
}

// sseEvent is the decoded JSON payload of a single `data:` line in an
// Anthropic message stream. Only the fields we reassemble are modelled.
type sseEvent struct {
	Type    string `json:"type"`
	Index   int    `json:"index"`
	Message *struct {
		StopReason string         `json:"stop_reason"`
		Usage      anthropicUsage `json:"usage"`
	} `json:"message,omitempty"`
	ContentBlock *struct {
		Type string `json:"type"`
		ID   string `json:"id"`
		Name string `json:"name"`
		Text string `json:"text"`
	} `json:"content_block,omitempty"`
	Delta *struct {
		Type        string `json:"type"`
		Text        string `json:"text"`
		PartialJSON string `json:"partial_json"`
		StopReason  string `json:"stop_reason"`
	} `json:"delta,omitempty"`
	Usage *anthropicUsage `json:"usage,omitempty"`
}

// truncateForLog caps a raw request/response body to a short preview so the
// multi-megabyte SSE streams Anthropic returns never flood the stderr log,
// the rotating file, or the TUI Activity & Log panel. Full bodies are only
// useful behind a network proxy, not in the activity feed.
func truncateForLog(s string) string {
	const max = 300
	if len(s) <= max {
		return s
	}
	return s[:max] + fmt.Sprintf("…(+%d bytes truncated)", len(s)-max)
}

// parseAnthropicSSE reassembles an Anthropic message stream into the same
// anthropicResponse the non-streaming path produces. It accumulates text
// deltas and tool-use input_json deltas per content-block index, and pulls
// stop_reason + usage from the message_start / message_delta events.
//
// The stream shape (verified against the live proxy):
//
//	event: message_start      -> message.usage (input tokens)
//	event: content_block_start -> content_block{type,id,name,text}
//	event: content_block_delta -> delta{text_delta | input_json_delta}
//	event: content_block_stop
//	event: message_delta       -> delta.stop_reason, usage (output tokens)
//	event: message_stop
func parseAnthropicSSE(body []byte) (anthropicResponse, error) {
	var resp anthropicResponse

	// Per-index accumulators. text holds text_delta runs; toolJSON holds the
	// concatenated input_json_delta fragments for a tool_use block.
	type blockAcc struct {
		typ      string
		text     *strings.Builder
		toolJSON *strings.Builder
		id       string
		name     string
		order    int
	}
	blocks := map[int]*blockAcc{}
	next := 0
	get := func(idx int) *blockAcc {
		b, ok := blocks[idx]
		if !ok {
			b = &blockAcc{text: &strings.Builder{}, toolJSON: &strings.Builder{}, order: next}
			blocks[idx] = b
			next++
		}
		return b
	}

	sc := bufio.NewScanner(bytes.NewReader(body))
	// Allow long data lines (large tool inputs / text runs).
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	sawEvent := false
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" || payload == "[DONE]" {
			continue
		}
		var ev sseEvent
		if err := json.Unmarshal([]byte(payload), &ev); err != nil {
			// Skip non-JSON keep-alive/comment frames rather than failing the
			// whole turn on a single malformed line.
			continue
		}
		sawEvent = true

		switch ev.Type {
		case "message_start":
			if ev.Message != nil {
				resp.Usage.InputTokens = ev.Message.Usage.InputTokens
				resp.Usage.CacheReadInputTokens = ev.Message.Usage.CacheReadInputTokens
				resp.Usage.CacheCreationInputTokens = ev.Message.Usage.CacheCreationInputTokens
			}
		case "content_block_start":
			b := get(ev.Index)
			if ev.ContentBlock != nil {
				b.typ = ev.ContentBlock.Type
				b.id = ev.ContentBlock.ID
				b.name = ev.ContentBlock.Name
				if ev.ContentBlock.Text != "" {
					b.text.WriteString(ev.ContentBlock.Text)
				}
			}
		case "content_block_delta":
			b := get(ev.Index)
			if ev.Delta != nil {
				switch ev.Delta.Type {
				case "text_delta":
					b.text.WriteString(ev.Delta.Text)
				case "input_json_delta":
					b.toolJSON.WriteString(ev.Delta.PartialJSON)
				}
			}
		case "message_delta":
			if ev.Delta != nil && ev.Delta.StopReason != "" {
				resp.StopReason = ev.Delta.StopReason
			}
			if ev.Usage != nil {
				if ev.Usage.OutputTokens > 0 {
					resp.Usage.OutputTokens = ev.Usage.OutputTokens
				}
				if ev.Usage.InputTokens > 0 {
					resp.Usage.InputTokens = ev.Usage.InputTokens
				}
			}
		}
	}
	if err := sc.Err(); err != nil {
		return resp, fmt.Errorf("scan stream: %w", err)
	}
	if !sawEvent {
		return resp, fmt.Errorf("no data events in stream")
	}

	// Emit content blocks in arrival order so text precedes/loops with tools
	// exactly as the model produced them.
	ordered := make([]*blockAcc, 0, len(blocks))
	for _, b := range blocks {
		ordered = append(ordered, b)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].order < ordered[j].order })

	for _, b := range ordered {
		switch b.typ {
		case "tool_use":
			input := b.toolJSON.String()
			if input == "" {
				input = "{}"
			}
			resp.Content = append(resp.Content, contentBlock{
				Type:  "tool_use",
				ID:    b.id,
				Name:  b.name,
				Input: json.RawMessage(input),
			})
		default: // "text" (and any unknown text-bearing block)
			if b.text.Len() > 0 {
				resp.Content = append(resp.Content, contentBlock{Type: "text", Text: b.text.String()})
			}
		}
	}
	return resp, nil
}

// minCacheableSystemTokens is the rough lower bound at which attaching
// cache_control to the system prompt actually pays off. Anthropic's
// ephemeral cache has a small write overhead (~1.25× list for the write);
// below this size the write cost outweighs any read savings. We use a
// character proxy (~3 chars/token on English) so we do not need a
// tokenizer here.
const minCacheableSystemChars = 2048 // ~650+ tokens

// buildSystemBlocks converts the flat system prompt into Anthropic's block
// form and attaches cache_control to the text when it is large enough to
// be worth caching. Small/empty prompts are returned unchanged (or omitted
// when empty) so we do not pay the cache-write premium on tiny prompts.
func buildSystemBlocks(systemPrompt string) []systemBlock {
	if systemPrompt == "" {
		return nil
	}
	blk := systemBlock{Type: "text", Text: systemPrompt}
	if len(systemPrompt) >= minCacheableSystemChars {
		blk.CacheControl = &cacheControl{Type: "ephemeral"}
	}
	return []systemBlock{blk}
}

// buildAnthropicTools converts the tool map into the Anthropic wire format.
// When at least one tool is present, cache_control is attached to the
// final tool so the entire tools block becomes cacheable (Anthropic treats
// all preceding tool blocks as part of the same cache prefix). This pays
// off for agents like Screening that call the LLM many times with an
// identical tool set.
//
// The map iteration order is non-deterministic, so we sort by name to keep
// the cached prefix stable across calls — otherwise every turn would
// write a fresh cache entry.
func buildAnthropicTools(tools map[string]port.Tool) []anthropicTool {
	if len(tools) == 0 {
		return nil
	}
	names := make([]string, 0, len(tools))
	for name := range tools {
		names = append(names, name)
	}
	sort.Strings(names)

	out := make([]anthropicTool, 0, len(tools))
	for _, name := range names {
		t := tools[name]
		schema := t.Definition.InputSchema
		if schema == nil {
			schema = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		out = append(out, anthropicTool{
			Name:        t.Definition.Name,
			Description: t.Definition.Description,
			InputSchema: schema,
		})
	}
	// Mark the final tool as the cache breakpoint. Anthropic caches the
	// full prefix up to (and including) the marked block.
	out[len(out)-1].CacheControl = &cacheControl{Type: "ephemeral"}
	return out
}

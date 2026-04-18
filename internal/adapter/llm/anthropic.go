package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"

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
		client:  &http.Client{},
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
	Type  string          `json:"type"`
	Text  string          `json:"text,omitempty"`
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
}

type toolResultBlock struct {
	Type      string `json:"type"`
	ToolUseID string `json:"tool_use_id"`
	Content   string `json:"content"`
}

type anthropicRequest struct {
	Model     string             `json:"model"`
	MaxTokens int                `json:"max_tokens"`
	System    string             `json:"system,omitempty"`
	Messages  []anthropicMessage `json:"messages"`
	Tools     []anthropicTool    `json:"tools,omitempty"`
}

type anthropicTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
}

type anthropicResponse struct {
	Content    []contentBlock `json:"content"`
	StopReason string         `json:"stop_reason"`

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
	msgs := []anthropicMessage{
		{Role: "user", Content: userMessage},
	}

	anthropicTools := buildAnthropicTools(tools)

	for {
		reqBody := anthropicRequest{
			Model:     a.modelID,
			MaxTokens: a.maxToks,
			System:    systemPrompt,
			Messages:  msgs,
		}
		if len(anthropicTools) > 0 {
			reqBody.Tools = anthropicTools
		}
		bodyBytes, _ := json.Marshal(reqBody)

		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, a.baseURL, bytes.NewReader(bodyBytes))
		if err != nil {
			return "", fmt.Errorf("%w: anthropic: build request: %v", ErrLLMCall, err)
		}
		httpReq.Header.Set("x-api-key", a.apiKey)
		httpReq.Header.Set("anthropic-version", anthropicVersion)
		httpReq.Header.Set("content-type", "application/json")

		slog.Debug("anthropic request", "url", a.baseURL, "model", a.modelID, "body", string(bodyBytes))

		resp, err := a.client.Do(httpReq)
		if err != nil {
			return "", fmt.Errorf("%w: anthropic: http: %v", ErrLLMCall, err)
		}
		respBody, respErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if respErr != nil {
			return "", fmt.Errorf("%w: anthropic: read body: %v", ErrLLMCall, respErr)
		}

		if resp.StatusCode != http.StatusOK {
			return "", fmt.Errorf("%w: anthropic: status %d: %s", ErrLLMCall, resp.StatusCode, string(respBody))
		}

		rawBody := respBody
		slog.Debug("anthropic raw response", "url", a.baseURL, "status", resp.StatusCode, "body", string(rawBody))

		// Detect proxy error responses wrapped in HTTP 200.
		var proxyErr proxyErrorResponse
		if json.Unmarshal(rawBody, &proxyErr) == nil && proxyErr.Msg != "" {
			return "", fmt.Errorf("%w: anthropic: proxy error (url=%s model=%s): %s",
				ErrLLMCall, a.baseURL, a.modelID, proxyErr.Msg)
		}

		var apiResp anthropicResponse
		if err := json.Unmarshal(rawBody, &apiResp); err != nil {
			return "", fmt.Errorf("%w: anthropic: decode: %v (body=%s)", ErrLLMCall, err, string(rawBody))
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
				return strings.Join(texts, "\n"), nil
			}
			// 2. Some proxies return content as a plain string.
			if apiResp.ContentStr != "" {
				return apiResp.ContentStr, nil
			}
			// 3. OpenAI-compatible proxies: choices[].message.content.
			if len(apiResp.Choices) > 0 && apiResp.Choices[0].Message.Content != "" {
				return apiResp.Choices[0].Message.Content, nil
			}
			// 4. Some proxies use output[].text instead of content[].
			for _, o := range apiResp.Output {
				if o.Text != "" {
					return o.Text, nil
				}
			}
			return "", fmt.Errorf("%w: anthropic: model returned no text (stop=%s blocks=%d raw=%s)",
				ErrLLMCall, apiResp.StopReason, len(apiResp.Content), string(rawBody))
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
}

func buildAnthropicTools(tools map[string]port.Tool) []anthropicTool {
	if len(tools) == 0 {
		return nil
	}
	out := make([]anthropicTool, 0, len(tools))
	for _, t := range tools {
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
	return out
}

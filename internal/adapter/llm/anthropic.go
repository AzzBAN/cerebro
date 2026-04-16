package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/azhar/cerebro/internal/port"
)

const anthropicAPIBase = "https://api.anthropic.com/v1/messages"
const anthropicVersion = "2023-06-01"

// AnthropicAdapter implements port.LLM using the Anthropic Claude API.
type AnthropicAdapter struct {
	apiKey  string
	modelID string
	temp    float64
	maxToks int
	client  *http.Client
}

// NewAnthropic creates a Claude LLM adapter.
func NewAnthropic(apiKey, modelID string, temperature float64, maxOutputTokens int) *AnthropicAdapter {
	return &AnthropicAdapter{
		apiKey:  apiKey,
		modelID: modelID,
		temp:    temperature,
		maxToks: maxOutputTokens,
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
}

// Complete runs the Anthropic tool-calling loop.
func (a *AnthropicAdapter) Complete(
	ctx context.Context,
	systemPrompt string,
	userMessage string,
	tools map[string]port.ToolHandler,
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
			Tools:     anthropicTools,
		}
		bodyBytes, _ := json.Marshal(reqBody)

		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, anthropicAPIBase, bytes.NewReader(bodyBytes))
		if err != nil {
			return "", fmt.Errorf("%w: anthropic: build request: %v", ErrLLMCall, err)
		}
		httpReq.Header.Set("x-api-key", a.apiKey)
		httpReq.Header.Set("anthropic-version", anthropicVersion)
		httpReq.Header.Set("content-type", "application/json")

		resp, err := a.client.Do(httpReq)
		if err != nil {
			return "", fmt.Errorf("%w: anthropic: http: %v", ErrLLMCall, err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			return "", fmt.Errorf("%w: anthropic: status %d: %s", ErrLLMCall, resp.StatusCode, string(body))
		}

		var apiResp anthropicResponse
		if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
			return "", fmt.Errorf("%w: anthropic: decode: %v", ErrLLMCall, err)
		}

		// Add assistant turn.
		msgs = append(msgs, anthropicMessage{Role: "assistant", Content: apiResp.Content})

		if apiResp.StopReason != "tool_use" {
			// Extract text from content blocks.
			for _, block := range apiResp.Content {
				if block.Type == "text" {
					return block.Text, nil
				}
			}
			return "", nil
		}

		// Dispatch tool calls.
		var toolResults []toolResultBlock
		for _, block := range apiResp.Content {
			if block.Type != "tool_use" {
				continue
			}
			handler, ok := tools[block.Name]
			var result string
			if !ok {
				result = fmt.Sprintf(`{"error":"unknown tool %q"}`, block.Name)
			} else {
				res, err := handler(ctx, block.Input)
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

func buildAnthropicTools(tools map[string]port.ToolHandler) []anthropicTool {
	out := make([]anthropicTool, 0, len(tools))
	for name := range tools {
		out = append(out, anthropicTool{
			Name:        name,
			Description: name,
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
		})
	}
	return out
}

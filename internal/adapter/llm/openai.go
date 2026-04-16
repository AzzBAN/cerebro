package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/azhar/cerebro/internal/port"
	openai "github.com/sashabaranov/go-openai"
)

// OpenAIAdapter implements port.LLM using the OpenAI-compatible API.
// Works with GPT models and any OpenAI-compatible endpoint (Ollama, LM Studio).
type OpenAIAdapter struct {
	client  *openai.Client
	modelID string
	provider string
	temp    float32
	maxToks int
}

// NewOpenAI creates an OpenAI-compatible LLM adapter.
// Set baseURL="" to use the standard OpenAI API.
func NewOpenAI(apiKey, baseURL, modelID string, temperature float64, maxOutputTokens int) *OpenAIAdapter {
	cfg := openai.DefaultConfig(apiKey)
	if baseURL != "" {
		cfg.BaseURL = baseURL
	}
	return &OpenAIAdapter{
		client:  openai.NewClientWithConfig(cfg),
		modelID: modelID,
		provider: "openai_compatible",
		temp:    float32(temperature),
		maxToks: maxOutputTokens,
	}
}

func (a *OpenAIAdapter) Provider() string { return a.provider }
func (a *OpenAIAdapter) ModelID() string  { return a.modelID }

// Complete runs a tool-calling loop until the model stops requesting tools
// or ctx deadline is exceeded. Fail-closed: any error returns ErrAgentTimeout
// (no new risk on the calling side).
func (a *OpenAIAdapter) Complete(
	ctx context.Context,
	systemPrompt string,
	userMessage string,
	tools map[string]port.ToolHandler,
) (string, error) {
	const maxTurns = 12

	msgs := []openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleSystem, Content: systemPrompt},
		{Role: openai.ChatMessageRoleUser, Content: userMessage},
	}

	openaiTools := buildOpenAITools(tools)

	for turn := 0; turn < maxTurns; turn++ {
		req := openai.ChatCompletionRequest{
			Model:       a.modelID,
			Messages:    msgs,
			Temperature: a.temp,
			MaxTokens:   a.maxToks,
		}
		if len(openaiTools) > 0 {
			req.Tools = openaiTools
		}

		resp, err := a.client.CreateChatCompletion(ctx, req)
		if err != nil {
			return "", fmt.Errorf("%w: openai: %v", ErrLLMCall, err)
		}
		if len(resp.Choices) == 0 {
			return "", fmt.Errorf("openai: empty response")
		}

		choice := resp.Choices[0]
		msgs = append(msgs, choice.Message)

		// Native OpenAI tool calls path.
		if len(choice.Message.ToolCalls) > 0 {
			for _, tc := range choice.Message.ToolCalls {
				handler, ok := tools[tc.Function.Name]
				if !ok {
					msgs = append(msgs, openai.ChatCompletionMessage{
						Role:       openai.ChatMessageRoleTool,
						ToolCallID: tc.ID,
						Content:    fmt.Sprintf(`{"error":"unknown tool %q"}`, tc.Function.Name),
					})
					continue
				}
				result, err := handler(ctx, json.RawMessage(tc.Function.Arguments))
				var content string
				if err != nil {
					content = fmt.Sprintf(`{"error":%q}`, err.Error())
				} else {
					content = string(result)
				}
				msgs = append(msgs, openai.ChatCompletionMessage{
					Role:       openai.ChatMessageRoleTool,
					ToolCallID: tc.ID,
					Content:    content,
				})
			}
			continue
		}

		// Compatibility path: some OpenAI-compatible providers emit custom
		// XML-like tool directives in message content instead of tool_calls.
		invokes := parseCompatInvokes(choice.Message.Content)
		if len(invokes) > 0 {
			var sb strings.Builder
			sb.WriteString("Tool call results:\n")
			for _, inv := range invokes {
				handler, ok := tools[inv.Name]
				if !ok {
					sb.WriteString(fmt.Sprintf("- %s: {\"error\":\"unknown tool\"}\n", inv.Name))
					continue
				}
				argsJSON, err := json.Marshal(inv.Args)
				if err != nil {
					sb.WriteString(fmt.Sprintf("- %s: {\"error\":%q}\n", inv.Name, err.Error()))
					continue
				}
				out, err := handler(ctx, json.RawMessage(argsJSON))
				if err != nil {
					sb.WriteString(fmt.Sprintf("- %s: {\"error\":%q}\n", inv.Name, err.Error()))
					continue
				}
				sb.WriteString(fmt.Sprintf("- %s: %s\n", inv.Name, string(out)))
			}
			sb.WriteString("\nNow return only the final JSON object requested by the system prompt. No prose and no XML tags.")
			msgs = append(msgs, openai.ChatCompletionMessage{
				Role:    openai.ChatMessageRoleUser,
				Content: sb.String(),
			})
			continue
		}

		// If no tool directives, treat as final answer.
		if len(choice.Message.ToolCalls) == 0 {
			return choice.Message.Content, nil
		}
	}
	return "", fmt.Errorf("%w: exceeded max turns (%d)", ErrLLMCall, maxTurns)
}

type compatInvoke struct {
	Name string
	Args map[string]string
}

var (
	invokeRe    = regexp.MustCompile(`(?s)<invoke\s+name="([^"]+)">\s*(.*?)\s*</invoke>`)
	parameterRe = regexp.MustCompile(`(?s)<parameter\s+name="([^"]+)">\s*(.*?)\s*</parameter>`)
)

func parseCompatInvokes(content string) []compatInvoke {
	matches := invokeRe.FindAllStringSubmatch(content, -1)
	if len(matches) == 0 {
		return nil
	}
	out := make([]compatInvoke, 0, len(matches))
	for _, m := range matches {
		name := strings.TrimSpace(m[1])
		body := m[2]
		args := make(map[string]string)
		for _, pm := range parameterRe.FindAllStringSubmatch(body, -1) {
			key := strings.TrimSpace(pm[1])
			val := strings.TrimSpace(pm[2])
			if key != "" {
				args[key] = val
			}
		}
		out = append(out, compatInvoke{Name: name, Args: args})
	}
	return out
}

func buildOpenAITools(tools map[string]port.ToolHandler) []openai.Tool {
	if len(tools) == 0 {
		return nil
	}
	out := make([]openai.Tool, 0, len(tools))
	for name := range tools {
		out = append(out, openai.Tool{
			Type: openai.ToolTypeFunction,
			Function: &openai.FunctionDefinition{
				Name:        name,
				Description: name, // enriched by tool registry in Phase 5 full wiring
				Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
			},
		})
	}
	return out
}

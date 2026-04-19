package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/azhar/cerebro/internal/agent"
	"github.com/azhar/cerebro/internal/port"
	openai "github.com/sashabaranov/go-openai"
)

// OpenAIAdapter implements port.LLM using the OpenAI-compatible API.
// Works with GPT models and any OpenAI-compatible endpoint (Ollama, LM Studio).
type OpenAIAdapter struct {
	client   *openai.Client
	modelID  string
	provider string
	temp     float32
	maxToks  int
}

// NewOpenAI creates an OpenAI-compatible LLM adapter.
// Set baseURL="" to use the standard OpenAI API.
func NewOpenAI(apiKey, baseURL, modelID string, temperature float64, maxOutputTokens int) *OpenAIAdapter {
	cfg := openai.DefaultConfig(apiKey)
	if baseURL != "" {
		cfg.BaseURL = baseURL
	}
	return &OpenAIAdapter{
		client:   openai.NewClientWithConfig(cfg),
		modelID:  modelID,
		provider: "openai_compatible",
		temp:     float32(temperature),
		maxToks:  maxOutputTokens,
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
	tools map[string]port.Tool,
) (string, error) {
	const defaultMaxTurns = 12
	maxTurns := agent.MaxTurnsFromCtx(ctx, defaultMaxTurns)

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
				tool, ok := tools[tc.Function.Name]
				var content string
				if !ok {
					content = fmt.Sprintf(`{"error":"unknown tool %q"}`, tc.Function.Name)
				} else {
					result, err := tool.Handler(ctx, json.RawMessage(tc.Function.Arguments))
					if err != nil {
						content = fmt.Sprintf(`{"error":%q}`, err.Error())
					} else {
						content = string(result)
					}
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
		// tool directives in message content instead of native tool_calls.
		invokes := parseCompatInvokes(choice.Message.Content)
		if len(invokes) > 0 {
			var sb strings.Builder
			sb.WriteString("Tool call results:\n")
			for _, inv := range invokes {
				tool, ok := tools[inv.Name]
				if !ok {
					sb.WriteString(fmt.Sprintf("- %s: {\"error\":\"unknown tool\"}\n", inv.Name))
					continue
				}
				argsJSON, err := json.Marshal(inv.Args)
				if err != nil {
					sb.WriteString(fmt.Sprintf("- %s: {\"error\":%q}\n", inv.Name, err.Error()))
					continue
				}
				out, err := tool.Handler(ctx, json.RawMessage(argsJSON))
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
		return choice.Message.Content, nil
	}
	return "", fmt.Errorf("%w: exceeded max turns (%d)", ErrLLMCall, maxTurns)
}

type compatInvoke struct {
	Name string
	Args map[string]string
}

var (
	invokeRe     = regexp.MustCompile(`(?s)<invoke\s+name="([^"]+)">\s*(.*?)\s*</invoke>`)
	structToolRe = regexp.MustCompile(`(?s)struct\s+Tool\s*\{\s*tool:\s*"([^"]+)"\s*,\s*args:\s*\{(.*?)\}\s*\}`)
	toolCallRe   = regexp.MustCompile(`(?s)\[TOOL_CALL\]\s*\{[^}]*tool\s*=>\s*"([^"]+)"[^}]*args\s*=>\s*\{(.*?)\}\s*\}`)
	parameterRe  = regexp.MustCompile(`(?s)<param(?:eter)?\s+name="([^"]+)">\s*(.*?)\s*</param(?:eter)?>`)
	keyValRe     = regexp.MustCompile(`"?(\w+)"?\s*=>\s*"([^"]*)"`)
)

func parseCompatInvokes(content string) []compatInvoke {
	// Pattern 1: <invoke name="...">...</invoke>
	if matches := invokeRe.FindAllStringSubmatch(content, -1); len(matches) > 0 {
		return buildInvokesFromMatches(matches)
	}
	// Pattern 2: struct Tool { tool: "...", args: { ... } }
	if matches := structToolRe.FindAllStringSubmatch(content, -1); len(matches) > 0 {
		return buildInvokesFromMatches(matches)
	}
	// Pattern 3: [TOOL_CALL] {tool => "...", args => { ... }} [/TOOL_CALL]
	if matches := toolCallRe.FindAllStringSubmatch(content, -1); len(matches) > 0 {
		return buildInvokesFromMatches(matches)
	}
	return nil
}

func buildInvokesFromMatches(matches [][]string) []compatInvoke {
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
		// Also try key => "value" pairs if no XML params found.
		if len(args) == 0 {
			for _, kv := range keyValRe.FindAllStringSubmatch(body, -1) {
				key := strings.TrimSpace(kv[1])
				val := strings.TrimSpace(kv[2])
				if key != "" {
					args[key] = val
				}
			}
		}
		out = append(out, compatInvoke{Name: name, Args: args})
	}
	return out
}

func buildOpenAITools(tools map[string]port.Tool) []openai.Tool {
	if len(tools) == 0 {
		return nil
	}
	out := make([]openai.Tool, 0, len(tools))
	for _, t := range tools {
		schema := t.Definition.InputSchema
		if schema == nil {
			schema = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		out = append(out, openai.Tool{
			Type: openai.ToolTypeFunction,
			Function: &openai.FunctionDefinition{
				Name:        t.Definition.Name,
				Description: t.Definition.Description,
				Parameters:  schema,
			},
		})
	}
	return out
}

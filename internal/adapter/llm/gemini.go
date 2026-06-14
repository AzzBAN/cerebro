package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/azhar/cerebro/internal/agent"
	"github.com/azhar/cerebro/internal/port"
)

// GeminiAdapter implements port.LLM using the Google Gemini REST API.
// Uses the generateContent endpoint with function calling.
type GeminiAdapter struct {
	apiKey  string
	modelID string
	temp    float64
	maxToks int
	client  *http.Client
}

// NewGemini creates a Gemini LLM adapter.
func NewGemini(apiKey, modelID string, temperature float64, maxOutputTokens int) *GeminiAdapter {
	return &GeminiAdapter{
		apiKey:  apiKey,
		modelID: modelID,
		temp:    temperature,
		maxToks: maxOutputTokens,
		client:  &http.Client{Timeout: defaultHTTPTimeout},
	}
}

func (g *GeminiAdapter) Provider() string { return "gemini" }
func (g *GeminiAdapter) ModelID() string  { return g.modelID }

type geminiContent struct {
	Role  string       `json:"role"`
	Parts []geminiPart `json:"parts"`
}

type geminiPart struct {
	Text             string              `json:"text,omitempty"`
	FunctionCall     *geminiFunctionCall `json:"functionCall,omitempty"`
	FunctionResponse *geminiFunctionResp `json:"functionResponse,omitempty"`
}

type geminiFunctionCall struct {
	Name string          `json:"name"`
	Args json.RawMessage `json:"args"`
}

type geminiFunctionResp struct {
	Name     string          `json:"name"`
	Response json.RawMessage `json:"response"`
}

type geminiRequest struct {
	Contents          []geminiContent `json:"contents"`
	SystemInstruction *geminiContent  `json:"systemInstruction,omitempty"`
	Tools             []geminiTool    `json:"tools,omitempty"`
	GenerationConfig  map[string]any  `json:"generationConfig,omitempty"`
}

type geminiTool struct {
	FunctionDeclarations []geminiFuncDecl `json:"functionDeclarations"`
}

type geminiFuncDecl struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

type geminiResponse struct {
	Candidates []struct {
		Content      geminiContent `json:"content"`
		FinishReason string        `json:"finishReason"`
	} `json:"candidates"`
	UsageMetadata geminiUsage `json:"usageMetadata"`
}

// geminiUsage matches Gemini's per-response token accounting.
// CachedContentTokenCount is the portion served from context cache (when
// enabled); we surface it so cost estimation can apply the cached rate.
type geminiUsage struct {
	PromptTokenCount        int `json:"promptTokenCount"`
	CandidatesTokenCount    int `json:"candidatesTokenCount"`
	CachedContentTokenCount int `json:"cachedContentTokenCount"`
}

// Complete runs a Gemini function-calling loop.
func (g *GeminiAdapter) Complete(
	ctx context.Context,
	systemPrompt string,
	userMessage string,
	tools map[string]port.Tool,
) (string, error) {
	const defaultMaxTurns = 12
	maxTurns := agent.MaxTurnsFromCtx(ctx, defaultMaxTurns)

	contents := []geminiContent{
		{Role: "user", Parts: []geminiPart{{Text: userMessage}}},
	}

	geminiTools := buildGeminiTools(tools)

	baseURL := fmt.Sprintf(
		"https://generativelanguage.googleapis.com/v1beta/models/%s:generateContent?key=%s",
		g.modelID, g.apiKey,
	)

	turnTimeout := agent.TurnTimeoutFromCtx(ctx)
	maxToks := agent.MaxTokensFromCtx(ctx, g.maxToks)

	for turn := 0; turn < maxTurns; turn++ {
		reqBody := geminiRequest{
			Contents: contents,
			SystemInstruction: &geminiContent{
				Parts: []geminiPart{{Text: systemPrompt}},
			},
			Tools: geminiTools,
			GenerationConfig: map[string]any{
				"temperature":     g.temp,
				"maxOutputTokens": maxToks,
			},
		}

		body, _ := json.Marshal(reqBody)
		rawBody, status, err := g.doTurn(ctx, turnTimeout, baseURL, body)
		if err != nil {
			return "", fmt.Errorf("%w: gemini: %w", ErrLLMCall, err)
		}
		if status != http.StatusOK {
			return "", fmt.Errorf("%w: gemini: status %d: %s", ErrLLMCall, status, string(rawBody))
		}

		var apiResp geminiResponse
		if err := json.Unmarshal(rawBody, &apiResp); err != nil {
			return "", fmt.Errorf("%w: gemini: decode: %v", ErrLLMCall, err)
		}

		// Record per-turn usage. CachedContentTokenCount is a subset of
		// PromptTokenCount — the cached portion — so it feeds the cached
		// bucket and the uncached rate applies to the remainder.
		if u := agent.UsageFromCtx(ctx); u != nil {
			u.Add(apiResp.UsageMetadata.PromptTokenCount,
				apiResp.UsageMetadata.CandidatesTokenCount,
				apiResp.UsageMetadata.CachedContentTokenCount)
		}

		if len(apiResp.Candidates) == 0 {
			return "", fmt.Errorf("gemini: no candidates in response")
		}

		candidate := apiResp.Candidates[0]
		contents = append(contents, candidate.Content)

		// Check for function calls.
		var functionCalls []geminiPart
		var textContent string
		for _, p := range candidate.Content.Parts {
			if p.FunctionCall != nil {
				functionCalls = append(functionCalls, p)
			} else if p.Text != "" {
				textContent = p.Text
			}
		}

		if len(functionCalls) == 0 {
			return stripReasoning(textContent), nil
		}

		// Dispatch function calls.
		var responseParts []geminiPart
		for _, fc := range functionCalls {
			tool, ok := tools[fc.FunctionCall.Name]
			var respJSON json.RawMessage
			if !ok {
				respJSON = json.RawMessage(fmt.Sprintf(`{"error":"unknown tool %q"}`, fc.FunctionCall.Name))
			} else {
				res, err := tool.Handler(ctx, fc.FunctionCall.Args)
				if err != nil {
					respJSON = json.RawMessage(fmt.Sprintf(`{"error":%q}`, err.Error()))
				} else {
					respJSON = res
				}
			}
			responseParts = append(responseParts, geminiPart{
				FunctionResponse: &geminiFunctionResp{
					Name:     fc.FunctionCall.Name,
					Response: respJSON,
				},
			})
		}
		contents = append(contents, geminiContent{Role: "function", Parts: responseParts})
	}
	return "", fmt.Errorf("%w: gemini: exceeded max turns (%d)", ErrLLMCall, maxTurns)
}

// doTurn performs a single Gemini API request under an optional per-turn
// deadline. Pulled out of the main loop so the per-turn cancel runs
// immediately at the end of each iteration rather than stacking via defer.
func (g *GeminiAdapter) doTurn(
	ctx context.Context,
	turnTimeout time.Duration,
	url string,
	body []byte,
) ([]byte, int, error) {
	callCtx := ctx
	var cancel context.CancelFunc
	if turnTimeout > 0 {
		callCtx, cancel = context.WithTimeout(ctx, turnTimeout)
		defer cancel()
	}

	httpReq, err := http.NewRequestWithContext(callCtx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, 0, fmt.Errorf("build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := g.client.Do(httpReq)
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

func buildGeminiTools(tools map[string]port.Tool) []geminiTool {
	if len(tools) == 0 {
		return nil
	}
	decls := make([]geminiFuncDecl, 0, len(tools))
	for _, t := range tools {
		schema := t.Definition.InputSchema
		if schema == nil {
			schema = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		decls = append(decls, geminiFuncDecl{
			Name:        t.Definition.Name,
			Description: t.Definition.Description,
			Parameters:  schema,
		})
	}
	return []geminiTool{{FunctionDeclarations: decls}}
}

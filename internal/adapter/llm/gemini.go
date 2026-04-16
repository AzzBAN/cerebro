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
		client:  &http.Client{},
	}
}

func (g *GeminiAdapter) Provider() string { return "gemini" }
func (g *GeminiAdapter) ModelID() string  { return g.modelID }

type geminiContent struct {
	Role  string      `json:"role"`
	Parts []geminiPart `json:"parts"`
}

type geminiPart struct {
	Text         string              `json:"text,omitempty"`
	FunctionCall *geminiFunctionCall `json:"functionCall,omitempty"`
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
		Content       geminiContent `json:"content"`
		FinishReason  string        `json:"finishReason"`
	} `json:"candidates"`
}

// Complete runs a Gemini function-calling loop.
func (g *GeminiAdapter) Complete(
	ctx context.Context,
	systemPrompt string,
	userMessage string,
	tools map[string]port.ToolHandler,
) (string, error) {
	contents := []geminiContent{
		{Role: "user", Parts: []geminiPart{{Text: userMessage}}},
	}

	var geminiTools []geminiTool
	if len(tools) > 0 {
		decls := make([]geminiFuncDecl, 0, len(tools))
		for name := range tools {
			decls = append(decls, geminiFuncDecl{
				Name:        name,
				Description: name,
				Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
			})
		}
		geminiTools = []geminiTool{{FunctionDeclarations: decls}}
	}

	baseURL := fmt.Sprintf(
		"https://generativelanguage.googleapis.com/v1beta/models/%s:generateContent?key=%s",
		g.modelID, g.apiKey,
	)

	for {
		reqBody := geminiRequest{
			Contents: contents,
			SystemInstruction: &geminiContent{
				Parts: []geminiPart{{Text: systemPrompt}},
			},
			Tools: geminiTools,
			GenerationConfig: map[string]any{
				"temperature":     g.temp,
				"maxOutputTokens": g.maxToks,
			},
		}

		body, _ := json.Marshal(reqBody)
		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL, bytes.NewReader(body))
		if err != nil {
			return "", fmt.Errorf("%w: gemini: build request: %v", ErrLLMCall, err)
		}
		httpReq.Header.Set("Content-Type", "application/json")

		resp, err := g.client.Do(httpReq)
		if err != nil {
			return "", fmt.Errorf("%w: gemini: http: %v", ErrLLMCall, err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			raw, _ := io.ReadAll(resp.Body)
			return "", fmt.Errorf("%w: gemini: status %d: %s", ErrLLMCall, resp.StatusCode, string(raw))
		}

		var apiResp geminiResponse
		if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
			return "", fmt.Errorf("%w: gemini: decode: %v", ErrLLMCall, err)
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
			return textContent, nil
		}

		// Dispatch function calls.
		var responseParts []geminiPart
		for _, fc := range functionCalls {
			handler, ok := tools[fc.FunctionCall.Name]
			var respJSON json.RawMessage
			if !ok {
				respJSON = json.RawMessage(fmt.Sprintf(`{"error":"unknown tool %q"}`, fc.FunctionCall.Name))
			} else {
				res, err := handler(ctx, fc.FunctionCall.Args)
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
}

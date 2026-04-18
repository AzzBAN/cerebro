package port

import (
	"context"
	"encoding/json"
)

// ToolHandler is a Go function that backs a single LLM tool / function call.
type ToolHandler func(ctx context.Context, input json.RawMessage) (json.RawMessage, error)

// ToolDefinition describes a tool that can be offered to the LLM.
type ToolDefinition struct {
	Name        string
	Description string
	// InputSchema is a JSON Schema object describing the expected input.
	InputSchema map[string]any
}

// Tool binds a handler with its schema definition so the LLM knows how to call it.
type Tool struct {
	Handler    ToolHandler
	Definition ToolDefinition
}

// LLM abstracts a single model provider (Gemini, Claude, OpenAI-compatible).
type LLM interface {
	// Complete runs a tool-calling loop until the model stops calling tools
	// or the context deadline is exceeded.
	Complete(
		ctx context.Context,
		systemPrompt string,
		userMessage string,
		tools map[string]Tool,
	) (string, error)

	// Provider returns a short identifier for the LLM provider (e.g. "gemini", "anthropic").
	Provider() string

	// ModelID returns the model identifier string used in API calls.
	ModelID() string
}

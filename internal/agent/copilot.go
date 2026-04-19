package agent

import (
	"context"
	"fmt"

	_ "embed"

	"github.com/azhar/cerebro/internal/domain"
	"github.com/azhar/cerebro/internal/port"
)

//go:embed prompts/copilot.tmpl
var copilotPrompt string

// Copilot handles conversational /ask queries from operators.
// It has read-only access (approve_and_route_order and halt tools are denied by policy).
// Fail-closed: any LLM error returns a safe "unable to process" message.
type Copilot struct {
	runtime *Runtime
	tools   map[string]port.Tool
}

// NewCopilot creates a Copilot agent.
func NewCopilot(runtime *Runtime, tools map[string]port.Tool) *Copilot {
	return &Copilot{runtime: runtime, tools: tools}
}

// Ask answers a free-form operator query. Non-blocking on the trading path.
func (c *Copilot) Ask(ctx context.Context, query string) (string, error) {
	result := c.runtime.Invoke(ctx, domain.AgentCopilot, copilotPrompt, query, c.tools, "copilot_response",
		"Answering operator query")
	if result.Err != nil {
		return "", fmt.Errorf("copilot: %w", result.Err)
	}
	return result.Output, nil
}

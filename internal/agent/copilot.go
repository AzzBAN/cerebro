package agent

import (
	"context"
	"fmt"
	"strings"
	"sync"

	_ "embed"

	"github.com/azhar/cerebro/internal/domain"
	"github.com/azhar/cerebro/internal/port"
)

//go:embed prompts/copilot.tmpl
var copilotPrompt string

const (
	// copilotMaxHistoryTurns caps how many prior (operator, copilot) exchanges
	// are replayed as context on each new query. Bounds token usage while still
	// letting the operator refer back to their last message ("why?", "that one",
	// "the first symbol you listed").
	copilotMaxHistoryTurns = 6

	// copilotMaxHistoryCharsPerEntry truncates any single stored query or
	// response so one tool-result-laden answer can't blow up the context fed
	// into later turns. Replies shown to the operator are never truncated —
	// only the copy retained for conversational memory.
	copilotMaxHistoryCharsPerEntry = 2000
)

// copilotTurn is one stored exchange in the conversation buffer.
type copilotTurn struct {
	query    string
	response string
}

// Copilot handles conversational /ask queries from operators.
// It has read-only access (approve_and_route_order and halt tools are denied by policy).
// Fail-closed: any LLM error returns a safe "unable to process" message.
//
// Copilot keeps a bounded, thread-safe buffer of recent turns so it can answer
// follow-ups that reference earlier messages in the same session. The buffer is
// shared across entry points (TUI and chatops) because there is a single
// operator session per running instance.
type Copilot struct {
	runtime *Runtime
	tools   map[string]port.Tool

	mu      sync.Mutex
	history []copilotTurn
}

// NewCopilot creates a Copilot agent.
func NewCopilot(runtime *Runtime, tools map[string]port.Tool) *Copilot {
	return &Copilot{runtime: runtime, tools: tools}
}

// Ask answers a free-form operator query. Non-blocking on the trading path.
// Prior turns from the session are replayed as context so the copilot can
// resolve references to earlier messages. Only successful exchanges are
// retained — a failed turn leaves the history untouched.
func (c *Copilot) Ask(ctx context.Context, query string) (string, error) {
	message := c.composeMessage(query)

	result := c.runtime.Invoke(ctx, domain.AgentCopilot, copilotPrompt, message, c.tools, "copilot_response",
		"Answering operator query")
	if result.Err != nil {
		return "", fmt.Errorf("copilot: %w", result.Err)
	}

	c.record(query, result.Output)
	return result.Output, nil
}

// composeMessage prepends a snapshot of the conversation buffer to the current
// query. With no history it returns the query unchanged, preserving the exact
// behaviour of a first-message-only copilot.
func (c *Copilot) composeMessage(query string) string {
	c.mu.Lock()
	hist := make([]copilotTurn, len(c.history))
	copy(hist, c.history)
	c.mu.Unlock()

	if len(hist) == 0 {
		return query
	}

	var b strings.Builder
	b.WriteString("## Conversation so far (oldest first — for context; do not re-answer these)\n")
	for _, t := range hist {
		b.WriteString("Operator: ")
		b.WriteString(t.query)
		b.WriteString("\nYou: ")
		b.WriteString(t.response)
		b.WriteString("\n\n")
	}
	b.WriteString("## Current message\n")
	b.WriteString(query)
	return b.String()
}

// record appends a completed exchange to the buffer and trims it to the most
// recent copilotMaxHistoryTurns. Stored text is truncated per entry to bound
// the context replayed on later turns.
func (c *Copilot) record(query, response string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.history = append(c.history, copilotTurn{
		query:    truncateHistory(query),
		response: truncateHistory(response),
	})
	if len(c.history) > copilotMaxHistoryTurns {
		c.history = c.history[len(c.history)-copilotMaxHistoryTurns:]
	}
}

func truncateHistory(s string) string {
	if len(s) <= copilotMaxHistoryCharsPerEntry {
		return s
	}
	return s[:copilotMaxHistoryCharsPerEntry] + "…"
}

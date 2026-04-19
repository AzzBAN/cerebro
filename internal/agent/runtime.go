package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/azhar/cerebro/internal/config"
	"github.com/azhar/cerebro/internal/domain"
	"github.com/azhar/cerebro/internal/port"
	"github.com/google/uuid"
)

type contextKey string

const maxTurnsKey contextKey = "max_turns"

// WithMaxTurns stores the configured turn limit in the context for LLM adapters.
func WithMaxTurns(ctx context.Context, n int) context.Context {
	return context.WithValue(ctx, maxTurnsKey, n)
}

// MaxTurnsFromCtx returns the turn limit stored in ctx, or the given default.
func MaxTurnsFromCtx(ctx context.Context, defaultVal int) int {
	if v, ok := ctx.Value(maxTurnsKey).(int); ok && v > 0 {
		return v
	}
	return defaultVal
}

// AgentStep represents the current step in an agent's ReAct loop.
type AgentStep string

const (
	StepThinking  AgentStep = "THINKING"
	StepTool      AgentStep = "TOOL"
	StepObserving AgentStep = "OBSERVING"
	StepStreaming AgentStep = "STREAMING"
	StepComplete  AgentStep = "COMPLETE"
	StepError     AgentStep = "ERROR"
)

// StepNotifyFunc is called on every ReAct step transition.
// All parameters are informational; the receiver must not block.
// description is a human-readable label for the current step.
// stepNum is the 1-based step counter; maxSteps is the configured turn limit.
type StepNotifyFunc func(agent string, runID string, step AgentStep, toolName string, description string, stepNum int, maxSteps int)

// Runtime manages a single agent invocation: tool loop, timeout, cost tracking,
// and fail-closed behaviour on any error.
type Runtime struct {
	llm      port.LLM
	logStore port.AgentLogStore
	cfg      config.AgentConfig
	onStep   StepNotifyFunc
}

// NewRuntime creates an agent Runtime.
func NewRuntime(llm port.LLM, logStore port.AgentLogStore, cfg config.AgentConfig) *Runtime {
	return &Runtime{llm: llm, logStore: logStore, cfg: cfg}
}

// SetOnStep registers a callback that fires on every ReAct step transition.
func (r *Runtime) SetOnStep(fn StepNotifyFunc) { r.onStep = fn }

func (r *Runtime) notifyStep(agent, runID string, step AgentStep, toolName, content string, stepNum, maxSteps int) {
	if r.onStep != nil {
		r.onStep(agent, runID, step, toolName, content, stepNum, maxSteps)
	}
}

// InvokeResult holds the output of a single agent run.
type InvokeResult struct {
	RunID   string
	Output  string
	Outcome string
	Err     error
}

// Invoke runs the agent with the given system prompt, user message, and tool set.
// Enforces total timeout from config. Returns ErrAgentTimeout on any failure.
// All invocation metadata is persisted to agent_runs.
// description is a human-readable label shown in the TUI during the run.
func (r *Runtime) Invoke(
	ctx context.Context,
	agent domain.AgentRole,
	systemPrompt string,
	userMessage string,
	tools map[string]port.Tool,
	outcome string,
	description string,
) InvokeResult {
	runID := uuid.New().String()
	start := time.Now()
	agentName := string(agent)

	maxSteps := r.cfg.MaxTurns
	if maxSteps <= 0 {
		maxSteps = 20
	}

	r.notifyStep(agentName, runID, StepThinking, "", description, 1, maxSteps)

	// Enforce per-invocation total timeout.
	totalTimeout := time.Duration(r.cfg.TimeoutTotalSeconds) * time.Second
	invokeCtx, cancel := context.WithTimeout(ctx, totalTimeout)
	defer cancel()
	invokeCtx = WithMaxTurns(invokeCtx, maxSteps)

	// Wrap tools to emit TOOL/OBSERVING step transitions.
	stepCounter := 1 // THINKING is step 1
	notifyingTools := r.wrapToolsWithNotify(agentName, runID, tools, &stepCounter, maxSteps)

	// Apply per-turn timeout via the LLM context (tool loop enforces max turns internally).
	// Retry transient errors (timeout/connection) with exponential backoff.
	maxRetries := max(r.cfg.RetryOnTransient, 0)

	var output string
	var err error
retryLoop:
	for attempt := 0; attempt <= maxRetries; attempt++ {
		output, err = r.llm.Complete(invokeCtx, systemPrompt, userMessage, notifyingTools)
		if err == nil {
			break
		}
		if !isTransient(err) || attempt == maxRetries {
			break
		}
		backoff := time.Duration(1<<uint(attempt)) * time.Second
		slog.Warn("agent invocation transient error; retrying",
			"agent", agent, "run_id", runID, "attempt", attempt+1, "backoff", backoff, "error", err)
		select {
		case <-invokeCtx.Done():
			break retryLoop
		case <-time.After(backoff):
		}
	}

	latencyMS := int(time.Since(start).Milliseconds())

	run := domain.AgentRun{
		ID:        runID,
		Agent:     agent,
		Model:     r.llm.ModelID(),
		Provider:  r.llm.Provider(),
		LatencyMS: latencyMS,
		Outcome:   outcome,
		CreatedAt: start,
	}

	if err != nil {
		run.Error = err.Error()
		r.notifyStep(agentName, runID, StepError, "", err.Error(), stepCounter, maxSteps)
		slog.Error("agent invocation failed",
			"agent", agent, "run_id", runID, "error", err, "latency_ms", latencyMS)
		_ = r.logStore.SaveRun(ctx, run)
		return InvokeResult{
			RunID: runID,
			Err:   fmt.Errorf("%w: %v", domain.ErrAgentTimeout, err),
		}
	}

	r.notifyStep(agentName, runID, StepComplete, "", output, stepCounter, maxSteps)
	slog.Info("agent invocation complete",
		"agent", agent, "run_id", runID, "latency_ms", latencyMS)
	_ = r.logStore.SaveRun(ctx, run)

	return InvokeResult{RunID: runID, Output: output, Outcome: outcome}
}

func (r *Runtime) wrapToolsWithNotify(agent, runID string, tools map[string]port.Tool, stepCounter *int, maxSteps int) map[string]port.Tool {
	if r.onStep == nil || len(tools) == 0 {
		return tools
	}
	wrapped := make(map[string]port.Tool, len(tools))
	for name, t := range tools {
		name, origHandler := name, t.Handler
		desc := describeTool(name)
		t.Handler = func(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
			*stepCounter++
			r.notifyStep(agent, runID, StepTool, name, desc, *stepCounter, maxSteps)
			result, err := origHandler(ctx, input)
			r.notifyStep(agent, runID, StepObserving, name, desc, *stepCounter, maxSteps)
			return result, err
		}
		wrapped[name] = t
	}
	return wrapped
}

// describeTool maps a tool name to a human-readable description for the TUI.
func describeTool(name string) string {
	switch name {
	case "get_market_data":
		return "Fetching market data"
	case "get_derivatives_data":
		return "Fetching derivatives data"
	case "fetch_latest_news":
		return "Fetching latest news"
	case "get_economic_events":
		return "Checking economic calendar"
	case "get_all_market_data":
		return "Comparing market data across symbols"
	case "get_positions":
		return "Fetching open positions"
	case "get_ticker":
		return "Fetching ticker data"
	default:
		return name
	}
}

func isTransient(err error) bool {
	return errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled)
}

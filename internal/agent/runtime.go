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

const (
	maxTurnsKey       contextKey = "max_turns"
	turnTimeoutKey    contextKey = "turn_timeout"
	maxTokensKey      contextKey = "max_tokens"
)

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

// WithTurnTimeout stores the per-turn deadline for LLM adapters.
func WithTurnTimeout(ctx context.Context, d time.Duration) context.Context {
	return context.WithValue(ctx, turnTimeoutKey, d)
}

// TurnTimeoutFromCtx returns the per-turn timeout stored in ctx, or zero (no per-turn limit).
func TurnTimeoutFromCtx(ctx context.Context) time.Duration {
	if v, ok := ctx.Value(turnTimeoutKey).(time.Duration); ok && v > 0 {
		return v
	}
	return 0
}

// WithMaxTokens stores a per-invocation override for the LLM `max_tokens`
// (output-token cap) parameter. Callers use this when a specific agent
// phase legitimately needs a larger output budget than the global default
// configured via `agent.llm.models.<provider>.max_output_tokens` — e.g. the
// cross-symbol screening Phase 2 emits a multi-entry JSON array that does
// not fit within the bias-score-calibrated default. Adapters fall back to
// their configured default when no override is set.
func WithMaxTokens(ctx context.Context, n int) context.Context {
	return context.WithValue(ctx, maxTokensKey, n)
}

// MaxTokensFromCtx returns the per-invocation max_tokens override stored in
// ctx, or the given default when none was set or it was non-positive.
func MaxTokensFromCtx(ctx context.Context, defaultVal int) int {
	if v, ok := ctx.Value(maxTokensKey).(int); ok && v > 0 {
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
	cost     *CostTracker // optional; when non-nil, every invocation records its usage
}

// NewRuntime creates an agent Runtime.
func NewRuntime(llm port.LLM, logStore port.AgentLogStore, cfg config.AgentConfig) *Runtime {
	return &Runtime{llm: llm, logStore: logStore, cfg: cfg}
}

// SetOnStep registers a callback that fires on every ReAct step transition.
func (r *Runtime) SetOnStep(fn StepNotifyFunc) { r.onStep = fn }

// SetCostTracker wires a CostTracker that receives every invocation's token
// counts and estimated cost. Pass nil to disable. Safe to call once during
// composition; not safe to call after the first Invoke.
func (r *Runtime) SetCostTracker(c *CostTracker) { r.cost = c }

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

	// Attach a per-invocation Usage accumulator so LLM adapters can report
	// token counts as they complete each API turn. We read the totals
	// after Complete returns to record them in the CostTracker.
	usage := &Usage{}
	invokeCtx = WithUsage(invokeCtx, usage)

	// Store per-turn timeout in context so LLM adapters can apply it to each
	// individual API call. This prevents a single slow turn from consuming
	// the entire invocation budget.
	if r.cfg.TimeoutPerTurnSeconds > 0 {
		invokeCtx = WithTurnTimeout(invokeCtx, time.Duration(r.cfg.TimeoutPerTurnSeconds)*time.Second)
	}

	// Wrap tools to emit TOOL/OBSERVING step transitions.
	stepCounter := 1 // THINKING is step 1
	notifyingTools := r.wrapToolsWithNotify(agentName, runID, tools, &stepCounter, maxSteps)

	// Retry transient errors (per-turn timeout / connection) with exponential
	// backoff. Errors are deemed transient when the underlying chain unwraps
	// to context.DeadlineExceeded — adapters now use %w to preserve the chain
	// across the FallbackChain wrap, so this finally fires as intended.
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
		// Don't bother retrying if the parent invocation budget is gone.
		if invokeCtx.Err() != nil {
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

	// Read token counters populated by the LLM adapters and forward to the
	// CostTracker (best-effort; tracker errors are logged, not fatal).
	inTok, outTok, cachedTok := usage.Snapshot()
	r.recordUsage(ctx, inTok, outTok, cachedTok)

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
			"agent", agent, "run_id", runID, "error", err, "latency_ms", latencyMS,
			"input_tokens", inTok, "output_tokens", outTok, "cached_input_tokens", cachedTok)
		_ = r.logStore.SaveRun(ctx, run)
		return InvokeResult{RunID: runID, Err: wrapAgentTimeout(err)}
	}

	r.notifyStep(agentName, runID, StepComplete, "", output, stepCounter, maxSteps)
	slog.Info("agent invocation complete",
		"agent", agent, "run_id", runID, "latency_ms", latencyMS,
		"input_tokens", inTok, "output_tokens", outTok, "cached_input_tokens", cachedTok)
	_ = r.logStore.SaveRun(ctx, run)

	return InvokeResult{RunID: runID, Output: output, Outcome: outcome}
}

// recordUsage forwards observed token counts to the CostTracker when one is
// configured. Price estimation is best-effort: unknown models record 0 cost
// (CostTracker's token budget still fires independently).
//
// We use a background context for the Record call so budget writes survive
// cancellation of the parent invocation; the Redis write is tiny and worth
// completing even when the agent itself timed out.
func (r *Runtime) recordUsage(parent context.Context, inputTokens, outputTokens, cachedInputTokens int) {
	if r.cost == nil || (inputTokens == 0 && outputTokens == 0) {
		return
	}
	pricing := LookupPricing(r.llm.ModelID())
	costMicroUSD := pricing.EstimateCostMicroUSD(inputTokens, outputTokens, cachedInputTokens)

	// Detach from parent deadline; use a short timeout so a stuck Redis
	// never blocks the caller. parent may already be cancelled.
	recordCtx, cancel := context.WithTimeout(context.WithoutCancel(parent), 2*time.Second)
	defer cancel()
	if err := r.cost.Record(recordCtx, r.llm.Provider(), inputTokens, outputTokens, costMicroUSD); err != nil {
		slog.Warn("llm cost record failed",
			"provider", r.llm.Provider(), "error", err)
	}
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

// isTransient reports whether err represents a transient LLM error that may
// succeed on a retry. Deadline / cancellation errors are retryable; circuit
// breaker open errors are explicitly NOT — the breaker exists to fail fast,
// and re-trying a tripped breaker is pointless and just wastes the parent
// invocation budget.
func isTransient(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, domain.ErrCircuitOpen) {
		return false
	}
	return errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled)
}

// wrapAgentTimeout tags err with domain.ErrAgentTimeout exactly once so that
// callers can use errors.Is(..., ErrAgentTimeout) to fail closed. If err is
// already wrapped by ErrAgentTimeout (e.g. returned from the LLM fallback
// chain) we pass it through instead of double-wrapping, which would produce
// log noise like "deadline: deadline: all LLM providers failed".
func wrapAgentTimeout(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, domain.ErrAgentTimeout) {
		return err
	}
	return fmt.Errorf("%w: %v", domain.ErrAgentTimeout, err)
}

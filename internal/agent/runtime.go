package agent

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/azhar/cerebro/internal/config"
	"github.com/azhar/cerebro/internal/domain"
	"github.com/azhar/cerebro/internal/port"
	"github.com/google/uuid"
)

// Runtime manages a single agent invocation: tool loop, timeout, cost tracking,
// and fail-closed behaviour on any error.
type Runtime struct {
	llm      port.LLM
	logStore port.AgentLogStore
	cfg      config.AgentConfig
}

// NewRuntime creates an agent Runtime.
func NewRuntime(llm port.LLM, logStore port.AgentLogStore, cfg config.AgentConfig) *Runtime {
	return &Runtime{llm: llm, logStore: logStore, cfg: cfg}
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
func (r *Runtime) Invoke(
	ctx context.Context,
	agent domain.AgentRole,
	systemPrompt string,
	userMessage string,
	tools map[string]port.ToolHandler,
	outcome string,
) InvokeResult {
	runID := uuid.New().String()
	start := time.Now()

	// Enforce per-invocation total timeout.
	totalTimeout := time.Duration(r.cfg.TimeoutTotalSeconds) * time.Second
	invokeCtx, cancel := context.WithTimeout(ctx, totalTimeout)
	defer cancel()

	// Apply per-turn timeout via the LLM context (tool loop enforces max turns internally).
	output, err := r.llm.Complete(invokeCtx, systemPrompt, userMessage, tools)

	latencyMS := int(time.Since(start).Milliseconds())

	run := domain.AgentRun{
		ID:          runID,
		Agent:       agent,
		Model:       r.llm.ModelID(),
		Provider:    r.llm.Provider(),
		LatencyMS:   latencyMS,
		Outcome:     outcome,
		CreatedAt:   start,
	}

	if err != nil {
		run.Error = err.Error()
		slog.Error("agent invocation failed",
			"agent", agent, "run_id", runID, "error", err, "latency_ms", latencyMS)
		_ = r.logStore.SaveRun(ctx, run)
		return InvokeResult{
			RunID:  runID,
			Err:    fmt.Errorf("%w: %v", domain.ErrAgentTimeout, err),
		}
	}

	slog.Info("agent invocation complete",
		"agent", agent, "run_id", runID, "latency_ms", latencyMS)
	_ = r.logStore.SaveRun(ctx, run)

	return InvokeResult{RunID: runID, Output: output, Outcome: outcome}
}

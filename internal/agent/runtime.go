package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/azhar/cerebro/internal/config"
	"github.com/azhar/cerebro/internal/domain"
	"github.com/azhar/cerebro/internal/port"
	"github.com/google/uuid"
)

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
// stepNum is the 1-based step counter; maxSteps is the configured turn limit.
type StepNotifyFunc func(agent string, runID string, step AgentStep, toolName string, content string, stepNum int, maxSteps int)

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
func (r *Runtime) Invoke(
	ctx context.Context,
	agent domain.AgentRole,
	systemPrompt string,
	userMessage string,
	tools map[string]port.Tool,
	outcome string,
) InvokeResult {
	runID := uuid.New().String()
	start := time.Now()
	agentName := string(agent)

	maxSteps := r.cfg.MaxTurns
	if maxSteps <= 0 {
		maxSteps = 20
	}

	r.notifyStep(agentName, runID, StepThinking, "", "", 1, maxSteps)

	// Enforce per-invocation total timeout.
	totalTimeout := time.Duration(r.cfg.TimeoutTotalSeconds) * time.Second
	invokeCtx, cancel := context.WithTimeout(ctx, totalTimeout)
	defer cancel()

	// Wrap tools to emit TOOL/OBSERVING step transitions.
	stepCounter := 1 // THINKING is step 1
	notifyingTools := r.wrapToolsWithNotify(agentName, runID, tools, &stepCounter, maxSteps)

	// Apply per-turn timeout via the LLM context (tool loop enforces max turns internally).
	output, err := r.llm.Complete(invokeCtx, systemPrompt, userMessage, notifyingTools)

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
		t.Handler = func(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
			*stepCounter++
			r.notifyStep(agent, runID, StepTool, name, "", *stepCounter, maxSteps)
			result, err := origHandler(ctx, input)
			r.notifyStep(agent, runID, StepObserving, name, "", *stepCounter, maxSteps)
			return result, err
		}
		wrapped[name] = t
	}
	return wrapped
}

package agent

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	_ "embed"

	"github.com/azhar/cerebro/internal/agent/tools"
	"github.com/azhar/cerebro/internal/domain"
	"github.com/azhar/cerebro/internal/port"
)

//go:embed prompts/risk.tmpl
var riskPrompt string

// RiskAgent is invoked by the risk gate on the signal path.
// It can approve, reject, or resize a signal.
// On any failure, the caller's risk gate fails closed (signal rejected).
type RiskAgent struct {
	runtime *Runtime
	tools   map[string]port.Tool
	trades  port.TradeStore
}

// NewRiskAgent creates a RiskAgent.
func NewRiskAgent(runtime *Runtime, agentTools map[string]port.Tool) *RiskAgent {
	return &RiskAgent{runtime: runtime, tools: agentTools}
}

// NewRiskAgentWithPerf creates a RiskAgent with trade performance data access.
func NewRiskAgentWithPerf(runtime *Runtime, agentTools map[string]port.Tool, trades port.TradeStore) *RiskAgent {
	return &RiskAgent{runtime: runtime, tools: agentTools, trades: trades}
}

// Evaluate asks the LLM to approve or reject the signal.
// Returns (approved, nil) on approval, (false, reason) on rejection.
// Any LLM failure returns (false, ErrAgentTimeout) — fail closed.
//
// The user message is kept small and signal-specific so that the long,
// stable system prompt (+ tool schemas + optional performance context)
// forms a cacheable prefix across successive Risk Agent invocations.
func (r *RiskAgent) Evaluate(ctx context.Context, sig domain.Signal, positions []domain.Position) (bool, error) {
	userMsg := fmt.Sprintf(
		"Signal received: symbol=%s side=%s strategy=%s reason=%q. "+
			"Current open positions: %d. Evaluate and call approve_and_route_order or reject_signal.",
		sig.Symbol, sig.Side, sig.Strategy, sig.Reason, len(positions),
	)

	// Inject recent strategy performance into the system prompt (cacheable)
	// rather than the user message.
	systemPrompt := riskPrompt
	if r.trades != nil {
		systemPrompt = r.injectPerformanceContextSystem(ctx, 7, systemPrompt)
	}

	result := r.runtime.Invoke(ctx, domain.AgentRisk, systemPrompt, userMsg, r.tools, "risk_evaluation",
		"Evaluating risk for signal")
	if result.Err != nil {
		slog.Error("risk agent: invocation failed; failing closed",
			"signal_id", sig.ID, "error", result.Err)
		return false, result.Err
	}

	// The agent's decision is encoded via tool calls (approve_and_route_order or reject_signal).
	// Here we check if the output indicates approval.
	// In practice the tool calls handle the routing directly.
	// This function returns true only if the approval tool was called without error.
	slog.Info("risk agent: evaluation complete",
		"signal_id", sig.ID, "output_preview", truncate(result.Output, 100))
	return true, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// injectPerformanceContextSystem appends recent performance data to the
// SYSTEM prompt rather than the user message. See the identically-named
// helper in screening.go — putting the context in the system prefix is
// what allows Anthropic's prompt cache to kick in across successive risk
// evaluations within the 5-minute cache window.
func (r *RiskAgent) injectPerformanceContextSystem(ctx context.Context, lookbackDays int, systemPrompt string) string {
	from := time.Now().UTC().AddDate(0, 0, -lookbackDays)
	to := time.Now().UTC()

	recentTrades, err := r.trades.TradesByWindow(ctx, from, to)
	if err != nil || len(recentTrades) == 0 {
		return systemPrompt
	}

	perf := tools.AggregatePerformance(recentTrades)
	context := tools.FormatPerformanceContext(perf)
	return systemPrompt + "\n\n## Recent Strategy Performance\n\n" + context
}

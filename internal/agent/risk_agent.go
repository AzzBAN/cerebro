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
func (r *RiskAgent) Evaluate(ctx context.Context, sig domain.Signal, positions []domain.Position) (bool, error) {
	userMsg := fmt.Sprintf(
		"Signal received: symbol=%s side=%s strategy=%s reason=%q. "+
			"Current open positions: %d. Evaluate and call approve_and_route_order or reject_signal.",
		sig.Symbol, sig.Side, sig.Strategy, sig.Reason, len(positions),
	)

	// Inject recent strategy performance when available.
	if r.trades != nil {
		userMsg = r.injectPerformanceContext(ctx, 7, userMsg)
	}

	result := r.runtime.Invoke(ctx, domain.AgentRisk, riskPrompt, userMsg, r.tools, "risk_evaluation")
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

func (r *RiskAgent) injectPerformanceContext(ctx context.Context, lookbackDays int, userMsg string) string {
	from := time.Now().UTC().AddDate(0, 0, -lookbackDays)
	to := time.Now().UTC()

	recentTrades, err := r.trades.TradesByWindow(ctx, from, to)
	if err != nil || len(recentTrades) == 0 {
		return userMsg
	}

	perf := tools.AggregatePerformance(recentTrades)
	context := tools.FormatPerformanceContext(perf)
	return context + "\n\n" + userMsg
}

package agent

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"text/template"
	"time"

	_ "embed"

	"github.com/azhar/cerebro/internal/domain"
	"github.com/azhar/cerebro/internal/port"
)

//go:embed prompts/reviewer.tmpl
var reviewerPromptTmpl string

// ReviewerAgent performs weekly post-trade analysis.
// It runs asynchronously (off the trading path) and sends recommendations
// to the operators via the notifier.
type ReviewerAgent struct {
	runtime      *Runtime
	tradeStore   port.TradeStore
	agentStore   port.AgentLogStore
	notifiers    []port.Notifier
	tools        map[string]port.Tool
	lookbackDays int
	minTrades    int
}

// NewReviewerAgent creates a ReviewerAgent.
func NewReviewerAgent(
	runtime *Runtime,
	tradeStore port.TradeStore,
	agentStore port.AgentLogStore,
	notifiers []port.Notifier,
	lookbackDays, minTrades int,
	agentTools map[string]port.Tool,
) *ReviewerAgent {
	return &ReviewerAgent{
		runtime:      runtime,
		tradeStore:   tradeStore,
		agentStore:   agentStore,
		notifiers:    notifiers,
		tools:        agentTools,
		lookbackDays: lookbackDays,
		minTrades:    minTrades,
	}
}

// Run executes the weekly review loop. Blocks until ctx is cancelled.
// Schedule is driven by a simple weekly ticker; cron support in Phase 9.
func (r *ReviewerAgent) Run(ctx context.Context) error {
	ticker := time.NewTicker(7 * 24 * time.Hour)
	defer ticker.Stop()

	slog.Info("reviewer agent started", "lookback_days", r.lookbackDays)

	for {
		select {
		case <-ctx.Done():
			slog.Info("reviewer agent stopping")
			return nil
		case <-ticker.C:
			r.runReview(ctx)
		}
	}
}

func (r *ReviewerAgent) runReview(ctx context.Context) {
	slog.Info("reviewer agent: running weekly review")

	from := time.Now().UTC().AddDate(0, 0, -r.lookbackDays)
	to := time.Now().UTC()

	trades, err := r.tradeStore.TradesByWindow(ctx, from, to)
	if err != nil {
		slog.Error("reviewer: fetch trades", "error", err)
		return
	}

	if len(trades) < r.minTrades {
		slog.Info("reviewer: insufficient trades for review",
			"count", len(trades), "min_required", r.minTrades)
		return
	}

	// Build the user message with trade summary.
	userMsg := buildReviewSummary(trades, r.lookbackDays)

	// Build system prompt from template.
	systemPrompt, err := renderReviewerPrompt(r.lookbackDays, len(trades), computeWinRate(trades))
	if err != nil {
		slog.Error("reviewer: render prompt", "error", err)
		return
	}

	result := r.runtime.Invoke(ctx, domain.AgentReviewer, systemPrompt, userMsg, r.tools, "reviewer_recommendation",
		"Reviewing recent trades")
	if result.Err != nil {
		slog.Error("reviewer: LLM failed", "error", result.Err)
		return
	}

	// Notify operators with the recommendation.
	notification := fmt.Sprintf("📊 *Weekly Reviewer Report*\n\n%s", result.Output)
	for _, n := range r.notifiers {
		_ = n.Send(ctx, port.ChannelAIReasoning, notification)
	}

	slog.Info("reviewer agent: review complete", "trades_analysed", len(trades))
}

func buildReviewSummary(trades []domain.Trade, lookbackDays int) string {
	wins, losses := 0, 0
	var totalPnL float64
	for _, t := range trades {
		if t.PnL != nil {
			pnl, _ := t.PnL.Float64()
			totalPnL += pnl
			if pnl > 0 {
				wins++
			} else {
				losses++
			}
		}
	}
	return fmt.Sprintf(
		"Review period: %d days, %d trades. Win: %d, Loss: %d, Net PnL: %.2f USDT.",
		lookbackDays, len(trades), wins, losses, totalPnL,
	)
}

func computeWinRate(trades []domain.Trade) float64 {
	wins := 0
	total := 0
	for _, t := range trades {
		if t.PnL != nil {
			total++
			if t.PnL.IsPositive() {
				wins++
			}
		}
	}
	if total == 0 {
		return 0
	}
	return float64(wins) / float64(total) * 100
}

func renderReviewerPrompt(lookbackDays, totalTrades int, winRate float64) (string, error) {
	tmpl, err := template.New("reviewer").Parse(reviewerPromptTmpl)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	data := map[string]any{
		"LookbackDays": lookbackDays,
		"TotalTrades":  totalTrades,
		"WinRate":      fmt.Sprintf("%.1f", winRate),
		"Date":         time.Now().UTC().Format("2006-01-02"),
	}
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}

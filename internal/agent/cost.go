package agent

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/azhar/cerebro/internal/domain"
	"github.com/azhar/cerebro/internal/port"
)

// CostTracker maintains per-provider, per-day token and USD cost counters in Redis.
// Circuit breaker trips when daily_token_budget or daily_cost_budget_usd is exceeded.
type CostTracker struct {
	cache         port.Cache
	notifier      port.Notifier
	tokenBudget   int
	costBudgetUSD float64
	alertAtPct    float64
}

// NewCostTracker creates a CostTracker.
func NewCostTracker(cache port.Cache, notifier port.Notifier, tokenBudget int, costBudgetUSD, alertAtPct float64) *CostTracker {
	return &CostTracker{
		cache:         cache,
		notifier:      notifier,
		tokenBudget:   tokenBudget,
		costBudgetUSD: costBudgetUSD,
		alertAtPct:    alertAtPct,
	}
}

// Record adds token usage and cost to the daily counters.
// Returns ErrBudgetExceeded if any limit is hit, triggering provider fallback.
func (c *CostTracker) Record(ctx context.Context, provider string, inputTokens, outputTokens int, costUSDCents int) error {
	date := time.Now().UTC().Format("2006-01-02")
	ttl := 48 * time.Hour

	tokenKey := fmt.Sprintf("llm_tokens:%s:%s", provider, date)
	costKey := fmt.Sprintf("llm_cost:%s:%s", provider, date)

	totalTokens := int64(inputTokens + outputTokens)
	totalTokensNow, _ := c.cache.IncrBy(ctx, tokenKey, totalTokens, ttl)
	totalCostNow, _ := c.cache.IncrBy(ctx, costKey, int64(costUSDCents), ttl)

	// Token budget check.
	if c.tokenBudget > 0 && int(totalTokensNow) >= c.tokenBudget {
		return fmt.Errorf("%w: daily token budget %d exceeded for %s", domain.ErrBudgetExceeded, c.tokenBudget, provider)
	}

	// Cost budget check.
	totalCostUSD := float64(totalCostNow) / 100.0
	if c.costBudgetUSD > 0 && totalCostUSD >= c.costBudgetUSD {
		return fmt.Errorf("%w: daily cost budget $%.2f exceeded for %s", domain.ErrBudgetExceeded, c.costBudgetUSD, provider)
	}

	// Alert at threshold.
	if c.tokenBudget > 0 {
		pct := float64(totalTokensNow) / float64(c.tokenBudget) * 100
		if pct >= c.alertAtPct*100 && c.notifier != nil {
			go func() {
				msg := fmt.Sprintf("[BUDGET] %s: %.0f%% of daily token budget used (%d/%d)",
					provider, pct, totalTokensNow, c.tokenBudget)
				_ = c.notifier.Send(ctx, port.ChannelSystemAlerts, msg)
			}()
		}
	}

	log := slog.With("provider", provider, "tokens", totalTokensNow, "cost_usd", totalCostUSD)
	log.Debug("LLM usage recorded")
	return nil
}

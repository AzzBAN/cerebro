package agent

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
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

	// alertedMu guards alerted, an in-process set of "provider:date" keys
	// for which we have already fired the threshold alert today. This is
	// best-effort dedup — a process restart may re-alert, which is fine.
	alertedMu sync.Mutex
	alerted   map[string]struct{}
}

// NewCostTracker creates a CostTracker.
func NewCostTracker(cache port.Cache, notifier port.Notifier, tokenBudget int, costBudgetUSD, alertAtPct float64) *CostTracker {
	return &CostTracker{
		cache:         cache,
		notifier:      notifier,
		tokenBudget:   tokenBudget,
		costBudgetUSD: costBudgetUSD,
		alertAtPct:    alertAtPct,
		alerted:       make(map[string]struct{}),
	}
}

// Redis key prefixes for daily per-provider counters. Cost is stored in
// **micro-USD** (10⁻⁶ USD) — see pricing.EstimateCostMicroUSD for why the
// whole-cent resolution used historically was insufficient. The `_u`
// suffix distinguishes the new unit from the legacy `llm_cost:` keys so
// stale data doesn't skew readings during a rollover.
const (
	tokenKeyPrefix = "llm_tokens"
	costKeyPrefix  = "llm_cost_u"
)

// microUSDPerUSD is the scaling factor between μUSD and USD.
const microUSDPerUSD = 1_000_000

// Record adds token usage and cost to the daily counters. costMicroUSD is
// the estimated spend for this single call in micro-dollars (10⁻⁶ USD).
// Returns ErrBudgetExceeded if any limit is hit, triggering provider fallback.
func (c *CostTracker) Record(ctx context.Context, provider string, inputTokens, outputTokens int, costMicroUSD int64) error {
	date := time.Now().UTC().Format("2006-01-02")
	ttl := 48 * time.Hour

	tokenKey := fmt.Sprintf("%s:%s:%s", tokenKeyPrefix, provider, date)
	costKey := fmt.Sprintf("%s:%s:%s", costKeyPrefix, provider, date)

	totalTokens := int64(inputTokens + outputTokens)
	totalTokensNow, _ := c.cache.IncrBy(ctx, tokenKey, totalTokens, ttl)
	totalCostNow, _ := c.cache.IncrBy(ctx, costKey, costMicroUSD, ttl)

	// Token budget check.
	if c.tokenBudget > 0 && int(totalTokensNow) >= c.tokenBudget {
		return fmt.Errorf("%w: daily token budget %d exceeded for %s", domain.ErrBudgetExceeded, c.tokenBudget, provider)
	}

	// Cost budget check.
	totalCostUSD := float64(totalCostNow) / microUSDPerUSD
	if c.costBudgetUSD > 0 && totalCostUSD >= c.costBudgetUSD {
		return fmt.Errorf("%w: daily cost budget $%.2f exceeded for %s", domain.ErrBudgetExceeded, c.costBudgetUSD, provider)
	}

	// Alert at threshold. Fire once per (provider, day) per process — a
	// restart may re-alert, which is acceptable.
	if c.tokenBudget > 0 && c.notifier != nil {
		pct := float64(totalTokensNow) / float64(c.tokenBudget) * 100
		if pct >= c.alertAtPct*100 && c.shouldAlert(provider, date) {
			msg := fmt.Sprintf("[BUDGET] %s: %.0f%% of daily token budget used (%d/%d)",
				provider, pct, totalTokensNow, c.tokenBudget)
			go func() {
				notifyCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				_ = c.notifier.Send(notifyCtx, port.ChannelSystemAlerts, msg)
			}()
		}
	}

	log := slog.With("provider", provider, "tokens", totalTokensNow, "cost_usd", totalCostUSD)
	log.Debug("LLM usage recorded")
	return nil
}

// ProviderUsage is the per-provider breakdown of today's LLM spend.
type ProviderUsage struct {
	Tokens  int64
	CostUSD float64
}

// BudgetSnapshot is a point-in-time view of the current day's LLM usage,
// aggregated across all providers and broken down per-provider. Budgets
// of 0 mean "unlimited" in config and the TUI renders them as hidden.
type BudgetSnapshot struct {
	Date          string                   // YYYY-MM-DD (UTC)
	TokensUsed    int64                    // sum across providers
	CostUSD       float64                  // sum across providers
	TokenBudget   int                      // configured daily_token_budget (0 = disabled)
	CostBudgetUSD float64                  // configured daily_cost_budget_usd (0 = disabled)
	PerProvider   map[string]ProviderUsage // provider → usage
	At            time.Time                // when this snapshot was built
}

// Snapshot returns today's aggregate and per-provider token / cost usage
// by scanning the Redis keys written by Record. Errors from the cache are
// swallowed — the snapshot is best-effort observability and must never
// take a path that could block the engine.
func (c *CostTracker) Snapshot(ctx context.Context) BudgetSnapshot {
	date := time.Now().UTC().Format("2006-01-02")
	snap := BudgetSnapshot{
		Date:          date,
		TokenBudget:   c.tokenBudget,
		CostBudgetUSD: c.costBudgetUSD,
		PerProvider:   make(map[string]ProviderUsage),
		At:            time.Now(),
	}
	if c.cache == nil {
		return snap
	}

	dateSuffix := ":" + date
	tokenKeys, _ := c.cache.Keys(ctx, tokenKeyPrefix+":*"+dateSuffix)
	for _, k := range tokenKeys {
		provider := strings.TrimSuffix(strings.TrimPrefix(k, tokenKeyPrefix+":"), dateSuffix)
		if provider == "" {
			continue
		}
		raw, err := c.cache.Get(ctx, k)
		if err != nil || len(raw) == 0 {
			continue
		}
		n, err := strconv.ParseInt(string(raw), 10, 64)
		if err != nil {
			continue
		}
		pu := snap.PerProvider[provider]
		pu.Tokens = n
		snap.PerProvider[provider] = pu
		snap.TokensUsed += n
	}

	costKeys, _ := c.cache.Keys(ctx, costKeyPrefix+":*"+dateSuffix)
	for _, k := range costKeys {
		provider := strings.TrimSuffix(strings.TrimPrefix(k, costKeyPrefix+":"), dateSuffix)
		if provider == "" {
			continue
		}
		raw, err := c.cache.Get(ctx, k)
		if err != nil || len(raw) == 0 {
			continue
		}
		micro, err := strconv.ParseInt(string(raw), 10, 64)
		if err != nil {
			continue
		}
		usd := float64(micro) / microUSDPerUSD
		pu := snap.PerProvider[provider]
		pu.CostUSD = usd
		snap.PerProvider[provider] = pu
		snap.CostUSD += usd
	}

	return snap
}

// ResetToday clears today's per-day Redis counters. `tokens` / `cost`
// select which counters to wipe; pass both for a full reset. When
// provider is empty all providers are matched via glob. Also clears the
// in-process alert dedup so a fresh threshold crossing will re-notify.
// Returns the number of Redis keys deleted.
func (c *CostTracker) ResetToday(ctx context.Context, provider string, tokens, cost bool) (int, error) {
	return c.Reset(ctx, provider, time.Now().UTC().Format("2006-01-02"), tokens, cost)
}

// Reset clears per-day Redis counters for the given UTC date. See
// ResetToday for parameter semantics. Exposed separately so operators
// can target past days that haven't yet aged out of the 48h TTL.
func (c *CostTracker) Reset(ctx context.Context, provider, date string, tokens, cost bool) (int, error) {
	if c.cache == nil {
		return 0, nil
	}
	if !tokens && !cost {
		return 0, fmt.Errorf("reset: must select at least one of tokens/cost")
	}
	providerGlob := provider
	if providerGlob == "" {
		providerGlob = "*"
	}

	var prefixes []string
	if tokens {
		prefixes = append(prefixes, tokenKeyPrefix)
	}
	if cost {
		prefixes = append(prefixes, costKeyPrefix)
	}

	var deleted int
	for _, prefix := range prefixes {
		pattern := fmt.Sprintf("%s:%s:%s", prefix, providerGlob, date)
		keys, err := c.cache.Keys(ctx, pattern)
		if err != nil {
			return deleted, fmt.Errorf("reset: list %q: %w", pattern, err)
		}
		for _, k := range keys {
			if err := c.cache.Delete(ctx, k); err != nil {
				return deleted, fmt.Errorf("reset: delete %q: %w", k, err)
			}
			deleted++
		}
	}

	// Clear the in-process alert dedup for this date so a fresh threshold
	// crossing re-alerts. Match on the same "provider:date" key shape
	// shouldAlert builds.
	c.alertedMu.Lock()
	suffix := ":" + date
	for k := range c.alerted {
		if !strings.HasSuffix(k, suffix) {
			continue
		}
		if provider != "" && !strings.HasPrefix(k, provider+":") {
			continue
		}
		delete(c.alerted, k)
	}
	c.alertedMu.Unlock()

	return deleted, nil
}

// shouldAlert returns true exactly once per (provider, date) in this process.
func (c *CostTracker) shouldAlert(provider, date string) bool {
	key := provider + ":" + date
	c.alertedMu.Lock()
	defer c.alertedMu.Unlock()
	if _, seen := c.alerted[key]; seen {
		return false
	}
	c.alerted[key] = struct{}{}
	return true
}

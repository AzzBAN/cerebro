package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/azhar/cerebro/internal/domain"
	"github.com/azhar/cerebro/internal/port"
	"github.com/azhar/cerebro/internal/screening"
)

// DiscoveryPlansCacheKey is the Redis key under which the current
// cycle's TradePlan slice is cached. The TUI and Telegram dispatcher
// read from this key. Exported so consumers can avoid duplicating the
// literal.
const DiscoveryPlansCacheKey = "screening:plans"

// PlannerOptions parameterises a DiscoveryPlanner. Zero values are
// safe and fall back to screening.DefaultThresholds /
// screening.DefaultPlanParams; callers should override them when the
// operator has tuned the matcher in app.yaml.
type PlannerOptions struct {
	Thresholds      screening.Thresholds
	DefaultPlan     screening.PlanParams
	EnabledStrategy map[domain.StrategyName]bool
	PerStrategyPlan map[domain.StrategyName]screening.PlanParams
	TopFitsPerCoin  int     // typically 1
	MinConfidence   float64 // drop fits below this
	MaxPlans        int     // hard cap on rendered plans (e.g. 10)
	NotifyChannel   port.NotifyChannel
}

// DiscoveryPlanner turns DiscoveryCandidates + a Coinglass scan into
// TradePlans, caches them, and pushes a human-readable report to the
// configured ChatOps notifiers.
//
// It is the orchestrator that bridges the deterministic
// internal/screening pipeline with the agent layer's I/O dependencies
// (cache, scanner adapter, notifiers, bias cache for the screener's
// directional read). All scoring / matching / planning logic lives in
// the screening package — Planner only does I/O and assembly.
type DiscoveryPlanner struct {
	scanner   port.MarketScanFeed // optional; nil → no derivatives enrichment
	cache     port.Cache
	notifiers []port.Notifier
	opts      PlannerOptions
}

// NewDiscoveryPlanner constructs a planner. `scanner` may be nil — when
// CoinGlass has no API key the planner still produces price-only plans
// using the Binance ticker alone.
func NewDiscoveryPlanner(
	scanner port.MarketScanFeed,
	cache port.Cache,
	notifiers []port.Notifier,
	opts PlannerOptions,
) *DiscoveryPlanner {
	if opts.Thresholds == (screening.Thresholds{}) {
		opts.Thresholds = screening.DefaultThresholds()
	}
	if opts.DefaultPlan == (screening.PlanParams{}) {
		opts.DefaultPlan = screening.DefaultPlanParams()
	}
	if opts.TopFitsPerCoin <= 0 {
		opts.TopFitsPerCoin = 1
	}
	if opts.NotifyChannel == "" {
		opts.NotifyChannel = port.ChannelAIReasoning
	}
	return &DiscoveryPlanner{
		scanner:   scanner,
		cache:     cache,
		notifiers: notifiers,
		opts:      opts,
	}
}

// Run takes a fresh candidate list, builds a TradePlan for each that
// matches an enabled strategy, caches the result, and pushes a
// Telegram-friendly report. Returns the produced plans for callers
// that want to render them elsewhere.
//
// Best-effort: scan failures, cache failures and notifier failures are
// logged but never propagate up (the screening cycle must always
// continue regardless of derivatives availability).
func (p *DiscoveryPlanner) Run(ctx context.Context, cands []DiscoveryCandidate, ttl time.Duration) []domain.TradePlan {
	if len(cands) == 0 {
		return nil
	}

	scanByBase := p.fetchScans(ctx)

	now := time.Now().UTC()
	plans := make([]domain.TradePlan, 0, len(cands))
	for _, c := range cands {
		base := strings.ToUpper(strings.TrimSpace(baseAsset(c.Symbol)))

		f := screening.EnrichFeatures(
			c.Symbol,
			c.Venue,
			base,
			c.LastPrice,
			c.PriceChangePct24,
			c.QuoteVolume24h,
			c.IsNewListing,
			scanByBase,
		)

		regime, side := screening.Classify(f, p.opts.Thresholds)
		if regime == domain.RegimeUnknown {
			continue
		}

		fits := screening.MatchStrategies(f, regime, side, p.opts.EnabledStrategy, p.opts.TopFitsPerCoin)
		if len(fits) == 0 {
			continue
		}
		fit := fits[0]
		if fit.Confidence < p.opts.MinConfidence {
			continue
		}

		params := p.opts.DefaultPlan
		if v, ok := p.opts.PerStrategyPlan[fit.Strategy]; ok {
			params = v
		}

		bias := p.loadBias(ctx, c.Symbol)

		plan, ok := screening.BuildTradePlan(f, regime, side, fit, bias, params, now)
		if !ok {
			continue
		}
		plan.ExpiresAt = now.Add(ttl)
		plans = append(plans, plan)
	}

	sort.SliceStable(plans, func(i, j int) bool {
		return plans[i].Confidence > plans[j].Confidence
	})

	if p.opts.MaxPlans > 0 && len(plans) > p.opts.MaxPlans {
		plans = plans[:p.opts.MaxPlans]
	}

	p.cachePlans(ctx, plans, ttl)
	p.notify(ctx, plans, now)
	return plans
}

// fetchScans calls the four list endpoints in parallel and merges them.
// Each endpoint is best-effort; failures are logged and dropped so a
// single rate-limit on (e.g.) the L/S endpoint doesn't poison the
// whole pipeline.
func (p *DiscoveryPlanner) fetchScans(ctx context.Context) map[string]port.MarketScanRow {
	if p.scanner == nil {
		return map[string]port.MarketScanRow{}
	}
	type result struct {
		name string
		rows []port.MarketScanRow
		err  error
	}
	resultsCh := make(chan result, 4)
	go func() {
		r, err := p.scanner.FundingExtremes(ctx, 0)
		resultsCh <- result{"funding", r, err}
	}()
	go func() {
		r, err := p.scanner.OpenInterestMovers(ctx, 0)
		resultsCh <- result{"oi_movers", r, err}
	}()
	go func() {
		r, err := p.scanner.LiquidationLeaders(ctx, 0)
		resultsCh <- result{"liq_leaders", r, err}
	}()
	go func() {
		r, err := p.scanner.LongShortExtremes(ctx, 0)
		resultsCh <- result{"long_short", r, err}
	}()

	slices := make([][]port.MarketScanRow, 0, 4)
	for i := 0; i < 4; i++ {
		r := <-resultsCh
		if r.err != nil {
			slog.Warn("planner: market scan failed; continuing without it",
				"endpoint", r.name, "error", r.err)
			continue
		}
		slices = append(slices, r.rows)
	}
	return screening.MergeScanRows(slices...)
}

func (p *DiscoveryPlanner) loadBias(ctx context.Context, sym domain.Symbol) domain.BiasScore {
	if p.cache == nil {
		return domain.BiasNeutral
	}
	raw, err := p.cache.Get(ctx, fmt.Sprintf("bias:%s", sym))
	if err != nil || raw == nil {
		return domain.BiasNeutral
	}
	var bias domain.BiasResult
	if err := json.Unmarshal(raw, &bias); err != nil {
		return domain.BiasNeutral
	}
	return bias.Score
}

func (p *DiscoveryPlanner) cachePlans(ctx context.Context, plans []domain.TradePlan, ttl time.Duration) {
	if p.cache == nil || ttl <= 0 {
		return
	}
	b, err := json.Marshal(plans)
	if err != nil {
		slog.Warn("planner: marshal plans failed", "error", err)
		return
	}
	if err := p.cache.Set(ctx, DiscoveryPlansCacheKey, b, ttl); err != nil {
		slog.Warn("planner: cache plans failed", "error", err)
	}
}

func (p *DiscoveryPlanner) notify(ctx context.Context, plans []domain.TradePlan, now time.Time) {
	if len(plans) == 0 || len(p.notifiers) == 0 {
		return
	}
	msg := RenderTradePlansMessage(plans, now)
	for _, n := range p.notifiers {
		if err := n.Send(ctx, p.opts.NotifyChannel, msg); err != nil {
			slog.Warn("planner: notify failed",
				"channel", p.opts.NotifyChannel, "error", err)
		}
	}
}

// LoadCachedTradePlans returns the cached TradePlan slice from Redis.
// Returns (nil, nil) on miss or nil cache.
func LoadCachedTradePlans(ctx context.Context, cache port.Cache) ([]domain.TradePlan, error) {
	if cache == nil {
		return nil, nil
	}
	raw, err := cache.Get(ctx, DiscoveryPlansCacheKey)
	if err != nil {
		return nil, fmt.Errorf("planner: cache get: %w", err)
	}
	if raw == nil {
		return nil, nil
	}
	var out []domain.TradePlan
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("planner: unmarshal: %w", err)
	}
	return out, nil
}

// RenderTradePlansMessage formats the plans as a compact, monospace-
// friendly message suitable for Telegram or the system_alerts channel.
//
// Pure function — no I/O — so it can be unit-tested cheaply.
func RenderTradePlansMessage(plans []domain.TradePlan, now time.Time) string {
	var b strings.Builder

	fmt.Fprintf(&b, "🎯 Top Trade Plans — %s UTC\n", now.Format("2006-01-02 15:04"))
	b.WriteString(strings.Repeat("━", 38))
	b.WriteByte('\n')

	for _, plan := range plans {
		side := strings.ToUpper(string(plan.Side))
		fmt.Fprintf(&b, "%s %s · %s · %s\n",
			side, plan.Symbol, plan.Strategy, plan.Bias)
		fmt.Fprintf(&b, "  regime  : %s (conf %.2f)\n",
			plan.Regime, plan.Confidence)
		fmt.Fprintf(&b, "  entry   : %s … %s (last %s)\n",
			fmtPrice(plan.EntryLow), fmtPrice(plan.EntryHigh), fmtPrice(plan.LastPrice))
		fmt.Fprintf(&b, "  SL      : %s\n", fmtPrice(plan.StopLoss))
		fmt.Fprintf(&b, "  TP1     : %s (R:R %.1f)\n",
			fmtPrice(plan.TakeProfit1), plan.RRRatio)
		if !plan.TakeProfit2.IsZero() {
			fmt.Fprintf(&b, "  TP2     : %s\n", fmtPrice(plan.TakeProfit2))
		}
		if len(plan.Reasoning) > 0 {
			fmt.Fprintf(&b, "  why     : %s\n", strings.Join(plan.Reasoning, " · "))
		}
		b.WriteByte('\n')
	}

	b.WriteString("ℹ︎ Advisory only — risk gate still requires markets.yaml allow-list.")
	return b.String()
}

func fmtPrice(d interface{ String() string }) string {
	// shopspring/decimal already prints sensible representations; we
	// trim trailing zeros where possible by relying on the Decimal's
	// own formatting. Wrapping behind an interface keeps the renderer
	// trivial to test with stub values.
	return d.String()
}

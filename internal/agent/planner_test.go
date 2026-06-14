package agent

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/azhar/cerebro/internal/domain"
	"github.com/azhar/cerebro/internal/port"
	"github.com/azhar/cerebro/internal/screening"
	"github.com/shopspring/decimal"
)

// memCache is a minimal in-memory port.Cache implementation for tests.
type memCache struct {
	mu sync.Mutex
	kv map[string][]byte
}

func newMemCache() *memCache { return &memCache{kv: map[string][]byte{}} }

func (m *memCache) Set(_ context.Context, k string, v []byte, _ time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.kv[k] = append([]byte(nil), v...)
	return nil
}
func (m *memCache) Get(_ context.Context, k string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.kv[k]
	if !ok {
		return nil, nil
	}
	return append([]byte(nil), v...), nil
}
func (m *memCache) Delete(_ context.Context, k string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.kv, k)
	return nil
}
func (m *memCache) IncrBy(_ context.Context, _ string, _ int64, _ time.Duration) (int64, error) {
	return 0, nil
}
func (m *memCache) Keys(_ context.Context, _ string) ([]string, error) { return nil, nil }
func (m *memCache) Exists(_ context.Context, k string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.kv[k]
	return ok, nil
}

// stubScanner returns canned rows from each endpoint.
type stubScanner struct {
	funding []port.MarketScanRow
	oi      []port.MarketScanRow
	liq     []port.MarketScanRow
	ls      []port.MarketScanRow
}

func (s *stubScanner) FundingExtremes(_ context.Context, _ int) ([]port.MarketScanRow, error) {
	return s.funding, nil
}
func (s *stubScanner) OpenInterestMovers(_ context.Context, _ int) ([]port.MarketScanRow, error) {
	return s.oi, nil
}
func (s *stubScanner) LiquidationLeaders(_ context.Context, _ int) ([]port.MarketScanRow, error) {
	return s.liq, nil
}
func (s *stubScanner) LongShortExtremes(_ context.Context, _ int) ([]port.MarketScanRow, error) {
	return s.ls, nil
}

// recordingNotifier captures every Send call.
type recordingNotifier struct {
	mu    sync.Mutex
	last  string
	count int
}

func (n *recordingNotifier) Send(_ context.Context, _ port.NotifyChannel, msg string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.last = msg
	n.count++
	return nil
}
func (n *recordingNotifier) SendEmbed(_ context.Context, _ port.NotifyChannel, _ string, body string, _ map[string]string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.last = body
	n.count++
	return nil
}

func TestDiscoveryPlanner_ProducesPlans_CachesAndNotifies(t *testing.T) {
	cache := newMemCache()
	notif := &recordingNotifier{}
	// Only the 24h OI signal — that pushes the candidate into the
	// "trending" regime (vs the "breakout" regime which fires when
	// 4h OI is also confirming).
	scanner := &stubScanner{
		oi: []port.MarketScanRow{
			{BaseAsset: "DOGE", OIChange24hPct: 8.0},
		},
	}

	planner := NewDiscoveryPlanner(scanner, cache, []port.Notifier{notif}, PlannerOptions{
		EnabledStrategy: screening.EnabledFromList([]domain.StrategyName{
			"trend_following", "mean_reversion", "volatility_breakout",
		}),
		MinConfidence: 0,
		MaxPlans:      5,
	})

	cands := []DiscoveryCandidate{{
		Symbol:           "DOGE/USDT-PERP",
		Venue:            domain.VenueBinanceFutures,
		ContractType:     domain.ContractFuturesPerp,
		QuoteAsset:       "USDT",
		LastPrice:        decimal.NewFromFloat(0.10),
		PriceChangePct24: 7.5,
		QuoteVolume24h:   decimal.NewFromInt(200_000_000),
	}}

	plans := planner.Run(context.Background(), cands, 5*time.Minute)
	if len(plans) != 1 {
		t.Fatalf("want 1 plan, got %d", len(plans))
	}
	plan := plans[0]
	if plan.Strategy != "trend_following" {
		t.Errorf("strategy: want trend_following, got %s", plan.Strategy)
	}
	if plan.Side != domain.SideBuy {
		t.Errorf("side: want buy, got %s", plan.Side)
	}
	if plan.StopLoss.Cmp(plan.EntryLow) != -1 {
		t.Errorf("buy SL must be below entry low")
	}

	// Cache check
	raw, err := cache.Get(context.Background(), DiscoveryPlansCacheKey)
	if err != nil || raw == nil {
		t.Fatalf("expected plans cached at %s, raw=%v err=%v", DiscoveryPlansCacheKey, raw, err)
	}
	var roundtrip []domain.TradePlan
	if err := json.Unmarshal(raw, &roundtrip); err != nil {
		t.Fatalf("cached plans not valid JSON: %v", err)
	}
	if len(roundtrip) != 1 {
		t.Errorf("cached plan count: want 1, got %d", len(roundtrip))
	}

	// Notifier check
	if notif.count == 0 {
		t.Error("expected notifier to be called at least once")
	}
	if !strings.Contains(notif.last, "DOGE") {
		t.Errorf("notification missing symbol: %q", notif.last)
	}
	if !strings.Contains(notif.last, "trend_following") {
		t.Errorf("notification missing strategy: %q", notif.last)
	}
}

func TestDiscoveryPlanner_NilScanner_StillProducesPricePlans(t *testing.T) {
	planner := NewDiscoveryPlanner(nil, newMemCache(), nil, PlannerOptions{
		EnabledStrategy: screening.EnabledFromList([]domain.StrategyName{
			"mean_reversion",
		}),
	})
	cands := []DiscoveryCandidate{{
		Symbol:           "RANGE/USDT-PERP",
		Venue:            domain.VenueBinanceFutures,
		LastPrice:        decimal.NewFromInt(10),
		PriceChangePct24: 0.6, // quiet → range
		QuoteVolume24h:   decimal.NewFromInt(100_000_000),
	}}
	plans := planner.Run(context.Background(), cands, time.Minute)
	if len(plans) != 1 {
		t.Fatalf("want 1 plan with nil scanner, got %d", len(plans))
	}
	if plans[0].Strategy != "mean_reversion" {
		t.Errorf("strategy: want mean_reversion, got %s", plans[0].Strategy)
	}
}

func TestDiscoveryPlanner_DropsBelowMinConfidence(t *testing.T) {
	planner := NewDiscoveryPlanner(nil, newMemCache(), nil, PlannerOptions{
		EnabledStrategy: screening.EnabledFromList([]domain.StrategyName{"mean_reversion"}),
		MinConfidence:   0.95, // unattainable
	})
	cands := []DiscoveryCandidate{{
		Symbol:           "RANGE/USDT-PERP",
		LastPrice:        decimal.NewFromInt(10),
		PriceChangePct24: 0.6,
	}}
	plans := planner.Run(context.Background(), cands, time.Minute)
	if len(plans) != 0 {
		t.Errorf("want 0 plans (confidence floor), got %d", len(plans))
	}
}

func TestRenderTradePlansMessage_IncludesAllSections(t *testing.T) {
	plan := domain.TradePlan{
		Symbol:      "BTCUSDT",
		Strategy:    "trend_following",
		Bias:        domain.BiasBullish,
		Side:        domain.SideBuy,
		Regime:      domain.RegimeTrending,
		LastPrice:   decimal.NewFromInt(60_000),
		EntryLow:    decimal.NewFromInt(59_900),
		EntryHigh:   decimal.NewFromInt(60_000),
		StopLoss:    decimal.NewFromInt(59_400),
		TakeProfit1: decimal.NewFromInt(61_000),
		TakeProfit2: decimal.NewFromInt(62_000),
		RRRatio:     2.0,
		Confidence:  0.78,
		Reasoning:   []string{"Δ24h ≥ 6%", "OI rising"},
	}
	msg := RenderTradePlansMessage([]domain.TradePlan{plan}, time.Now())
	for _, want := range []string{"BTCUSDT", "trend_following", "Bullish", "59900", "61000", "TP1", "regime"} {
		if !strings.Contains(msg, want) {
			t.Errorf("rendered message missing %q\n---\n%s", want, msg)
		}
	}
}

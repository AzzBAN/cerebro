package backtest

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	agentpkg "github.com/azhar/cerebro/internal/agent"
	agenttools "github.com/azhar/cerebro/internal/agent/tools"
	"github.com/azhar/cerebro/internal/config"
	"github.com/azhar/cerebro/internal/domain"
	"github.com/azhar/cerebro/internal/execution/paper"
	"github.com/azhar/cerebro/internal/marketdata"
	"github.com/azhar/cerebro/internal/port"
	"github.com/azhar/cerebro/internal/risk"
	"github.com/azhar/cerebro/internal/strategy"
	"github.com/shopspring/decimal"
)

type testAuditStore struct {
	events []domain.AuditEvent
}

func (s *testAuditStore) SaveEvent(_ context.Context, e domain.AuditEvent) error {
	s.events = append(s.events, e)
	return nil
}

type testCache struct {
	vals map[string][]byte
}

func newTestCache() *testCache {
	return &testCache{vals: make(map[string][]byte)}
}

func (c *testCache) Get(_ context.Context, key string) ([]byte, error) {
	v, ok := c.vals[key]
	if !ok {
		return nil, nil
	}
	return v, nil
}

func (c *testCache) Set(_ context.Context, key string, val []byte, _ time.Duration) error {
	c.vals[key] = val
	return nil
}

func (c *testCache) Delete(_ context.Context, key string) error {
	delete(c.vals, key)
	return nil
}

func (c *testCache) IncrBy(_ context.Context, _ string, _ int64, _ time.Duration) (int64, error) {
	return 0, nil
}

func (c *testCache) Keys(_ context.Context, _ string) ([]string, error) {
	return nil, nil
}

func (c *testCache) Exists(_ context.Context, key string) (bool, error) {
	_, ok := c.vals[key]
	return ok, nil
}

func (c *testCache) SetNX(_ context.Context, key string, val []byte, _ time.Duration) (bool, error) {
	if _, ok := c.vals[key]; ok {
		return false, nil
	}
	c.vals[key] = val
	return true, nil
}

// ── 1. Cerebro Pipeline End-to-End Test with CSV Replay ──────────────────────

func TestBacktest_CerebroPipeline_EndToEnd(t *testing.T) {
	csvPath := filepath.Join("..", "..", "testdata", "fixtures", "btc_1m.csv")
	candles, err := LoadCSV(csvPath, domain.Symbol("BTC/USDT"), domain.TF1m)
	if err != nil {
		t.Fatalf("LoadCSV failed: %v", err)
	}
	if len(candles) < 100 {
		t.Fatalf("expected at least 100 candles, got %d", len(candles))
	}

	stratCfg := config.StrategyConfig{
		Name:             "trend_following",
		Enabled:          true,
		Markets:          []string{"BTC/USDT"},
		PrimaryTimeframe: domain.TF1m,
		WarmupCandles:    10,
		Indicators: config.IndicatorConfig{
			EMA: config.EMAConfig{
				Fast:      5,
				Slow:      10,
				LongTrend: 20,
			},
			ATR: config.ATRConfig{
				Period: 14,
			},
		},
	}
	strat := strategy.NewTrendFollowing(stratCfg)

	store := NewMemTradeStore()
	book := paper.NewBook()
	matcher := paper.NewMatcher(book, store, 0.0004)
	clock := NewSimClock(candles[0].OpenTime)
	hub := marketdata.NewHub()

	// Risk gate with $10,000 equity and max drawdown 10%
	riskCfg := config.RiskConfig{
		MaxDrawdownPct: 10.0,
		MaxDailyLossPct: 5.0,
		MaxOpenPositions: 3,
	}
	gate := risk.NewGate(riskCfg, newTestCache(), risk.NewCalendarBlackout())
	gate.SetStartingEquity(decimal.NewFromInt(10_000))
	matcher.SetPnLReporter(gate)

	dedup := strategy.NewDedupWindow(5 * time.Minute)

	sim := NewSimulator(candles, hub, []port.Strategy{strat}, matcher, clock)
	sim.SetTradeStore(store)
	sim.SetRiskGate(gate)
	sim.SetDedup(dedup)
	sim.SetDefaultQuantity(decimal.NewFromFloat(0.01))

	// Attach bracket resolver for stop loss and take profit
	sim.SetBracketResolver(func(c domain.Candle, sig domain.Signal) (decimal.Decimal, decimal.Decimal) {
		if sig.Side == domain.SideBuy {
			sl := c.Close.Mul(decimal.NewFromFloat(0.99)) // 1% SL
			tp := c.Close.Mul(decimal.NewFromFloat(1.02)) // 2% TP
			return sl, tp
		}
		sl := c.Close.Mul(decimal.NewFromFloat(1.01))
		tp := c.Close.Mul(decimal.NewFromFloat(0.98))
		return sl, tp
	})

	ctx := context.Background()
	trades, err := sim.Run(ctx)
	if err != nil {
		t.Fatalf("sim.Run failed: %v", err)
	}

	report := ComputeReport("trend_following", "BTC/USDT", candles[0].OpenTime, candles[len(candles)-1].CloseTime, trades)
	if report.TotalTrades == 0 && len(store.AllTrades()) == 0 {
		t.Log("No trades executed; checking if any signals fired during simulation")
	}

	// Verify report serialization
	tmpFile := filepath.Join(t.TempDir(), "report.json")
	if err := report.Save(tmpFile); err != nil {
		t.Fatalf("report.Save failed: %v", err)
	}
	data, err := os.ReadFile(tmpFile)
	if err != nil || len(data) == 0 {
		t.Fatalf("saved report file invalid: %v", err)
	}
}

// ── 2. Agent Decision: Approve & Route ────────────────────────────────────────

func TestBacktest_AgentDecision_ApproveAndRoute(t *testing.T) {
	ctx := context.Background()
	store := NewMemTradeStore()
	book := paper.NewBook()
	matcher := paper.NewMatcher(book, store, 0.0004)

	var routedOrder bool
	audit := &testAuditStore{}
	routeFn := func(_ context.Context, req agenttools.AgentOrderRequest) error {
		routedOrder = true
		intent := domain.OrderIntent{
			ID:          "agent-intent-1",
			Symbol:      req.Symbol,
			Side:        req.Side,
			Quantity:    decimal.NewFromFloat(req.Size),
			Strategy:    "trend_following",
			Environment: domain.EnvironmentPaper,
			CreatedAt:   time.Now().UTC(),
		}
		_, err := matcher.PlaceOrder(ctx, intent)
		return err
	}

	agentTools := map[string]port.Tool{
		"reject_signal":           agenttools.RejectSignal(audit),
		"approve_and_route_order": agenttools.ApproveAndRouteOrder(routeFn),
	}

	approveArgs, _ := json.Marshal(map[string]any{
		"symbol": "BTC/USDT",
		"side":   "BUY",
		"size":   0.05,
	})

	mockLLM := NewLLMMockFromSlice([]string{"Approving order based on momentum"})
	mockLLM.WithToolCall("approve_and_route_order", approveArgs)

	rt := agentpkg.NewRuntime(mockLLM, NoopLogStore{}, config.AgentConfig{MaxTurns: 1, TimeoutTotalSeconds: 5})
	riskAgent := agentpkg.NewRiskAgent(rt, agentTools)

	sig := domain.Signal{
		ID:          "sig-1",
		Symbol:      "BTC/USDT",
		Side:        domain.SideBuy,
		Strategy:    "trend_following",
		Reason:      "Golden cross confirmed",
		GeneratedAt: time.Now().UTC(),
	}

	approved, err := riskAgent.Evaluate(ctx, sig, nil)
	if err != nil {
		t.Fatalf("RiskAgent.Evaluate error: %v", err)
	}
	if !approved {
		t.Fatalf("expected signal to be approved by agent")
	}
	if !routedOrder {
		t.Fatalf("expected order to be routed by agent tool")
	}

	// Verify order is pending in paper matcher
	orders := book.OpenOrders()
	if len(orders) != 1 {
		t.Fatalf("expected 1 open order in paper book, got %d", len(orders))
	}
	if !orders[0].Intent.Quantity.Equal(decimal.NewFromFloat(0.05)) {
		t.Errorf("expected size 0.05, got %s", orders[0].Intent.Quantity)
	}
}

// ── 3. Agent Decision: Reject Signal ──────────────────────────────────────────

func TestBacktest_AgentDecision_RejectSignal(t *testing.T) {
	ctx := context.Background()
	store := NewMemTradeStore()
	book := paper.NewBook()
	matcher := paper.NewMatcher(book, store, 0.0004)

	var routedOrder bool
	audit := &testAuditStore{}
	routeFn := func(_ context.Context, _ agenttools.AgentOrderRequest) error {
		routedOrder = true
		return nil
	}

	agentTools := map[string]port.Tool{
		"reject_signal":           agenttools.RejectSignal(audit),
		"approve_and_route_order": agenttools.ApproveAndRouteOrder(routeFn),
	}

	rejectArgs, _ := json.Marshal(map[string]any{
		"reason": "Market volatility too high before news event",
	})

	mockLLM := NewLLMMockFromSlice([]string{"Rejecting signal due to impending macro risk"})
	mockLLM.WithToolCall("reject_signal", rejectArgs)

	rt := agentpkg.NewRuntime(mockLLM, NoopLogStore{}, config.AgentConfig{MaxTurns: 1, TimeoutTotalSeconds: 5})
	riskAgent := agentpkg.NewRiskAgent(rt, agentTools)

	sig := domain.Signal{
		ID:          "sig-2",
		Symbol:      "BTC/USDT",
		Side:        domain.SideBuy,
		Strategy:    "trend_following",
		Reason:      "EMA cross",
		GeneratedAt: time.Now().UTC(),
	}

	approved, err := riskAgent.Evaluate(ctx, sig, nil)
	if err != nil {
		t.Fatalf("unexpected evaluate error: %v", err)
	}
	if approved {
		t.Fatalf("expected signal to be rejected by risk agent")
	}
	if routedOrder {
		t.Fatalf("order should NOT be routed when agent rejects")
	}
	if len(matcher.Venue()) == 0 {
		t.Fatal("matcher venue empty")
	}
}

// ── 4. Agent Decision: Fail-Closed on Error ───────────────────────────────────

func TestBacktest_AgentDecision_FailClosed(t *testing.T) {
	ctx := context.Background()
	audit := &testAuditStore{}
	agentTools := map[string]port.Tool{
		"reject_signal": agenttools.RejectSignal(audit),
	}

	mockLLM := NewLLMMockFromSlice([]string{})
	mockLLM.WithError(errors.New("API rate limit exceeded / connection reset"))

	rt := agentpkg.NewRuntime(mockLLM, NoopLogStore{}, config.AgentConfig{MaxTurns: 1, TimeoutTotalSeconds: 5})
	riskAgent := agentpkg.NewRiskAgent(rt, agentTools)

	sig := domain.Signal{
		ID:          "sig-3",
		Symbol:      "BTC/USDT",
		Side:        domain.SideBuy,
		Strategy:    "trend_following",
		GeneratedAt: time.Now().UTC(),
	}

	approved, err := riskAgent.Evaluate(ctx, sig, nil)
	if approved {
		t.Fatalf("risk agent must FAIL-CLOSED on LLM error")
	}
	if err == nil {
		t.Fatalf("expected error from evaluate when LLM fails")
	}
}

// ── 5. Cerebro Logics: Dedup Window ──────────────────────────────────────────

func TestBacktest_CerebroLogics_DedupWindow(t *testing.T) {
	dedup := strategy.NewDedupWindow(10 * time.Minute)

	baseTime := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)
	sig1 := domain.Signal{
		Symbol:      "BTC/USDT",
		Strategy:    "trend_following",
		Side:        domain.SideBuy,
		GeneratedAt: baseTime,
	}

	// First signal should pass
	if !dedup.Allow(sig1, 10*time.Minute) {
		t.Fatalf("first signal must be allowed by dedup")
	}

	// Repeat signal 3 minutes later (inside 10m window) must be rejected
	sig2 := sig1
	sig2.GeneratedAt = baseTime.Add(3 * time.Minute)
	if dedup.Allow(sig2, 10*time.Minute) {
		t.Fatalf("duplicate signal within 10m window must be deduplicated")
	}

	// Different side (SELL) within window should be allowed
	sigSell := sig1
	sigSell.Side = domain.SideSell
	sigSell.GeneratedAt = baseTime.Add(4 * time.Minute)
	if !dedup.Allow(sigSell, 10*time.Minute) {
		t.Fatalf("different side signal must be allowed")
	}

	// Signal 11 minutes later (outside 10m window) must be allowed
	sig3 := sig1
	sig3.GeneratedAt = baseTime.Add(11 * time.Minute)
	if !dedup.Allow(sig3, 10*time.Minute) {
		t.Fatalf("signal after window expiry must be allowed")
	}
}

// ── 6. Cerebro Logics: Risk Gate Checks ───────────────────────────────────────

func TestBacktest_CerebroLogics_RiskGate(t *testing.T) {
	ctx := context.Background()
	riskCfg := config.RiskConfig{
		MaxDrawdownPct:            5.0,
		MaxDailyLossPct:           2.0,
		MaxOpenPositions:          2,
		MaxOpenPositionsPerSymbol: 1,
	}

	gate := risk.NewGate(riskCfg, newTestCache(), risk.NewCalendarBlackout())
	gate.SetStartingEquity(decimal.NewFromInt(10_000))
	gate.SetAllowedSymbols([]domain.Symbol{"BTC/USDT", "ETH/USDT"})

	sigBTC := domain.Signal{Symbol: "BTC/USDT", Side: domain.SideBuy, Strategy: "test"}
	sigDOGE := domain.Signal{Symbol: "DOGE/USDT", Side: domain.SideBuy, Strategy: "test"}

	// 1. Unconfigured symbol outside allow-list must be rejected
	if err := gate.Check(ctx, sigDOGE, nil); err == nil {
		t.Fatalf("unconfigured symbol DOGE must be rejected by risk gate")
	}

	// 2. Allowed symbol with 0 open positions should pass
	if err := gate.Check(ctx, sigBTC, nil); err != nil {
		t.Fatalf("allowed symbol BTC should pass: %v", err)
	}

	// 3. MaxOpenPositionsPerSymbol check
	existingPos := []domain.Position{
		{Symbol: "BTC/USDT", Side: domain.SideBuy, Quantity: decimal.NewFromFloat(0.1)},
	}
	if err := gate.Check(ctx, sigBTC, existingPos); err == nil {
		t.Fatalf("should reject when symbol already has max positions")
	}

	// 4. Global halt check
	gate.SetHalt(domain.HaltModePause)
	if err := gate.Check(ctx, domain.Signal{Symbol: "ETH/USDT", Side: domain.SideBuy}, nil); err == nil {
		t.Fatalf("gate must reject all signals when halt is active")
	}
	gate.ClearHalt()

	// 5. Drawdown limit tripping: simulate realized loss exceeding 5%
	gate.UpdatePnL(decimal.NewFromInt(-600)) // -$600 loss on $10,000 equity = 6% loss
	if err := gate.Check(ctx, domain.Signal{Symbol: "ETH/USDT", Side: domain.SideBuy}, nil); err == nil {
		t.Fatalf("gate must reject signal when session drawdown exceeds MaxDrawdownPct")
	}
}

// ── 7. Strategies: Indicator Evaluation and Signal Generation ────────────────

func TestBacktest_Strategies_TrendFollowing_GoldenCross(t *testing.T) {
	stratCfg := config.StrategyConfig{
		Name:             "trend_following",
		Enabled:          true,
		Markets:          []string{"BTC/USDT"},
		PrimaryTimeframe: domain.TF1m,
		WarmupCandles:    3,
		Indicators: config.IndicatorConfig{
			EMA: config.EMAConfig{
				Fast:      3,
				Slow:      5,
				LongTrend: 10,
			},
			ATR: config.ATRConfig{Period: 5},
		},
	}
	strat := strategy.NewTrendFollowing(stratCfg)

	// Feed candles to establish death cross first, then sudden rise to create golden cross
	baseTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	prices := []float64{100, 95, 90, 85, 80, 75, 120, 150}

	var signals []domain.Signal
	for i, p := range prices {
		cTime := baseTime.Add(time.Duration(i) * time.Minute)
		candle := domain.Candle{
			Symbol:    "BTC/USDT",
			Timeframe: domain.TF1m,
			OpenTime:  cTime,
			CloseTime: cTime.Add(time.Minute),
			Open:      decimal.NewFromFloat(p - 1),
			High:      decimal.NewFromFloat(p + 2),
			Low:       decimal.NewFromFloat(p - 2),
			Close:     decimal.NewFromFloat(p),
			Volume:    decimal.NewFromInt(10),
			Closed:    true,
		}
		sig, fired := strat.OnCandle(context.Background(), candle)
		if fired {
			signals = append(signals, sig)
		}
	}

	if len(signals) == 0 {
		t.Fatalf("expected golden cross signal to fire on price breakout")
	}
	if signals[0].Side != domain.SideBuy {
		t.Errorf("expected BUY signal on golden cross, got %s", signals[0].Side)
	}
}

// ── 8. Paper Matcher: Next-Candle-Open Fill and Bracket PnL ──────────────────

func TestBacktest_PaperMatcher_ExecutionAndPnL(t *testing.T) {
	ctx := context.Background()
	store := NewMemTradeStore()
	book := paper.NewBook()
	matcher := paper.NewMatcher(book, store, 0.0) // 0% commission for clean math

	t0 := time.Date(2024, 1, 1, 10, 0, 0, 0, time.UTC)

	// 1. Place order at candle 0 close
	intent := domain.OrderIntent{
		ID:          "intent-1",
		Symbol:      "BTC/USDT",
		Side:        domain.SideBuy,
		Quantity:    decimal.NewFromInt(1),
		Strategy:    "trend_following",
		Environment: domain.EnvironmentPaper,
		CreatedAt:   t0,
	}
	if _, err := matcher.PlaceOrder(ctx, intent); err != nil {
		t.Fatalf("PlaceOrder failed: %v", err)
	}

	// 2. Candle 1 arrives: open=100. Pending entry must fill at 100!
	c1 := domain.Candle{
		Symbol:    "BTC/USDT",
		Timeframe: domain.TF1m,
		OpenTime:  t0,
		CloseTime: t0.Add(time.Minute),
		Open:      decimal.NewFromInt(100),
		High:      decimal.NewFromInt(105),
		Low:       decimal.NewFromInt(95),
		Close:     decimal.NewFromInt(102),
		Closed:    true,
	}
	matcher.OnCandle(ctx, c1)

	positions, _ := matcher.Positions(ctx)
	if len(positions) != 1 {
		t.Fatalf("expected 1 filled position, got %d", len(positions))
	}
	if !positions[0].EntryPrice.Equal(decimal.NewFromInt(100)) {
		t.Fatalf("expected fill at next candle open 100, got %s", positions[0].EntryPrice)
	}

	// 3. Attach bracket: stop at 90, take-profit at 120
	bracketReq := domain.BracketRequest{
		ParentIntentID: intent.ID,
		Symbol:         "BTC/USDT",
		Venue:          domain.VenueBinanceSpot,
		Side:           domain.SideBuy,
		Quantity:       decimal.NewFromInt(1),
		StopLoss:       decimal.NewFromInt(90),
		TakeProfit:     decimal.NewFromInt(120),
	}
	if _, err := matcher.PlaceBracket(ctx, bracketReq); err != nil {
		t.Fatalf("PlaceBracket failed: %v", err)
	}

	// 4. Candle 2 arrives: high hits 125, triggering Take Profit!
	c2 := domain.Candle{
		Symbol:    "BTC/USDT",
		Timeframe: domain.TF1m,
		OpenTime:  t0.Add(time.Minute),
		CloseTime: t0.Add(2 * time.Minute),
		Open:      decimal.NewFromInt(103),
		High:      decimal.NewFromInt(125), // triggers TP at 120
		Low:       decimal.NewFromInt(101),
		Close:     decimal.NewFromInt(122),
		Closed:    true,
	}
	matcher.OnCandle(ctx, c2)

	// Position should now be closed
	positionsAfter, _ := matcher.Positions(ctx)
	if len(positionsAfter) != 0 {
		t.Fatalf("expected position to be closed by TP, got %d positions", len(positionsAfter))
	}

	// Verify trade was saved with realized PnL
	trades := store.AllTrades()
	if len(trades) < 2 { // 1 entry fill, 1 bracket exit
		t.Fatalf("expected at least 2 trades (entry + exit), got %d", len(trades))
	}

	var exitTrade *domain.Trade
	for _, tr := range trades {
		if tr.Side == domain.SideSell && tr.PnL != nil {
			exitTrade = &tr
			break
		}
	}
	if exitTrade == nil {
		t.Fatalf("exit trade with realized PnL not found")
	}
	// Entry 100, exit 120, qty 1 -> PnL = +20
	wantPnL := decimal.NewFromInt(20)
	if !exitTrade.PnL.Equal(wantPnL) {
		t.Errorf("expected realized PnL = %s, got %s", wantPnL, exitTrade.PnL)
	}

	// Compute report from trades
	report := ComputeReport("trend_following", "BTC/USDT", t0, t0.Add(2*time.Minute), trades)
	if report.WinRate != 100.0 {
		t.Errorf("expected 100%% win rate, got %.2f%%", report.WinRate)
	}
	if report.NetPnL != 20.0 {
		t.Errorf("expected NetPnL 20.0, got %.2f", report.NetPnL)
	}
}

// ── 9. Agent Decision Time-by-Time Audit & Verdict Correlation ───────────────

func TestBacktest_AgentDecision_TimeByTimeAudit(t *testing.T) {
	ctx := context.Background()
	store := NewMemTradeStore()
	book := paper.NewBook()
	matcher := paper.NewMatcher(book, store, 0.0)
	tracker := NewDecisionTracker()

	routeFn := func(_ context.Context, req agenttools.AgentOrderRequest) error {
		intent := domain.OrderIntent{
			ID:          "audit-order-1",
			Symbol:      req.Symbol,
			Side:        req.Side,
			Quantity:    decimal.NewFromFloat(req.Size),
			Strategy:    "trend_following",
			Environment: domain.EnvironmentPaper,
			CreatedAt:   time.Now().UTC(),
		}
		_, err := matcher.PlaceOrder(ctx, intent)
		return err
	}

	agent := BuildFreeRiskAgent(routeFn, "smart")

	t0 := time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)
	c1 := domain.Candle{
		Symbol:    "BTC/USDT",
		Timeframe: domain.TF1h,
		OpenTime:  t0,
		CloseTime: t0.Add(time.Hour),
		Open:      decimal.NewFromInt(42000),
		High:      decimal.NewFromInt(42500),
		Low:       decimal.NewFromInt(41800),
		Close:     decimal.NewFromInt(42400),
		Closed:    true,
	}

	sim := NewSimulator([]domain.Candle{c1}, nil, nil, matcher, NewSimClock(t0))
	sim.SetTradeStore(store)
	sim.SetRiskAgent(agent)
	sim.SetDecisionTracker(tracker)

	sig := domain.Signal{
		ID:          "sig-audit-1",
		Symbol:      "BTC/USDT",
		Side:        domain.SideBuy,
		Strategy:    "trend_following",
		Reason:      "EMA golden cross",
		GeneratedAt: t0,
	}

	// Evaluate signal directly through agent
	approved, err := agent.Evaluate(ctx, sig, nil)
	if err != nil || !approved {
		t.Fatalf("expected smart heuristic to approve, got approved=%v, err=%v", approved, err)
	}

	// Record in tracker
	tracker.Record(AgentDecisionRecord{
		Timestamp:    t0,
		Symbol:       "BTC/USDT",
		Strategy:     "trend_following",
		SignalSide:   "BUY",
		Price:        42400,
		SignalReason: sig.Reason,
		Action:       "approved",
		Rationale:    "Signal approved by heuristic risk evaluation",
	})

	// Simulate closing trade with profit
	pnl := decimal.NewFromInt(350)
	completedTrade := domain.Trade{
		ID:        "exit-trade-1",
		Symbol:    "BTC/USDT",
		Strategy:  "trend_following",
		Side:      domain.SideSell,
		PnL:       &pnl,
		CreatedAt: t0.Add(2 * time.Hour),
	}
	tracker.CorrelateWithTrades([]domain.Trade{completedTrade})

	records := tracker.Records()
	if len(records) != 1 {
		t.Fatalf("expected 1 decision record, got %d", len(records))
	}
	if records[0].Verdict != "WIN" {
		t.Errorf("expected WIN verdict for profitable approved trade, got %s", records[0].Verdict)
	}
	if records[0].OutcomePnL == nil || *records[0].OutcomePnL != 350.0 {
		t.Errorf("expected OutcomePnL 350.0, got %v", records[0].OutcomePnL)
	}
}
func TestBacktest_AgentDecision_MultiSignalCorrelation_NoDuplication(t *testing.T) {
	tracker := NewDecisionTracker()
	t0 := time.Date(2026, 1, 10, 10, 0, 0, 0, time.UTC)

	// Decision 1: BUY at t0, intent bt-1
	tracker.Record(AgentDecisionRecord{
		Timestamp:  t0,
		Symbol:     "BTC/USDT",
		Strategy:   "trend_following",
		SignalSide: "BUY",
		Action:     "approved",
		IntentID:   "intent-1",
	})

	// Decision 2: SELL at t0+1h (e.g. noise), intent bt-2
	tracker.Record(AgentDecisionRecord{
		Timestamp:  t0.Add(time.Hour),
		Symbol:     "BTC/USDT",
		Strategy:   "trend_following",
		SignalSide: "SELL",
		Action:     "approved",
		IntentID:   "intent-2",
	})

	// Decision 3: BUY at t0+2h, intent bt-3
	tracker.Record(AgentDecisionRecord{
		Timestamp:  t0.Add(2 * time.Hour),
		Symbol:     "BTC/USDT",
		Strategy:   "trend_following",
		SignalSide: "BUY",
		Action:     "approved",
		IntentID:   "intent-3",
	})

	// Decision 4: Rejected decision
	tracker.Record(AgentDecisionRecord{
		Timestamp:  t0.Add(3 * time.Hour),
		Symbol:     "BTC/USDT",
		Strategy:   "trend_following",
		SignalSide: "BUY",
		Action:     "rejected",
	})

	// Only intent-3 actually executed and exited with a loss
	lossPnL := decimal.NewFromFloat(-9.05)
	trade1 := domain.Trade{
		ID:        "exit-trade-3",
		IntentID:  "intent-3",
		Symbol:    "BTC/USDT",
		Strategy:  "trend_following",
		Side:      domain.SideSell,
		PnL:       &lossPnL,
		CreatedAt: t0.Add(5 * time.Hour),
	}

	tracker.CorrelateWithTrades([]domain.Trade{trade1})
	records := tracker.Records()

	// Decision 1 should be NEUTRAL (its position never closed via bracket)
	if records[0].Verdict != "NEUTRAL" || records[0].OutcomePnL != nil {
		t.Errorf("decision 0: expected NEUTRAL nil PnL, got %s %v", records[0].Verdict, records[0].OutcomePnL)
	}

	// Decision 2 should be NEUTRAL (not duplicated!)
	if records[1].Verdict != "NEUTRAL" || records[1].OutcomePnL != nil {
		t.Errorf("decision 1: expected NEUTRAL nil PnL, got %s %v", records[1].Verdict, records[1].OutcomePnL)
	}

	// Decision 3 should be LOSS -9.05
	if records[2].Verdict != "LOSS" || records[2].OutcomePnL == nil || *records[2].OutcomePnL != -9.05 {
		t.Errorf("decision 2: expected LOSS -9.05, got %s %v", records[2].Verdict, records[2].OutcomePnL)
	}

	// Decision 4 should be REJECTED
	if records[3].Verdict != "REJECTED" || records[3].OutcomePnL != nil {
		t.Errorf("decision 3: expected REJECTED nil PnL, got %s %v", records[3].Verdict, records[3].OutcomePnL)
	}
}

func TestBacktest_AgentDecision_LongHoldingSwingTrade(t *testing.T) {
	tracker := NewDecisionTracker()
	t0 := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)

	// Decision held for 4 days (96 hours)
	tracker.Record(AgentDecisionRecord{
		Timestamp:  t0,
		Symbol:     "BTC/USDT",
		Strategy:   "trend_following",
		SignalSide: "BUY",
		Action:     "approved",
		IntentID:   "intent-swing-1",
	})

	winPnL := decimal.NewFromFloat(1750.50)
	trade := domain.Trade{
		ID:        "exit-swing-1",
		IntentID:  "intent-swing-1",
		Symbol:    "BTC/USDT",
		Strategy:  "trend_following",
		Side:      domain.SideSell,
		PnL:       &winPnL,
		CreatedAt: t0.Add(96 * time.Hour), // 4 days later!
	}

	tracker.CorrelateWithTrades([]domain.Trade{trade})
	records := tracker.Records()

	if records[0].Verdict != "WIN" {
		t.Errorf("expected WIN for swing trade held 4 days, got %s", records[0].Verdict)
	}
	if records[0].OutcomePnL == nil || *records[0].OutcomePnL != 1750.50 {
		t.Errorf("expected OutcomePnL 1750.50, got %v", records[0].OutcomePnL)
	}
}

func TestBacktest_HighProfitTarget(t *testing.T) {
	csvPath := filepath.Join("..", "..", "testdata", "fixtures", "btc_1m.csv")
	candles, err := LoadCSV(csvPath, domain.Symbol("BTC/USDT"), domain.TF1m)
	if err != nil {
		t.Fatalf("LoadCSV failed: %v", err)
	}

	stratCfg := config.StrategyConfig{
		Name:             "mean_reversion",
		Enabled:          true,
		Markets:          []string{"BTC/USDT"},
		PrimaryTimeframe: domain.TF1m,
		WarmupCandles:    10,
		Indicators: config.IndicatorConfig{
			RSI: config.RSIConfig{
				Period:     14,
				Oversold:   40,
				Overbought: 60,
			},
			Bollinger: config.BollingerConfig{
				Period: 20,
				StdDev: 1.2,
			},
		},
	}
	strat := strategy.NewMeanReversion(stratCfg)

	store := NewMemTradeStore()
	book := paper.NewBook()
	matcher := paper.NewMatcher(book, store, 0.0004)
	clock := NewSimClock(candles[0].OpenTime)
	hub := marketdata.NewHub()

	sim := NewSimulator(candles, hub, []port.Strategy{strat}, matcher, clock)
	sim.SetTradeStore(store)
	sim.SetDedup(strategy.NewDedupWindow(1 * time.Minute))
	sim.SetDefaultQuantity(decimal.NewFromFloat(5.0))

	sim.SetBracketResolver(func(c domain.Candle, sig domain.Signal) (decimal.Decimal, decimal.Decimal) {
		if sig.Side == domain.SideBuy {
			sl := c.Close.Mul(decimal.NewFromFloat(0.97))  // 3% SL
			tp := c.Close.Mul(decimal.NewFromFloat(1.003)) // 0.3% TP
			return sl, tp
		}
		sl := c.Close.Mul(decimal.NewFromFloat(1.03))
		tp := c.Close.Mul(decimal.NewFromFloat(0.997))
		return sl, tp
	})

	ctx := context.Background()
	trades, err := sim.Run(ctx)
	if err != nil {
		t.Fatalf("sim.Run failed: %v", err)
	}

	report := ComputeReport("mean_reversion", "BTC/USDT", candles[0].OpenTime, candles[len(candles)-1].CloseTime, trades)
	t.Logf("Report: Trades=%d, WinRate=%.2f%%, NetPnL=%.2f USDT, MaxDD=%.2f%%",
		report.TotalTrades, report.WinRate, report.NetPnL, report.MaxDrawdown)

	if report.TotalTrades < 10 {
		t.Errorf("expected at least 10 trades, got %d", report.TotalTrades)
	}
	if report.WinRate < 96.0 {
		t.Errorf("expected win rate >= 96%%, got %.2f%%", report.WinRate)
	}
}

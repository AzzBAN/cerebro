# Agent Decision, Dynamic Leverage, and Closed-Trade Learning Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fix losing agent decisions by providing dynamic leverage and liquidation calculation, injecting Screening Agent market bias into the Risk Agent prompt, caching the economic calendar to stop HTTP 429 rate limits, and recording closed Binance Futures trades to enable the self-learning reflection loop.

**Architecture:** Pure hexagonal architecture. Math logic for safe leverage and liquidation boundaries lives in `internal/risk`. Tool and prompt enrichments live in `internal/agent`. Calendar caching lives in `internal/ingest/calendar`. WebSocket trade fill handling lives in `internal/adapter/binance/futures/rest.go`, feeding `port.TradeStore` so `injectPerformanceContextSystem` and `ReviewerAgent` have real trade history for in-context learning.

**Tech Stack:** Go 1.25, `shopspring/decimal`, `go-binance/v2/futures`, slog, pgx/v5.

**Spec:** Documented in this plan; driven by live trading observations from `logs/cerebro.log` and `/api/state`.

## Global Constraints

- Never use `float64` for money; all prices, quantities, and PnL must use `shopspring/decimal.Decimal`.
- Hexagonal boundaries: `internal/domain` has zero dependencies; `internal/port` interfaces only; implementations in `internal/adapter` and `internal/agent/tools`.
- Risk gate fail-closed: if calculations or LLM fail, default to reject or safe hold.
- Liquidation Price must NEVER be inside the Stop Loss distance; minimum 1.5× stop-loss buffer required.
- Standard Go testing with table-driven tests; no unrequested third-party test libraries.

---

### Task 1: Dynamic Leverage & Liquidation Calculator

**Files:**
- Create: `internal/risk/leverage.go`
- Modify: `internal/domain/risk.go:5-15`
- Modify: `internal/agent/tools/risk_tools.go:40-85`
- Test: `internal/risk/leverage_test.go`
- Test: `internal/agent/tools/risk_tools_test.go`

**Interfaces:**
- Consumes: `domain.Side`, `decimal.Decimal`
- Produces: `risk.CalculateSafeLeverage(entryPrice, stopLoss decimal.Decimal, side domain.Side, equity decimal.Decimal, riskPct float64, mmr decimal.Decimal) (risk.LeverageResult, error)`

- [ ] **Step 1: Write the failing test for leverage and liquidation math**

```go
// internal/risk/leverage_test.go
package risk

import (
	"testing"

	"github.com/azhar/cerebro/internal/domain"
	"github.com/shopspring/decimal"
)

func TestCalculateSafeLeverage_Long(t *testing.T) {
	// Entry: 100, StopLoss: 98 (2% distance)
	// Equity: 1000 USDT, Risk: 1% (10 USDT risk)
	// Qty = 10 / 2 = 5 units. Notional = 500 USDT.
	// MMR = 0.005 (0.5%)
	// Safe leverage must ensure LiqPrice <= 97 (at least 1.5x SL distance from entry).
	entry := decimal.NewFromInt(100)
	sl := decimal.NewFromInt(98)
	equity := decimal.NewFromInt(1000)
	riskPct := 1.0
	mmr := decimal.NewFromFloat(0.005)

	res, err := CalculateSafeLeverage(entry, sl, domain.SideBuy, equity, riskPct, mmr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if res.MaxSafeLeverage <= 0 {
		t.Fatalf("expected positive MaxSafeLeverage, got %d", res.MaxSafeLeverage)
	}
	// For 2% SL with 1.5x buffer (3%) + 0.5% MMR = 3.5% adverse move => max safe lev <= 1 / 0.035 = 28x
	if res.MaxSafeLeverage > 28 {
		t.Errorf("MaxSafeLeverage = %d; want <= 28", res.MaxSafeLeverage)
	}
	if res.LiquidationPrice.GreaterThanOrEqual(sl) {
		t.Errorf("LiquidationPrice (%s) must be below stop loss (%s)", res.LiquidationPrice, sl)
	}
}

func TestCalculateSafeLeverage_Short(t *testing.T) {
	// Entry: 100, StopLoss: 102 (2% distance)
	entry := decimal.NewFromInt(100)
	sl := decimal.NewFromInt(102)
	equity := decimal.NewFromInt(1000)
	riskPct := 1.0
	mmr := decimal.NewFromFloat(0.005)

	res, err := CalculateSafeLeverage(entry, sl, domain.SideSell, equity, riskPct, mmr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if res.LiquidationPrice.LessThanOrEqual(sl) {
		t.Errorf("LiquidationPrice (%s) must be above stop loss (%s) for short", res.LiquidationPrice, sl)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -v ./internal/risk -run TestCalculateSafeLeverage`
Expected: FAIL (symbol `CalculateSafeLeverage` undefined).

- [ ] **Step 3: Implement `CalculateSafeLeverage` in `internal/risk/leverage.go` and update `CalculatePositionSize` tool**

```go
// internal/risk/leverage.go
package risk

import (
	"fmt"

	"github.com/azhar/cerebro/internal/domain"
	"github.com/shopspring/decimal"
)

// LeverageResult holds dynamic leverage and liquidation calculations.
type LeverageResult struct {
	Quantity                decimal.Decimal `json:"quantity"`
	Notional                decimal.Decimal `json:"notional"`
	MaxSafeLeverage         int             `json:"max_safe_leverage"`
	RecommendedLeverage     int             `json:"recommended_leverage"`
	LiquidationPrice        decimal.Decimal `json:"liquidation_price"`
	RequiredMargin          decimal.Decimal `json:"required_margin"`
	StopLossDistancePct     decimal.Decimal `json:"stop_loss_distance_pct"`
}

// CalculateSafeLeverage computes safe leverage such that estimated liquidation
// is at least 1.5x the stop-loss distance away from entry price.
func CalculateSafeLeverage(
	entryPrice, stopLoss decimal.Decimal,
	side domain.Side,
	equity decimal.Decimal,
	riskPct float64,
	mmr decimal.Decimal,
) (LeverageResult, error) {
	if entryPrice.IsZero() || entryPrice.IsNegative() {
		return LeverageResult{}, fmt.Errorf("entry price must be positive")
	}
	if stopLoss.IsZero() || stopLoss.IsNegative() {
		return LeverageResult{}, fmt.Errorf("stop loss must be positive")
	}
	if equity.IsZero() || equity.IsNegative() {
		return LeverageResult{}, fmt.Errorf("equity must be positive")
	}
	if riskPct <= 0 {
		return LeverageResult{}, fmt.Errorf("risk pct must be positive")
	}
	if mmr.IsZero() {
		mmr = decimal.NewFromFloat(0.005) // 0.5% default maintenance margin rate
	}

	slDist := entryPrice.Sub(stopLoss).Abs()
	if slDist.IsZero() {
		return LeverageResult{}, fmt.Errorf("stop loss distance is zero")
	}

	slDistPct := slDist.Div(entryPrice)
	riskAmt := equity.Mul(decimal.NewFromFloat(riskPct / 100))
	qty := riskAmt.Div(slDist)
	notional := qty.Mul(entryPrice)

	// Safe buffer: require liquidation to be at least 1.5x the stop-loss distance away.
	safeBufferDistPct := slDistPct.Mul(decimal.NewFromFloat(1.5)).Add(mmr)
	if safeBufferDistPct.GreaterThanOrEqual(decimal.NewFromInt(1)) {
		safeBufferDistPct = decimal.NewFromFloat(0.99)
	}

	// Max safe leverage = 1 / safeBufferDistPct
	maxSafeLevDecimal := decimal.NewFromInt(1).Div(safeBufferDistPct)
	maxSafeLev := int(maxSafeLevDecimal.IntPart())
	if maxSafeLev < 1 {
		maxSafeLev = 1
	}
	if maxSafeLev > 125 {
		maxSafeLev = 125
	}

	// Recommended leverage: balance small margin requirements against risk.
	recLev := maxSafeLev
	// If required margin at maxSafeLev is well below equity, clamp to conservative upper bound of 20x.
	if recLev > 20 {
		recLev = 20
	}
	if recLev < 1 {
		recLev = 1
	}

	reqMargin := notional.Div(decimal.NewFromInt(int64(recLev)))

	// Liquidation price calculation:
	// Long: Liq = Entry * (1 - 1/Lev + MMR)
	// Short: Liq = Entry * (1 + 1/Lev - MMR)
	levDec := decimal.NewFromInt(int64(recLev))
	var liqPrice decimal.Decimal
	if side == domain.SideBuy {
		factor := decimal.NewFromInt(1).Sub(decimal.NewFromInt(1).Div(levDec)).Add(mmr)
		liqPrice = entryPrice.Mul(factor)
		if liqPrice.IsNegative() {
			liqPrice = decimal.Zero
		}
	} else {
		factor := decimal.NewFromInt(1).Add(decimal.NewFromInt(1).Div(levDec)).Sub(mmr)
		liqPrice = entryPrice.Mul(factor)
	}

	return LeverageResult{
		Quantity:            qty,
		Notional:            notional,
		MaxSafeLeverage:     maxSafeLev,
		RecommendedLeverage: recLev,
		LiquidationPrice:    liqPrice,
		RequiredMargin:      reqMargin,
		StopLossDistancePct: slDistPct.Mul(decimal.NewFromInt(100)),
	}, nil
}
```

Update `CalculatePositionSize` tool in `internal/agent/tools/risk_tools.go` to return these fields in its JSON output.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test -v ./internal/risk -run TestCalculateSafeLeverage`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/risk/leverage.go internal/risk/leverage_test.go internal/agent/tools/risk_tools.go
git commit -m "feat(risk): add dynamic safe leverage and liquidation calculator"
```

---

### Task 2: Inject Market Bias Context into Risk Agent & Update Risk Prompt

**Files:**
- Modify: `internal/agent/risk_agent.go:45-110,230-268`
- Modify: `internal/agent/prompts/risk.tmpl`
- Modify: `internal/app/strategies.go:238-245`
- Test: `internal/agent/risk_agent_test.go`

**Interfaces:**
- Consumes: `port.Cache`, `domain.BiasResult`
- Produces: Enriched `buildUserMessage` containing current market bias, score, and reasoning.

- [ ] **Step 1: Write the failing test for RiskAgent bias injection**

```go
// In internal/agent/risk_agent_test.go
func TestRiskAgent_BuildUserMessage_IncludesMarketBias(t *testing.T) {
	// Verify that when cache contains bias:BTC/USDT-PERP, the user message
	// includes the bias label and score so Risk Agent does not trade blind.
	ra := newTestRiskAgent()
	cache := newMockCache()
	bias := domain.BiasResult{
		Symbol: "BTC/USDT-PERP",
		Label:  domain.BiasBullish,
		Score:  1,
		Reasoning: "Strong volume and rising OI",
	}
	bBytes, _ := json.Marshal(bias)
	_ = cache.Set(context.Background(), "bias:BTC/USDT-PERP", bBytes, 0)
	ra.SetCache(cache)

	sig := domain.Signal{
		Symbol:   "BTC/USDT-PERP",
		Side:     domain.SideSell,
		Strategy: "mean_reversion",
		Reason:   "RSI overbought",
	}
	msg := ra.buildUserMessage(context.Background(), sig, sig.Reason, nil)
	if !strings.Contains(msg, "market_bias=Bullish") {
		t.Errorf("user message should include market_bias=Bullish, got: %s", msg)
	}
	if !strings.Contains(msg, "bias_score=1") {
		t.Errorf("user message should include bias_score=1, got: %s", msg)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -v ./internal/agent -run TestRiskAgent_BuildUserMessage_IncludesMarketBias`
Expected: FAIL (method `SetCache` undefined or string missing).

- [ ] **Step 3: Implement `SetCache` and bias injection in `risk_agent.go` and update `risk.tmpl`**

In `internal/agent/risk_agent.go`:
1. Add `cache port.Cache` to `RiskAgent` struct and `SetCache(c port.Cache)`.
2. In `buildUserMessage`:
```go
	if r.cache != nil {
		if bBytes, err := r.cache.Get(ctx, fmt.Sprintf("bias:%s", sig.Symbol)); err == nil && len(bBytes) > 0 {
			var bias domain.BiasResult
			if err := json.Unmarshal(bBytes, &bias); err == nil {
				fmt.Fprintf(&b, " market_bias=%s bias_score=%d bias_reason=%q", bias.Label, bias.Score, bias.Reasoning)
			}
		}
	}
```
3. In `internal/agent/prompts/risk.tmpl`:
Add explicit rules under `Decision rules`:
```text
2. Market Bias Alignment:
   - If market_bias is Bullish (bias_score > 0) and the signal is SELL, REJECT the signal unless there is explicit breakdown evidence. Do NOT fight the prevailing bias.
   - If market_bias is Bearish (bias_score < 0) and the signal is BUY, REJECT the signal.
   - If market_bias is Neutral or missing, proceed with normal risk checks.

3. Dynamic Leverage & Liquidation Safety:
   - Call calculate_position_size to determine quantity and safe leverage.
   - You MUST pass an explicit `leverage` in approve_and_route_order.
   - Ensure the liquidation price is at least 1.5x farther away than the stop_loss so the trade cannot be liquidated before the stop-loss triggers.
   - For smaller account equity, choose a leverage that allows the order to satisfy minimum notional while keeping required margin within free balance.
```
4. In `internal/app/strategies.go:242`: call `riskAgent.SetCache(gate.Cache())` or pass cache.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -v ./internal/agent -run TestRiskAgent_BuildUserMessage_IncludesMarketBias`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/agent/risk_agent.go internal/agent/prompts/risk.tmpl internal/agent/risk_agent_test.go internal/app/strategies.go
git commit -m "feat(agent): inject market bias and dynamic leverage guidance into risk agent"
```

---

### Task 3: Enforce Strategy Bias Alignment in Config & Risk Gate

**Files:**
- Modify: `configs/strategies.yaml:268,444,540`
- Modify: `internal/risk/gate.go:150-170`
- Test: `internal/risk/gate_test.go`

**Interfaces:**
- Consumes: `domain.Signal`, `domain.BiasResult`
- Produces: Risk gate rejection when signal directly contradicts active bias on bias-aligned strategies.

- [ ] **Step 1: Write failing test in `gate_test.go`**

```go
func TestGate_RejectsOpposingBiasWhenEnforced(t *testing.T) {
	cache := newMockCache()
	bias := domain.BiasResult{
		Symbol: "BTC/USDT-PERP",
		Label:  domain.BiasBullish,
		Score:  1,
	}
	bBytes, _ := json.Marshal(bias)
	_ = cache.Set(context.Background(), "bias:BTC/USDT-PERP", bBytes, 0)

	cfg := GateConfig{
		MaxDrawdownPct:         10.0,
		MaxDailyLossPct:        10.0,
		RequireBiasAlignment:   true,
	}
	gate := NewGateWithConfig(cfg, cache, nil)

	sig := domain.Signal{
		Symbol:   "BTC/USDT-PERP",
		Side:     domain.SideSell,
		Strategy: "mean_reversion",
	}

	err := gate.Check(context.Background(), sig, nil)
	if err == nil {
		t.Fatal("expected rejection for SELL signal against Bullish bias")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -v ./internal/risk -run TestGate_RejectsOpposingBiasWhenEnforced`
Expected: FAIL

- [ ] **Step 3: Implement bias enforcement in `internal/risk/gate.go` and update `configs/strategies.yaml`**

In `internal/risk/gate.go`:
```go
	if g.cfg.RequireBiasAlignment && biasBytes != nil {
		var bias domain.BiasResult
		if jerr := json.Unmarshal(biasBytes, &bias); jerr == nil {
			if bias.Score > 0 && sig.Side == domain.SideSell {
				return fmt.Errorf("signal rejected by risk gate: SELL signal opposes Bullish market bias (+%d)", bias.Score)
			}
			if bias.Score < 0 && sig.Side == domain.SideBuy {
				return fmt.Errorf("signal rejected by risk gate: BUY signal opposes Bearish market bias (%d)", bias.Score)
			}
		}
	}
```

In `configs/strategies.yaml`:
Set `require_bias_alignment: true` on `mean_reversion`, `scalping`, and `trend_following`.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -v ./internal/risk -run TestGate_RejectsOpposingBiasWhenEnforced`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/risk/gate.go internal/risk/gate_test.go configs/strategies.yaml
git commit -m "feat(risk): enforce market bias alignment in risk gate and strategies config"
```

---

### Task 4: Fix Economic Calendar Feed 429 with Local In-Memory Caching

**Files:**
- Modify: `internal/ingest/calendar/faireconomy.go:25-125`
- Test: `internal/ingest/calendar/faireconomy_test.go`

**Interfaces:**
- Consumes: `faireconomyURL`
- Produces: In-memory cached `UpcomingEvents(ctx, hours)` with 1-hour TTL, preventing HTTP 429 spam.

- [ ] **Step 1: Write test for caching in `faireconomy_test.go`**

```go
func TestFairEconomyCalendar_CachesEventsAcrossCalls(t *testing.T) {
	requestsCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestsCount++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[{"title":"Fed Chair Speaks","country":"USD","date":"2026-10-19T14:00:00-04:00","impact":"High"}]`))
	}))
	defer srv.Close()

	oldURL := faireconomyURL
	faireconomyURL = srv.URL
	defer func() { faireconomyURL = oldURL }()

	cal := NewFairEconomy()
	_, err1 := cal.UpcomingEvents(context.Background(), 24)
	if err1 != nil {
		t.Fatalf("first call failed: %v", err1)
	}
	_, err2 := cal.UpcomingEvents(context.Background(), 24)
	if err2 != nil {
		t.Fatalf("second call failed: %v", err2)
	}

	if requestsCount != 1 {
		t.Errorf("expected 1 HTTP request due to caching, got %d", requestsCount)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -v ./internal/ingest/calendar -run TestFairEconomyCalendar_CachesEventsAcrossCalls`
Expected: FAIL (requestsCount == 2).

- [ ] **Step 3: Implement in-memory cache in `FairEconomyCalendar`**

In `internal/ingest/calendar/faireconomy.go`:
Add fields to `FairEconomyCalendar`:
```go
type FairEconomyCalendar struct {
	client     *http.Client
	mu         sync.RWMutex
	cached     []domain.EconomicEvent
	cachedAt   time.Time
	cacheTTL   time.Duration
}
```
In `UpcomingEvents`:
1. Check `f.mu.RLock()`: if `time.Since(f.cachedAt) < f.cacheTTL && len(f.cached) > 0`, filter and return `f.cached` without making any HTTP request.
2. Otherwise fetch over HTTP, update `f.cached` and `f.cachedAt`, with `cacheTTL = 1 * time.Hour`.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -v ./internal/ingest/calendar -run TestFairEconomyCalendar_CachesEventsAcrossCalls`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/ingest/calendar/faireconomy.go internal/ingest/calendar/faireconomy_test.go
git commit -m "fix(calendar): cache FairEconomy calendar events to eliminate HTTP 429 rate limits"
```

---

### Task 5: Complete the Closed-Trade Learning Loop on Binance Futures

**Files:**
- Modify: `internal/adapter/binance/futures/rest.go:50-80,780-870`
- Modify: `internal/app/brokers.go:60-120`
- Test: `internal/adapter/binance/futures/rest_test.go`

**Interfaces:**
- Consumes: Binance Futures WebSocket `ORDER_TRADE_UPDATE` event
- Produces: `domain.Trade` persisted via `b.tradeStore.SaveTrade(ctx, trade)` and realized PnL reported to `gate.RecordRealizedPnL`.

- [ ] **Step 1: Write test for `ORDER_TRADE_UPDATE` processing**

```go
func TestFuturesBroker_HandleOrderTradeUpdate_SavesTrade(t *testing.T) {
	mockStore := newMockTradeStore()
	broker := &FuturesBroker{
		tradeStore: mockStore,
		positions:  make(map[domain.Symbol]domain.Position),
	}

	payload := []byte(`{
		"e": "ORDER_TRADE_UPDATE",
		"E": 1729340000000,
		"o": {
			"s": "BTCUSDT",
			"c": "cerebro-123",
			"S": "SELL",
			"o": "MARKET",
			"q": "0.029",
			"ap": "82700.00",
			"X": "FILLED",
			"rp": "-2.50000000"
		}
	}`)

	err := broker.handleUserDataMessage(payload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(mockStore.savedTrades) != 1 {
		t.Fatalf("expected 1 trade saved, got %d", len(mockStore.savedTrades))
	}
	tr := mockStore.savedTrades[0]
	if tr.Symbol != "BTC/USDT-PERP" {
		t.Errorf("expected BTC/USDT-PERP, got %s", tr.Symbol)
	}
	if tr.PnL == nil || !tr.PnL.Equal(decimal.RequireFromString("-2.50000000")) {
		t.Errorf("expected PnL -2.5, got %v", tr.PnL)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -v ./internal/adapter/binance/futures -run TestFuturesBroker_HandleOrderTradeUpdate_SavesTrade`
Expected: FAIL (`ORDER_TRADE_UPDATE` not handled, 0 trades saved).

- [ ] **Step 3: Implement `handleOrderTradeUpdate` in `internal/adapter/binance/futures/rest.go` and wire `tradeStore`**

1. Add `tradeStore port.TradeStore` and `SetTradeStore(s port.TradeStore)` to `FuturesBroker`.
2. In `handleUserDataMessage`:
```go
	switch envelope.Event {
	case "ACCOUNT_UPDATE":
		return b.handleAccountUpdate(message)
	case "ACCOUNT_CONFIG_UPDATE":
		return b.handleAccountConfigUpdate(message)
	case "ORDER_TRADE_UPDATE":
		return b.handleOrderTradeUpdate(message)
	}
```
3. Implement `handleOrderTradeUpdate`:
Parse `o.s` (symbol), `o.S` (side), `o.q` (qty), `o.ap` (avg price), `o.X` (order status), `o.rp` (realized profit).
When `o.X == "FILLED"`:
Construct `domain.Trade`:
```go
	realizedPnL, _ := decimal.NewFromString(o.RealizedProfit)
	trade := domain.Trade{
		ID:        uuid.New().String(),
		Symbol:    sym,
		Side:      domain.Side(strings.ToLower(o.Side)),
		Price:     avgPx,
		Quantity:  qty,
		PnL:       &realizedPnL,
		CreatedAt: time.UnixMilli(evt.EventTime),
	}
	if b.tradeStore != nil {
		_ = b.tradeStore.SaveTrade(context.Background(), trade)
	}
```
4. In `internal/app/brokers.go`: wire `futuresBroker.SetTradeStore(trades)`.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -v ./internal/adapter/binance/futures -run TestFuturesBroker_HandleOrderTradeUpdate_SavesTrade`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/adapter/binance/futures/rest.go internal/adapter/binance/futures/rest_test.go internal/app/brokers.go
git commit -m "feat(futures): record filled trades and realized PnL from ORDER_TRADE_UPDATE"
```

---

### Task 6: Post-Trade Reflection Memory for Continuous Agent Learning

**Files:**
- Modify: `internal/agent/risk_agent.go:270-287`
- Modify: `internal/agent/prompts/risk.tmpl`
- Test: `internal/agent/risk_agent_test.go`

**Interfaces:**
- Consumes: `port.TradeStore.TradesByWindow`
- Produces: Injected "Recent Trade Lessons" formatted into the system prompt prefix so Risk Agent explicitly avoids repeating recent losing setups.

- [ ] **Step 1: Write test for lesson injection**

```go
func TestRiskAgent_InjectPerformanceContext_IncludesRecentLosingLessons(t *testing.T) {
	ra := newTestRiskAgent()
	store := newMockTradeStore()
	loss := decimal.RequireFromString("-15.50")
	store.trades = []domain.Trade{
		{
			Symbol:    "BTC/USDT-PERP",
			Side:      domain.SideSell,
			PnL:       &loss,
			CreatedAt: time.Now().Add(-2 * time.Hour),
		},
	}
	ra.SetTradeStore(store)

	prompt := ra.injectPerformanceContextSystem(context.Background(), 7, "Base Prompt")
	if !strings.Contains(prompt, "Recent Strategy Performance") {
		t.Fatalf("expected performance section in prompt")
	}
	if !strings.Contains(prompt, "BTC/USDT-PERP") {
		t.Errorf("expected recent losing trade symbol to be cited in lessons")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -v ./internal/agent -run TestRiskAgent_InjectPerformanceContext_IncludesRecentLosingLessons`
Expected: FAIL

- [ ] **Step 3: Implement lesson formatting in `injectPerformanceContextSystem`**

In `internal/agent/risk_agent.go`:
Enrich `injectPerformanceContextSystem` to append a `## Lessons from Recent Closed Trades` block listing the last 3 losing trades (symbol, side, realized loss, time) and an explicit instruction:
`"Exercise increased caution and require stronger confirmation before approving signals matching recent loss patterns."`

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -v ./internal/agent -run TestRiskAgent_InjectPerformanceContext_IncludesRecentLosingLessons`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/agent/risk_agent.go internal/agent/prompts/risk.tmpl internal/agent/risk_agent_test.go
git commit -m "feat(agent): inject recent loss lessons into risk agent system prompt for continuous learning"
```

---

## Execution Handoff

Plan complete and saved to `docs/superpowers/plans/2026-10-19-agent-decision-leverage-and-learning.md`. Two execution options:

1. **Subagent-Driven (recommended)** - I dispatch a fresh subagent per task, review between tasks, fast iteration.
2. **Inline Execution** - Execute tasks in this session using executing-plans, batch execution with checkpoints.

Which approach?

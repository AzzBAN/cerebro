package app

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/azhar/cerebro/internal/adapter/binance/futures"
	"github.com/azhar/cerebro/internal/adapter/binance/spot"
	agentpkg "github.com/azhar/cerebro/internal/agent"
	agenttools "github.com/azhar/cerebro/internal/agent/tools"
	"github.com/azhar/cerebro/internal/config"
	"github.com/azhar/cerebro/internal/domain"
	"github.com/azhar/cerebro/internal/execution"
	"github.com/azhar/cerebro/internal/marketdata"
	"github.com/azhar/cerebro/internal/port"
	"github.com/azhar/cerebro/internal/risk"
	"github.com/azhar/cerebro/internal/strategy"
	"github.com/azhar/cerebro/internal/tui"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// ── Strategy registration ─────────────────────────────────────────────────────

func registerStrategies(cfg *config.Config) []strategyCandidate {
	var out []strategyCandidate
	for _, sc := range cfg.Strategies.Strategies {
		if !sc.Enabled {
			continue
		}
		name := strings.ToLower(string(sc.Name))
		switch name {
		case "mean_reversion":
			out = append(out, strategyCandidate{impl: strategy.NewMeanReversion(sc), cfg: sc})
		case "trend_following":
			out = append(out, strategyCandidate{impl: strategy.NewTrendFollowing(sc), cfg: sc})
		case "volatility_breakout":
			out = append(out, strategyCandidate{impl: strategy.NewVolatilityBreakout(sc), cfg: sc})
		default:
			slog.Warn("strategy preset not executable by runtime; skipping",
				"name", sc.Name,
				"hint", "only mean_reversion / trend_following / volatility_breakout are wired")
		}
	}

	if len(out) == 0 {
		slog.Warn("no executable strategies enabled — bot will observe market data but place no orders",
			"hint", "enable mean_reversion, trend_following, or volatility_breakout in strategies.yaml")
	} else {
		names := make([]string, len(out))
		for i, c := range out {
			names[i] = string(c.cfg.Name)
		}
		slog.Info("strategies wired", "count", len(out), "names", names)
	}
	return out
}

type strategyCandidate struct {
	impl port.Strategy
	cfg  config.StrategyConfig
}

// enabledStrategySet returns the set of strategy names that are
// `enabled: true` in strategies.yaml. Used by the discovery planner so
// the matcher only suggests presets the operator has actually opted in
// to (including yaml-only variants like funding_arb / squeeze_fade
// that don't have a Go implementation but can still be advised on).
func enabledStrategySet(cfg config.StrategiesConfig) map[domain.StrategyName]bool {
	out := make(map[domain.StrategyName]bool, len(cfg.Strategies))
	for _, sc := range cfg.Strategies {
		if sc.Enabled {
			out[sc.Name] = true
		}
	}
	return out
}

// runStandalonePlanner ticks the discovery + planner pipeline without
// any LLM dependency. Used in demo / paper mode when no LLM key is
// configured, so the operator still gets Telegram trade-plan reports.
//
// The loop runs once immediately on startup, then every `interval`.
// Cancelling ctx returns cleanly.
func runStandalonePlanner(
	ctx context.Context,
	discovery *agentpkg.Discovery,
	planner *agentpkg.DiscoveryPlanner,
	interval time.Duration,
	ttl time.Duration,
) {
	tick := time.NewTicker(interval)
	defer tick.Stop()

	runOnce := func() {
		cands, err := discovery.Candidates(ctx)
		if err != nil {
			slog.Warn("standalone planner: discovery failed", "error", err)
			return
		}
		plans := planner.Run(ctx, cands, ttl)
		slog.Info("standalone planner: cycle complete",
			"candidates", len(cands), "plans", len(plans))
	}

	runOnce()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			runOnce()
		}
	}
}

// runStrategyEngine evaluates every strategy against every incoming candle,
// routes approved signals through the risk gate, sizes positions, and dispatches
// order intents to the execution router.
func runStrategyEngine(
	ctx context.Context,
	hub *marketdata.Hub,
	strategies []strategyCandidate,
	dedup *strategy.DedupWindow,
	gate *risk.Gate,
	brokers map[domain.Venue]port.Broker,
	env domain.Environment,
	llmCfg config.LLMConfig,
	router *execution.Router,
	symbolMeta map[domain.Symbol]symbolMeta,
	tuiRunner *tui.Runner,
	metrics *runtimeMetrics,
	kc *klineClients,
	markets []config.VenueConfig,
	agentRuntime *agentpkg.Runtime,
	toolReg *agenttools.Registry,
	trades port.TradeStore,
) error {
	// Warm up strategies with historical klines before the live loop starts.
	warmupStrategies(ctx, strategies, kc, markets, symbolMeta)

	_, candles := hub.Subscribe()
	slog.Info("strategy engine started", "strategies", len(strategies))
	for {
		select {
		case <-ctx.Done():
			return nil
		case evt, ok := <-candles:
			if !ok {
				return nil
			}
			metrics.candlesConsumedByStrategy.Add(1)
			c := evt.Candle

			slog.Debug("strategy engine candle",
				"symbol", c.Symbol, "tf", c.Timeframe,
				"close", c.Close.StringFixed(4),
				"candles_total", metrics.candlesConsumedByStrategy.Load(),
			)

			if len(strategies) == 0 {
				continue
			}

			positions := collectPositions(ctx, brokers)

			for _, s := range strategies {
				sig, fired := s.impl.OnCandle(ctx, c)
				if !fired {
					continue
				}
				metrics.signalsFired.Add(1)

				slog.Info("⚡ SIGNAL fired",
					"strategy", sig.Strategy,
					"symbol", sig.Symbol,
					"side", sig.Side,
					"tf", sig.Timeframe,
					"reason", sig.Reason,
				)
				pushTUI(tuiRunner, fmt.Sprintf("⚡ SIGNAL %s %s %s — %s",
					sig.Side, sig.Symbol, sig.Strategy, sig.Reason))

				if !dedup.Allow(sig) {
					metrics.signalsDeduped.Add(1)
					slog.Debug("signal deduped (same symbol+strategy within window)",
						"strategy", sig.Strategy, "symbol", sig.Symbol)
					continue
				}

				if err := gate.Check(ctx, sig, positions); err != nil {
					metrics.signalsRejectedByRisk.Add(1)
					slog.Warn("✗ SIGNAL rejected by risk gate",
						"strategy", sig.Strategy, "symbol", sig.Symbol,
						"side", sig.Side, "reason", err)
					pushTUI(tuiRunner, fmt.Sprintf("✗ RISK-REJECT %s %s — %v",
						sig.Symbol, sig.Strategy, err))
					continue
				}

				// Risk Agent evaluation (optional — only when LLM is wired).
				// When active, the agent handles sizing and routing via its tools.
				// On LLM error, `technical_only_fallback` can opt the signal
				// into the deterministic path below with a reduced size.
				// The deterministic path below is the fallback when no LLM is available.
				techOnlyEngaged := false
				if agentRuntime != nil {
					riskTools := toolReg.ForAgentWithDefs("risk")
					riskAgent := agentpkg.NewRiskAgentWithPerf(agentRuntime, riskTools, trades)
					approved, err := riskAgent.Evaluate(ctx, sig, positions)
					switch {
					case err != nil && llmCfg.TechnicalOnlyFallback:
						metrics.signalsTechOnlyFallback.Add(1)
						techOnlyEngaged = true
						slog.Warn("risk agent failed; falling back to technical-only sizing",
							"strategy", sig.Strategy, "symbol", sig.Symbol,
							"size_multiplier", llmCfg.TechnicalOnlySizeMultiplier,
							"error", err)
						pushTUI(tuiRunner, fmt.Sprintf("TECH-ONLY %s %s — LLM failed, sizing×%.2f",
							sig.Symbol, sig.Strategy, llmCfg.TechnicalOnlySizeMultiplier))
						// fall through to deterministic path
					case err != nil:
						metrics.signalsRejectedByRisk.Add(1)
						slog.Warn("risk agent rejected signal (error)",
							"strategy", sig.Strategy, "symbol", sig.Symbol, "error", err)
						pushTUI(tuiRunner, fmt.Sprintf("RISK-AGENT-ERROR %s %s: %v",
							sig.Symbol, sig.Strategy, err))
						continue
					case !approved:
						metrics.signalsRejectedByRisk.Add(1)
						slog.Info("risk agent rejected signal",
							"strategy", sig.Strategy, "symbol", sig.Symbol)
						pushTUI(tuiRunner, fmt.Sprintf("RISK-AGENT-REJECT %s %s",
							sig.Symbol, sig.Strategy))
						continue
					default:
						slog.Info("risk agent approved signal",
							"strategy", sig.Strategy, "symbol", sig.Symbol)
						continue // LLM tools handled routing
					}
				}

				// Deterministic path: runs when no LLM is wired OR when
				// technical-only fallback engaged above.
				meta, ok := symbolMeta[sig.Symbol]
				if !ok {
					metrics.orderRouteErrors.Add(1)
					slog.Error("order route failed: symbol not configured",
						"symbol", sig.Symbol, "strategy", sig.Strategy)
					continue
				}
				qty := computeQuantity(sig, c.Close, s.cfg, symbolMeta)
				if techOnlyEngaged {
					qty = applyTechOnlyMultiplier(qty, llmCfg)
				}

				// Derive protective SL / TP levels from the strategy config
				// so the broker can attach a server-side bracket after
				// entry. The same SL value is used for sizing, so the pair
				// is consistent by construction.
				entryPrice := c.Close
				sl := deriveStopLoss(sig.Side, entryPrice, s.cfg)
				tp1 := deriveFirstTakeProfit(sig.Side, entryPrice, sl, s.cfg)
				scaleOutPct := firstTPScaleOutPct(s.cfg)

				orderType, limitPx, stopPx := deriveEntryOrder(sig.Side, entryPrice, s.cfg, meta)
				tif := s.cfg.TimeInForce
				if tif == "" {
					tif = domain.TIFGTC
				}

				intent := domain.OrderIntent{
					ID:            uuid.New().String(),
					CorrelationID: sig.CorrelationID,
					Symbol:        sig.Symbol,
					Venue:         meta.venue,
					Side:          sig.Side,
					OrderType:     orderType,
					Quantity:      qty,
					LimitPrice:    limitPx,
					StopPrice:     stopPx,
					TIF:           tif,
					StopLoss:      sl,
					TakeProfit1:   tp1,
					ScaleOutPct:   scaleOutPct,
					Strategy:      sig.Strategy,
					Environment:   env,
					CreatedAt:     time.Now().UTC(),
				}
				// Futures-only fields: carry leverage/positionSide through
				// from the symbol config when the venue is futures.
				if meta.venue == domain.VenueBinanceFutures {
					intent.Leverage = meta.cfg.Leverage
					// PositionSide left as zero (BOTH / one-way mode);
					// hedge-mode support is a future extension.
				}
				resp, err := router.Route(ctx, intent, meta.venue)
				if err != nil {
					metrics.orderRouteErrors.Add(1)
					slog.Error("order route failed",
						"symbol", sig.Symbol, "strategy", sig.Strategy, "error", err)
					continue
				}

				metrics.ordersRouted.Add(1)
				slog.Info("order routed",
					"strategy", sig.Strategy,
					"symbol", sig.Symbol,
					"venue", meta.venue,
					"side", sig.Side,
					"qty", qty.String(),
					"broker_id", resp.BrokerOrderID,
				)
				pushTUIOrder(tuiRunner, fmt.Sprintf("ORDER %s %s %.6f @ %.4f [%s] {%s}",
					sig.Side, sig.Symbol,
					qty.InexactFloat64(), c.Close.InexactFloat64(),
					sig.Strategy, meta.venue))
			}
		}
	}
}

// warmupStrategies fetches historical klines from Binance REST and feeds them
// into each strategy to prime indicators. In paper mode (kc == nil), warmup is
// skipped — the synthetic feeder produces candles fast enough.
func warmupStrategies(
	ctx context.Context,
	strategies []strategyCandidate,
	kc *klineClients,
	markets []config.VenueConfig,
	symbolMeta map[domain.Symbol]symbolMeta,
) {
	if kc == nil || len(strategies) == 0 {
		return
	}

	// Determine the max warmup needed per (symbol, timeframe, venue) triple.
	type warmupKey struct {
		symbol domain.Symbol
		tf     domain.Timeframe
		venue  domain.Venue
	}
	maxWarmup := make(map[warmupKey]int)
	for _, s := range strategies {
		for _, sym := range s.impl.Symbols() {
			for _, tf := range s.impl.Timeframes() {
				meta, ok := symbolMeta[sym]
				if !ok {
					continue
				}
				key := warmupKey{symbol: sym, tf: tf, venue: meta.venue}
				if s.cfg.WarmupCandles > maxWarmup[key] {
					maxWarmup[key] = s.cfg.WarmupCandles
				}
			}
		}
	}

	// Fetch historical klines for each unique (symbol, timeframe, venue).
	historicalCandles := make(map[warmupKey][]domain.Candle)
	for key, limit := range maxWarmup {
		if limit <= 0 {
			continue
		}
		var candles []domain.Candle
		var err error

		switch key.venue {
		case domain.VenueBinanceSpot:
			if kc.spotClient != nil {
				candles, err = spot.FetchKlines(ctx, kc.spotClient, key.symbol, key.tf, limit)
			}
		case domain.VenueBinanceFutures:
			if kc.futuresClient != nil {
				candles, err = futures.FetchKlines(ctx, kc.futuresClient, key.symbol, key.tf, limit)
			}
		}

		if err != nil {
			slog.Warn("strategy warmup: failed to fetch historical klines",
				"symbol", key.symbol, "tf", key.tf, "venue", key.venue, "error", err)
			continue
		}

		historicalCandles[key] = candles
		slog.Info("strategy warmup: fetched historical klines",
			"symbol", key.symbol, "tf", key.tf, "venue", key.venue,
			"fetched", len(candles), "requested", limit)
	}

	// Feed historical candles into each strategy's Warmup method.
	for _, s := range strategies {
		for _, sym := range s.impl.Symbols() {
			for _, tf := range s.impl.Timeframes() {
				meta, ok := symbolMeta[sym]
				if !ok {
					continue
				}
				key := warmupKey{symbol: sym, tf: tf, venue: meta.venue}
				candles := historicalCandles[key]
				if len(candles) == 0 {
					continue
				}
				s.impl.Warmup(ctx, candles)
				slog.Info("strategy warmup: indicators primed",
					"strategy", s.cfg.Name, "symbol", sym, "tf", tf,
					"candles_fed", len(candles))
			}
		}
	}
}

// ── Position sizing ───────────────────────────────────────────────────────────

func computeQuantity(
	sig domain.Signal,
	entryPrice decimal.Decimal,
	sc config.StrategyConfig,
	symbolMeta map[domain.Symbol]symbolMeta,
) decimal.Decimal {
	meta, ok := symbolMeta[sig.Symbol]
	if !ok {
		slog.Debug("symbol not in market config; using fallback quantity",
			"symbol", sig.Symbol)
		return decimal.NewFromFloat(0.001)
	}
	mkt := meta.cfg

	sl := deriveStopLoss(sig.Side, entryPrice, sc)
	riskPct := sc.RiskPctPerTrade
	if riskPct <= 0 {
		riskPct = 0.5
	}

	params, err := risk.CalculatePositionSize(
		decimal.NewFromFloat(paperStartingEquity),
		riskPct,
		entryPrice,
		sl,
		mkt.MinLotUnits,
		mkt.MaxLotUnits,
		mkt.MinNotional,
	)
	if err != nil {
		slog.Debug("position sizing failed; falling back to min lot",
			"symbol", sig.Symbol, "error", err)
		if !mkt.MinLotUnits.IsZero() {
			return roundToLotSize(mkt.MinLotUnits, mkt.LotSize)
		}
		return decimal.NewFromFloat(0.001)
	}

	qty := params.Quantity

	// Round down to the symbol's lot size step so the exchange doesn't reject.
	if !mkt.LotSize.IsZero() {
		qty = roundToLotSize(qty, mkt.LotSize)
	}

	// If rounding brought us below minLot, bump up to minLot.
	if !mkt.MinLotUnits.IsZero() && qty.LessThan(mkt.MinLotUnits) {
		qty = roundToLotSize(mkt.MinLotUnits, mkt.LotSize)
	}

	slog.Debug("position sized",
		"symbol", sig.Symbol, "side", sig.Side,
		"entry", entryPrice.StringFixed(4),
		"stop_loss", sl.StringFixed(4),
		"qty", qty.String(),
		"risk_usd", params.RiskAmountQuote.StringFixed(2),
	)
	return qty
}

// roundToLotSize rounds qty down to the nearest step increment.
// If step is zero, qty is returned unchanged.
func roundToLotSize(qty, step decimal.Decimal) decimal.Decimal {
	if step.IsZero() {
		return qty
	}
	return qty.Div(step).Floor().Mul(step)
}

// applyTechOnlyMultiplier shrinks a position size when the LLM risk agent
// failed and technical-only fallback is active. The multiplier is applied
// only when it's a sensible reduction (0 < m < 1). Values <=0 or >=1 are
// treated as "no adjustment" so that a misconfigured or unset multiplier
// doesn't silently zero out the trade or amplify it.
func applyTechOnlyMultiplier(qty decimal.Decimal, llmCfg config.LLMConfig) decimal.Decimal {
	if !llmCfg.TechnicalOnlyFallback {
		return qty
	}
	m := llmCfg.TechnicalOnlySizeMultiplier
	if m <= 0 || m >= 1 {
		return qty
	}
	return qty.Mul(decimal.NewFromFloat(m))
}

// deriveFirstTakeProfit computes the TP1 price from the strategy's first
// TakeProfitLevel. TP is defined as a multiple of the SL distance (R:R
// ratio), so risk is always quantised against the protective stop and the
// paper/live bracket agrees with the sizing math.
//
// Returns zero when TP levels are not configured, which disables the TP leg
// of the broker bracket.
func deriveFirstTakeProfit(side domain.Side, entry, stop decimal.Decimal, sc config.StrategyConfig) decimal.Decimal {
	if len(sc.TakeProfitLevels) == 0 {
		return decimal.Zero
	}
	rr := sc.TakeProfitLevels[0].RRRatio
	if rr <= 0 {
		return decimal.Zero
	}
	if stop.IsZero() || entry.IsZero() {
		return decimal.Zero
	}
	slDist := entry.Sub(stop).Abs()
	tpDist := slDist.Mul(decimal.NewFromFloat(rr))
	if side == domain.SideBuy {
		return entry.Add(tpDist)
	}
	return entry.Sub(tpDist)
}

// firstTPScaleOutPct returns the fractional scale-out for TP1 (0–1 range).
// A zero value means "close the full position at TP1". This drives the
// client-side Monitor's partial-close behaviour; the server bracket always
// closes the full position.
func firstTPScaleOutPct(sc config.StrategyConfig) float64 {
	if len(sc.TakeProfitLevels) == 0 {
		return 0
	}
	pct := sc.TakeProfitLevels[0].ScaleOutPct
	if pct < 0 {
		return 0
	}
	if pct > 100 {
		return 1
	}
	return pct / 100
}

// deriveEntryOrder picks the entry OrderType, LimitPrice and StopPrice for
// the intent based on the strategy configuration. When OrderType is LIMIT
// or STOP_LIMIT, we use meta.cfg.TickSize and sc.LimitOffsetPips to push
// the limit slightly inside the last close to improve fill probability.
//
// Unknown or zero OrderType defaults to MARKET, which matches the previous
// behaviour and is safe for strategies that have not yet adopted the new
// config keys.
func deriveEntryOrder(side domain.Side, close decimal.Decimal, sc config.StrategyConfig, meta symbolMeta) (domain.OrderType, decimal.Decimal, decimal.Decimal) {
	switch sc.OrderType {
	case domain.OrderTypeLimit:
		return domain.OrderTypeLimit, limitPriceWithOffset(side, close, sc, meta), decimal.Zero
	case domain.OrderTypeStopLimit:
		// Stop-limit breakouts trigger above (buy) or below (sell) the
		// current close. We use the configured offset as the trigger
		// distance, and set the limit price equal to the trigger (price
		// protection happens via the exchange's STOP logic).
		trigger := stopPriceWithOffset(side, close, sc, meta)
		limit := trigger
		return domain.OrderTypeStopLimit, limit, trigger
	default:
		return domain.OrderTypeMarket, decimal.Zero, decimal.Zero
	}
}

// limitPriceWithOffset offsets the entry limit by sc.LimitOffsetPips in the
// direction that improves fill probability for the given side. For BUYs
// this means bumping the limit up (we're willing to pay more than the last
// close); for SELLs it means lowering the limit.
//
// "Pips" here is interpreted as tickSize units since our markets config
// exposes tickSize, not a fixed $0.0001 convention.
func limitPriceWithOffset(side domain.Side, close decimal.Decimal, sc config.StrategyConfig, meta symbolMeta) decimal.Decimal {
	offsetTicks := decimal.NewFromFloat(sc.LimitOffsetPips)
	tick := meta.cfg.TickSize
	if tick.IsZero() {
		tick = decimal.NewFromFloat(0.01)
	}
	delta := offsetTicks.Mul(tick)
	if side == domain.SideBuy {
		return close.Add(delta)
	}
	return close.Sub(delta)
}

// stopPriceWithOffset mirrors limitPriceWithOffset for stop triggers: buy
// triggers above close, sell triggers below.
func stopPriceWithOffset(side domain.Side, close decimal.Decimal, sc config.StrategyConfig, meta symbolMeta) decimal.Decimal {
	return limitPriceWithOffset(side, close, sc, meta)
}

func deriveStopLoss(side domain.Side, entry decimal.Decimal, sc config.StrategyConfig) decimal.Decimal {
	isBuy := side == domain.SideBuy

	switch sc.StopLoss.Type {
	case domain.SLTypeFixedPct:
		pct := sc.StopLoss.FixedPct
		if pct <= 0 {
			pct = 0.5
		}
		factor := decimal.NewFromFloat(pct / 100)
		if isBuy {
			return entry.Mul(decimal.NewFromFloat(1).Sub(factor))
		}
		return entry.Mul(decimal.NewFromFloat(1).Add(factor))

	case domain.SLTypeFixedPips:
		dist := decimal.NewFromFloat(sc.StopLoss.FixedPips)
		if dist.IsZero() {
			dist = decimal.NewFromFloat(10)
		}
		if isBuy {
			return entry.Sub(dist)
		}
		return entry.Add(dist)

	default:
		// ATR-based SL needs live indicator data; fall back to 0.5% in paper mode.
		factor := decimal.NewFromFloat(0.005)
		if isBuy {
			return entry.Mul(decimal.NewFromFloat(1).Sub(factor))
		}
		return entry.Mul(decimal.NewFromFloat(1).Add(factor))
	}
}

// buildFlipEntryFn returns the EntryFn used by the position-manager flip path.
// It opens a new gated, strategy-sized position in the requested direction
// (the opposite of the position just closed). The full risk gate runs first;
// a gate rejection returns an error so the executor leaves the position flat
// rather than reversed-but-naked. Sizing uses a conservative default strategy
// config (risk-% based), honouring the user's "strategy-sized flips" choice.
func buildFlipEntryFn(
	gate *risk.Gate,
	router *execution.Router,
	brokers map[domain.Venue]port.Broker,
	symbolMeta map[domain.Symbol]symbolMeta,
	env domain.Environment,
) func(ctx context.Context, want domain.Position) error {
	return func(ctx context.Context, want domain.Position) error {
		meta, resolved, err := resolveRouteSymbol(want.Symbol, symbolMeta)
		if err != nil {
			return err
		}

		sig := domain.Signal{
			CorrelationID: uuid.New().String(),
			Strategy:      domain.StrategyName("position_manager_flip"),
			Symbol:        resolved,
			Side:          want.Side,
			GeneratedAt:   time.Now().UTC(),
		}
		positions := collectPositions(ctx, brokers)
		if gerr := gate.Check(ctx, sig, positions); gerr != nil {
			return fmt.Errorf("flip entry risk gate: %w", gerr)
		}

		entry := want.CurrentPrice
		if entry.IsZero() {
			entry = want.EntryPrice
		}
		flipCfg := defaultFlipStrategyCfg()
		qty := computeQuantity(sig, entry, flipCfg, symbolMeta)
		sl := deriveStopLoss(want.Side, entry, flipCfg)
		tp1 := deriveFirstTakeProfit(want.Side, entry, sl, flipCfg)

		intent := domain.OrderIntent{
			ID:            uuid.New().String(),
			CorrelationID: sig.CorrelationID,
			Symbol:        resolved,
			Venue:         meta.venue,
			Side:          want.Side,
			OrderType:     domain.OrderTypeMarket,
			Quantity:      qty,
			StopLoss:      sl,
			TakeProfit1:   tp1,
			Strategy:      sig.Strategy,
			Environment:   env,
			CreatedAt:     time.Now().UTC(),
		}
		if meta.venue == domain.VenueBinanceFutures {
			intent.Leverage = meta.cfg.Leverage
		}
		if _, err := router.Route(ctx, intent, meta.venue); err != nil {
			return fmt.Errorf("flip entry route: %w", err)
		}
		return nil
	}
}

// defaultFlipStrategyCfg returns conservative SL/TP/risk defaults for flip
// re-entries when the originating strategy config is not in scope. 0.5% fixed
// stop, 1.5 R:R take-profit, 0.5% account risk per trade.
func defaultFlipStrategyCfg() config.StrategyConfig {
	return config.StrategyConfig{
		RiskPctPerTrade:  0.5,
		StopLoss:         config.StopLossConfig{Type: domain.SLTypeFixedPct, FixedPct: 0.5},
		TakeProfitLevels: []config.TPLevel{{RRRatio: 1.5, ScaleOutPct: 100}},
	}
}

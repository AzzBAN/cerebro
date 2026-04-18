package app

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	agentpkg "github.com/azhar/cerebro/internal/agent"
	agenttools "github.com/azhar/cerebro/internal/agent/tools"
	"github.com/azhar/cerebro/internal/adapter/binance/futures"
	"github.com/azhar/cerebro/internal/adapter/binance/spot"
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
				// The deterministic path below is the fallback when no LLM is available.
				if agentRuntime != nil {
					riskTools := toolReg.ForAgentWithDefs("risk")
					riskAgent := agentpkg.NewRiskAgentWithPerf(agentRuntime, riskTools, trades)
					approved, err := riskAgent.Evaluate(ctx, sig, positions)
					if err != nil {
						metrics.signalsRejectedByRisk.Add(1)
						slog.Warn("risk agent rejected signal (error)",
							"strategy", sig.Strategy, "symbol", sig.Symbol, "error", err)
						pushTUI(tuiRunner, fmt.Sprintf("RISK-AGENT-ERROR %s %s: %v",
							sig.Symbol, sig.Strategy, err))
						continue
					}
					if !approved {
						metrics.signalsRejectedByRisk.Add(1)
						slog.Info("risk agent rejected signal",
							"strategy", sig.Strategy, "symbol", sig.Symbol)
						pushTUI(tuiRunner, fmt.Sprintf("RISK-AGENT-REJECT %s %s",
							sig.Symbol, sig.Strategy))
						continue
					}
					slog.Info("risk agent approved signal",
						"strategy", sig.Strategy, "symbol", sig.Symbol)
					continue
				}

				// Deterministic fallback: no LLM available.
				meta, ok := symbolMeta[sig.Symbol]
				if !ok {
					metrics.orderRouteErrors.Add(1)
					slog.Error("order route failed: symbol not configured",
						"symbol", sig.Symbol, "strategy", sig.Strategy)
					continue
				}
				qty := computeQuantity(sig, c.Close, s.cfg, symbolMeta)

				intent := domain.OrderIntent{
					ID:            uuid.New().String(),
					CorrelationID: sig.CorrelationID,
					Symbol:        sig.Symbol,
					Venue:         meta.venue,
					Side:          sig.Side,
					Quantity:      qty,
					Strategy:      sig.Strategy,
					Environment:   env,
					CreatedAt:     time.Now().UTC(),
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
			return mkt.MinLotUnits
		}
		return decimal.NewFromFloat(0.001)
	}
	slog.Debug("position sized",
		"symbol", sig.Symbol, "side", sig.Side,
		"entry", entryPrice.StringFixed(4),
		"stop_loss", sl.StringFixed(4),
		"qty", params.Quantity.String(),
		"risk_usd", params.RiskAmountQuote.StringFixed(2),
	)
	return params.Quantity
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

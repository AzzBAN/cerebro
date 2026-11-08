package strategy

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/azhar/cerebro/internal/config"
	"github.com/azhar/cerebro/internal/domain"
	"github.com/azhar/cerebro/internal/strategy/indicators"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// Scalping implements a high-frequency scalp strategy optimized for
// 1m/5m timeframes with tight stops and quick profit targets.
// Designed for high-leverage futures trading (20x-50x) where small
// price movements (0.1-0.3%) are amplified into meaningful profits.
//
// Entry Logic:
//   - Fast EMA (8) crosses above/below Slow EMA (21) for momentum confirmation
//   - RSI(7) not overbought/oversold (avoid exhaustion entries)
//   - Price above/below trend EMA(50) for directional bias
//   - ATR-based minimum volatility gate (no scalping in dead markets)
//
// Exit Logic:
//   - Tight stop loss: 0.3% or 1.0x ATR (whichever is tighter)
//   - Quick take profit: 0.5-0.8% or 1.5-2.0x ATR
//   - High win rate (60%+) with small profits accumulating over many trades
//
// Risk Management:
//   - Small risk per trade: 0.25-0.5% of account
//   - Max position size: 2-3% of account (leverage does the heavy lifting)
//   - Dedup window: 60s (1 minute) to avoid overtrading the same signal
type Scalping struct {
	cfg     config.StrategyConfig
	symbols []domain.Symbol

	// Per-symbol indicator state (fast parameters for scalping).
	fastEMA  map[domain.Symbol]*indicators.EMA
	slowEMA  map[domain.Symbol]*indicators.EMA
	trendEMA map[domain.Symbol]*indicators.EMA
	rsi      map[domain.Symbol]*indicators.RSI
	atr      map[domain.Symbol]*indicators.ATR
	warmup   map[domain.Symbol]int

	// Track previous EMA cross state to detect transitions.
	prevFastAboveSlow map[domain.Symbol]bool
}

// NewScalping creates a Scalping strategy from config.
func NewScalping(cfg config.StrategyConfig) *Scalping {
	syms := make([]domain.Symbol, len(cfg.Markets))
	for i, m := range cfg.Markets {
		syms[i] = domain.Symbol(m)
	}
	s := &Scalping{
		cfg:               cfg,
		symbols:           syms,
		fastEMA:           make(map[domain.Symbol]*indicators.EMA),
		slowEMA:           make(map[domain.Symbol]*indicators.EMA),
		trendEMA:          make(map[domain.Symbol]*indicators.EMA),
		rsi:               make(map[domain.Symbol]*indicators.RSI),
		atr:               make(map[domain.Symbol]*indicators.ATR),
		warmup:            make(map[domain.Symbol]int),
		prevFastAboveSlow: make(map[domain.Symbol]bool),
	}
	for _, sym := range syms {
		s.fastEMA[sym] = indicators.NewEMA(cfg.Indicators.EMA.Fast)
		s.slowEMA[sym] = indicators.NewEMA(cfg.Indicators.EMA.Slow)
		s.trendEMA[sym] = indicators.NewEMA(cfg.Indicators.EMA.Trend)
		s.rsi[sym] = indicators.NewRSI(cfg.Indicators.RSI.Period)
		s.atr[sym] = indicators.NewATR(cfg.Indicators.ATR.Period)
	}
	return s
}

func (s *Scalping) Name() domain.StrategyName { return s.cfg.Name }
func (s *Scalping) Symbols() []domain.Symbol  { return s.symbols }
func (s *Scalping) Timeframes() []domain.Timeframe {
	return []domain.Timeframe{s.cfg.PrimaryTimeframe}
}

// OnCandle evaluates one closed candle and emits a scalp signal when conditions align.
func (s *Scalping) OnCandle(_ context.Context, c domain.Candle) (domain.Signal, bool) {
	slog.Info("scalping: evaluating candle",
		"symbol", c.Symbol,
		"timeframe", c.Timeframe,
		"close", c.Close.StringFixed(6),
		"primary_timeframe", s.cfg.PrimaryTimeframe)

	if c.Timeframe != s.cfg.PrimaryTimeframe {
		slog.Info("scalping: REJECTED - wrong timeframe",
			"symbol", c.Symbol,
			"candle_timeframe", c.Timeframe,
			"required_timeframe", s.cfg.PrimaryTimeframe)
		return domain.Signal{}, false
	}
	if !s.isTargetSymbol(c.Symbol) {
		slog.Info("scalping: REJECTED - not target symbol",
			"symbol", c.Symbol,
			"target_symbols", s.symbols)
		return domain.Signal{}, false
	}

	// Update indicators.
	fast := s.fastEMA[c.Symbol]
	slow := s.slowEMA[c.Symbol]
	trend := s.trendEMA[c.Symbol]
	rsi := s.rsi[c.Symbol]
	atr := s.atr[c.Symbol]

	fast.Add(c.Close)
	slow.Add(c.Close)
	trend.Add(c.Close)
	rsi.Add(c.Close)
	atr.Add(c)

	fastVal, fastOK := fast.Value()
	slowVal, slowOK := slow.Value()

	s.warmup[c.Symbol]++
	if s.warmup[c.Symbol] < s.cfg.WarmupCandles {
		if fastOK && slowOK {
			s.prevFastAboveSlow[c.Symbol] = fastVal.GreaterThan(slowVal)
		}
		slog.Info("scalping: REJECTED - warmup period",
			"symbol", c.Symbol,
			"warmup_count", s.warmup[c.Symbol],
			"warmup_required", s.cfg.WarmupCandles)
		return domain.Signal{}, false
	}

	// Session filter: scalping works best during high-volume sessions.
	if !s.inSession(c.CloseTime) {
		slog.Info("scalping: REJECTED - outside session",
			"symbol", c.Symbol,
			"close_time", c.CloseTime,
			"session_filter", s.cfg.SessionFilter)
		return domain.Signal{}, false
	}

	// Require all indicators to be ready.
	trendVal, trendOK := trend.Value()
	rsiVal, rsiOK := rsi.Value()
	atrVal, atrOK := atr.Value()

	if !fastOK || !slowOK || !trendOK || !rsiOK || !atrOK {
		slog.Info("scalping: REJECTED - indicators not ready",
			"symbol", c.Symbol,
			"fast_ok", fastOK,
			"slow_ok", slowOK,
			"trend_ok", trendOK,
			"rsi_ok", rsiOK,
			"atr_ok", atrOK)
		return domain.Signal{}, false
	}

	slog.Info("scalping: indicator values",
		"symbol", c.Symbol,
		"ema_fast", fastVal.StringFixed(6),
		"ema_slow", slowVal.StringFixed(6),
		"ema_trend", trendVal.StringFixed(6),
		"rsi", rsiVal.StringFixed(2),
		"atr", atrVal.StringFixed(6))

	// Minimum ATR threshold: avoid scalping in dead markets (0.05% of price).
	minATR := c.Close.Mul(decimal.NewFromFloat(0.0005))
	if atrVal.LessThan(minATR) {
		slog.Info("scalping: REJECTED - ATR too low",
			"symbol", c.Symbol,
			"atr", atrVal.StringFixed(6),
			"min_atr", minATR.StringFixed(6))
		return domain.Signal{}, false
	}

	// Detect EMA crossover transition.
	fastAboveSlow := fastVal.GreaterThan(slowVal)
	prev := s.prevFastAboveSlow[c.Symbol]
	s.prevFastAboveSlow[c.Symbol] = fastAboveSlow

	slog.Info("scalping: crossover check",
		"symbol", c.Symbol,
		"fast_above_slow", fastAboveSlow,
		"prev_fast_above_slow", prev,
		"crossover_up", !prev && fastAboveSlow,
		"crossover_down", prev && !fastAboveSlow)

	// ── BUY signal: Golden cross + bullish trend + RSI in sweet spot ────────
	if !prev && fastAboveSlow {
		slog.Info("scalping: detected golden cross, checking conditions",
			"symbol", c.Symbol,
			"close", c.Close.StringFixed(6),
			"trend_ema", trendVal.StringFixed(6),
			"rsi", rsiVal.StringFixed(2))

		// Price must be above trend EMA (bullish bias).
		if c.Close.LessThanOrEqual(trendVal) {
			slog.Info("scalping: REJECTED - BUY but price below trend",
				"symbol", c.Symbol,
				"close", c.Close.StringFixed(6),
				"trend_ema", trendVal.StringFixed(6))
			return domain.Signal{}, false
		}
		// RSI not overbought (avoid buying into exhaustion).
		if rsiVal.GreaterThanOrEqual(decimal.NewFromFloat(float64(s.cfg.Indicators.RSI.Overbought))) {
			slog.Info("scalping: REJECTED - BUY but RSI overbought",
				"symbol", c.Symbol,
				"rsi", rsiVal.StringFixed(2),
				"overbought", s.cfg.Indicators.RSI.Overbought)
			return domain.Signal{}, false
		}
		// RSI above 50 (bullish momentum confirmation).
		if rsiVal.LessThan(decimal.NewFromFloat(50)) {
			slog.Info("scalping: REJECTED - BUY but RSI below 50",
				"symbol", c.Symbol,
				"rsi", rsiVal.StringFixed(2))
			return domain.Signal{}, false
		}
		slog.Info("scalping: ✓ BUY signal generated",
			"symbol", c.Symbol,
			"close", c.Close.StringFixed(6),
			"rsi", rsiVal.StringFixed(2),
			"atr", atrVal.StringFixed(6))
		return s.newSignal(c, domain.SideBuy,
			fmt.Sprintf("scalp long: EMA%d/%d golden cross, RSI=%.1f, ATR=%.4f",
				s.cfg.Indicators.EMA.Fast, s.cfg.Indicators.EMA.Slow,
				rsiVal.InexactFloat64(), atrVal.InexactFloat64()),
		), true
	}

	// ── SELL signal: Death cross + bearish trend + RSI in sweet spot ────────
	if prev && !fastAboveSlow {
		slog.Info("scalping: detected death cross, checking conditions",
			"symbol", c.Symbol,
			"close", c.Close.StringFixed(6),
			"trend_ema", trendVal.StringFixed(6),
			"rsi", rsiVal.StringFixed(2))

		// Price must be below trend EMA (bearish bias).
		if c.Close.GreaterThanOrEqual(trendVal) {
			slog.Info("scalping: REJECTED - SELL but price above trend",
				"symbol", c.Symbol,
				"close", c.Close.StringFixed(6),
				"trend_ema", trendVal.StringFixed(6))
			return domain.Signal{}, false
		}
		// RSI not oversold (avoid selling into exhaustion).
		if rsiVal.LessThanOrEqual(decimal.NewFromFloat(float64(s.cfg.Indicators.RSI.Oversold))) {
			slog.Info("scalping: REJECTED - SELL but RSI oversold",
				"symbol", c.Symbol,
				"rsi", rsiVal.StringFixed(2),
				"oversold", s.cfg.Indicators.RSI.Oversold)
			return domain.Signal{}, false
		}
		// RSI below 50 (bearish momentum confirmation).
		if rsiVal.GreaterThan(decimal.NewFromFloat(50)) {
			slog.Info("scalping: REJECTED - SELL but RSI above 50",
				"symbol", c.Symbol,
				"rsi", rsiVal.StringFixed(2))
			return domain.Signal{}, false
		}
		slog.Info("scalping: ✓ SELL signal generated",
			"symbol", c.Symbol,
			"close", c.Close.StringFixed(6),
			"rsi", rsiVal.StringFixed(2),
			"atr", atrVal.StringFixed(6))
		return s.newSignal(c, domain.SideSell,
			fmt.Sprintf("scalp short: EMA%d/%d death cross, RSI=%.1f, ATR=%.4f",
				s.cfg.Indicators.EMA.Fast, s.cfg.Indicators.EMA.Slow,
				rsiVal.InexactFloat64(), atrVal.InexactFloat64()),
		), true
	}

	return domain.Signal{}, false
}

func (s *Scalping) newSignal(c domain.Candle, side domain.Side, reason string) domain.Signal {
	id := uuid.New().String()
	genAt := c.CloseTime
	if genAt.IsZero() {
		genAt = time.Now().UTC()
	}
	return domain.Signal{
		ID:            id,
		CorrelationID: id,
		Strategy:      s.cfg.Name,
		Symbol:        c.Symbol,
		Side:          side,
		Timeframe:     c.Timeframe,
		Reason:        reason,
		GeneratedAt:   genAt,
	}
}

// Warmup feeds historical candles through OnCandle to prime indicators.
// Signals produced during warmup are discarded by the engine.
func (s *Scalping) Warmup(_ context.Context, candles []domain.Candle) {
	for _, c := range candles {
		s.OnCandle(context.Background(), c)
	}
}

func (s *Scalping) isTargetSymbol(sym domain.Symbol) bool {
	for _, target := range s.symbols {
		if target == sym {
			return true
		}
	}
	return false
}

func (s *Scalping) inSession(t time.Time) bool {
	if s.cfg.SessionFilter == domain.SessionAll || s.cfg.SessionFilter == "" {
		return true
	}
	win := domain.SessionWindowFor(s.cfg.SessionFilter)
	return win != nil && win.Contains(t)
}

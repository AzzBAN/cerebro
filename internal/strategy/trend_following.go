package strategy

import (
	"context"
	"fmt"
	"time"

	"github.com/azhar/cerebro/internal/config"
	"github.com/azhar/cerebro/internal/domain"
	"github.com/azhar/cerebro/internal/strategy/indicators"
	"github.com/google/uuid"
)

// TrendFollowing fires signals on EMA 50/200 golden cross / death cross patterns,
// with multi-timeframe trend confirmation. PRD §4.2.
type TrendFollowing struct {
	cfg     config.StrategyConfig
	symbols []domain.Symbol

	fastEMA  map[domain.Symbol]*indicators.EMA
	slowEMA  map[domain.Symbol]*indicators.EMA
	trendEMA map[domain.Symbol]*indicators.EMA
	atr      map[domain.Symbol]*indicators.ATR
	warmup   map[domain.Symbol]int

	// Track previous cross state to detect transitions.
	prevFastAboveSlow map[domain.Symbol]bool
}

// NewTrendFollowing creates a TrendFollowing strategy from config.
func NewTrendFollowing(cfg config.StrategyConfig) *TrendFollowing {
	syms := make([]domain.Symbol, len(cfg.Markets))
	for i, m := range cfg.Markets {
		syms[i] = domain.Symbol(m)
	}
	s := &TrendFollowing{
		cfg:               cfg,
		symbols:           syms,
		fastEMA:           make(map[domain.Symbol]*indicators.EMA),
		slowEMA:           make(map[domain.Symbol]*indicators.EMA),
		trendEMA:          make(map[domain.Symbol]*indicators.EMA),
		atr:               make(map[domain.Symbol]*indicators.ATR),
		warmup:            make(map[domain.Symbol]int),
		prevFastAboveSlow: make(map[domain.Symbol]bool),
	}
	for _, sym := range syms {
		s.fastEMA[sym] = indicators.NewEMA(cfg.Indicators.EMA.Fast)
		s.slowEMA[sym] = indicators.NewEMA(cfg.Indicators.EMA.Slow)
		s.trendEMA[sym] = indicators.NewEMA(cfg.Indicators.EMA.LongTrend)
		s.atr[sym] = indicators.NewATR(cfg.Indicators.ATR.Period)
	}
	return s
}

func (t *TrendFollowing) Name() domain.StrategyName  { return t.cfg.Name }
func (t *TrendFollowing) Symbols() []domain.Symbol    { return t.symbols }
func (t *TrendFollowing) Timeframes() []domain.Timeframe {
	return []domain.Timeframe{t.cfg.PrimaryTimeframe}
}

// OnCandle evaluates each closed candle for EMA cross signals.
func (t *TrendFollowing) OnCandle(_ context.Context, c domain.Candle) (domain.Signal, bool) {
	if c.Timeframe != t.cfg.PrimaryTimeframe {
		return domain.Signal{}, false
	}
	if !t.isTargetSymbol(c.Symbol) {
		return domain.Signal{}, false
	}

	fast := t.fastEMA[c.Symbol]
	slow := t.slowEMA[c.Symbol]
	trend := t.trendEMA[c.Symbol]
	atr := t.atr[c.Symbol]

	fast.Add(c.Close)
	slow.Add(c.Close)
	trend.Add(c.Close)
	atr.Add(c)

	t.warmup[c.Symbol]++
	if t.warmup[c.Symbol] < t.cfg.WarmupCandles {
		return domain.Signal{}, false
	}

	fastVal, fastOK := fast.Value()
	slowVal, slowOK := slow.Value()
	if !fastOK || !slowOK {
		return domain.Signal{}, false
	}

	fastAboveSlow := fastVal.GreaterThan(slowVal)
	prev := t.prevFastAboveSlow[c.Symbol]
	t.prevFastAboveSlow[c.Symbol] = fastAboveSlow

	// Golden cross: fast crossed above slow → BUY
	if !prev && fastAboveSlow {
		if t.cfg.RequireTrendAlignment {
			if tv, ok := trend.Value(); ok && c.Close.LessThan(tv) {
				return domain.Signal{}, false
			}
		}
		return t.newSignal(c, domain.SideBuy,
			fmt.Sprintf("golden cross EMA%d/EMA%d; fast=%.4f slow=%.4f",
				t.cfg.Indicators.EMA.Fast, t.cfg.Indicators.EMA.Slow,
				fastVal.InexactFloat64(), slowVal.InexactFloat64()),
		), true
	}

	// Death cross: fast crossed below slow → SELL
	if prev && !fastAboveSlow {
		if t.cfg.RequireTrendAlignment {
			if tv, ok := trend.Value(); ok && c.Close.GreaterThan(tv) {
				return domain.Signal{}, false
			}
		}
		return t.newSignal(c, domain.SideSell,
			fmt.Sprintf("death cross EMA%d/EMA%d; fast=%.4f slow=%.4f",
				t.cfg.Indicators.EMA.Fast, t.cfg.Indicators.EMA.Slow,
				fastVal.InexactFloat64(), slowVal.InexactFloat64()),
		), true
	}

	return domain.Signal{}, false
}

func (t *TrendFollowing) newSignal(c domain.Candle, side domain.Side, reason string) domain.Signal {
	id := uuid.New().String()
	return domain.Signal{
		ID:            id,
		CorrelationID: id,
		Strategy:      t.cfg.Name,
		Symbol:        c.Symbol,
		Side:          side,
		Timeframe:     c.Timeframe,
		Reason:        reason,
		GeneratedAt:   time.Now().UTC(),
	}
}

// Warmup feeds historical candles through OnCandle to prime indicators.
// Signals produced during warmup are discarded.
func (t *TrendFollowing) Warmup(_ context.Context, candles []domain.Candle) {
	for _, c := range candles {
		t.OnCandle(context.Background(), c)
	}
}

func (t *TrendFollowing) isTargetSymbol(s domain.Symbol) bool {
	for _, sym := range t.symbols {
		if sym == s {
			return true
		}
	}
	return false
}

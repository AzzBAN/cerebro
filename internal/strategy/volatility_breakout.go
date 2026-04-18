package strategy

import (
	"context"
	"fmt"
	"time"

	"github.com/azhar/cerebro/internal/config"
	"github.com/azhar/cerebro/internal/domain"
	"github.com/azhar/cerebro/internal/strategy/indicators"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// VolatilityBreakout fires stop-limit orders above/below the recent consolidation
// range during the NY open and Asian open sessions. PRD §4.3.
type VolatilityBreakout struct {
	cfg     config.StrategyConfig
	symbols []domain.Symbol

	atr      map[domain.Symbol]*indicators.ATR
	bollinger map[domain.Symbol]*indicators.Bollinger
	warmup   map[domain.Symbol]int

	// Track the consolidation range within the current session window.
	sessionHigh map[domain.Symbol]decimal.Decimal
	sessionLow  map[domain.Symbol]decimal.Decimal
}

// NewVolatilityBreakout creates a VolatilityBreakout strategy from config.
func NewVolatilityBreakout(cfg config.StrategyConfig) *VolatilityBreakout {
	syms := make([]domain.Symbol, len(cfg.Markets))
	for i, m := range cfg.Markets {
		syms[i] = domain.Symbol(m)
	}
	s := &VolatilityBreakout{
		cfg:         cfg,
		symbols:     syms,
		atr:         make(map[domain.Symbol]*indicators.ATR),
		bollinger:   make(map[domain.Symbol]*indicators.Bollinger),
		warmup:      make(map[domain.Symbol]int),
		sessionHigh: make(map[domain.Symbol]decimal.Decimal),
		sessionLow:  make(map[domain.Symbol]decimal.Decimal),
	}
	for _, sym := range syms {
		s.atr[sym] = indicators.NewATR(cfg.Indicators.ATR.Period)
		s.bollinger[sym] = indicators.NewBollinger(cfg.Indicators.Bollinger.Period, cfg.Indicators.Bollinger.StdDev)
		s.sessionHigh[sym] = decimal.Zero
		s.sessionLow[sym] = decimal.Zero
	}
	return s
}

func (v *VolatilityBreakout) Name() domain.StrategyName  { return v.cfg.Name }
func (v *VolatilityBreakout) Symbols() []domain.Symbol    { return v.symbols }
func (v *VolatilityBreakout) Timeframes() []domain.Timeframe {
	return []domain.Timeframe{v.cfg.PrimaryTimeframe}
}

// OnCandle detects breakouts at session opens.
func (v *VolatilityBreakout) OnCandle(_ context.Context, c domain.Candle) (domain.Signal, bool) {
	if c.Timeframe != v.cfg.PrimaryTimeframe {
		return domain.Signal{}, false
	}
	if !v.isTargetSymbol(c.Symbol) {
		return domain.Signal{}, false
	}

	atr := v.atr[c.Symbol]
	bb := v.bollinger[c.Symbol]

	atr.Add(c)
	bb.Add(c.Close)

	v.warmup[c.Symbol]++
	if v.warmup[c.Symbol] < v.cfg.WarmupCandles {
		v.updateSessionRange(c)
		return domain.Signal{}, false
	}

	// Check if we're in the target session window.
	if !v.inSession(c.CloseTime) {
		// Outside session: track range for next session setup.
		v.updateSessionRange(c)
		return domain.Signal{}, false
	}

	atrVal, atrOK := atr.Value()
	if !atrOK {
		v.updateSessionRange(c)
		return domain.Signal{}, false
	}

	sh := v.sessionHigh[c.Symbol]
	sl := v.sessionLow[c.Symbol]
	if sh.IsZero() || sl.IsZero() {
		v.updateSessionRange(c)
		return domain.Signal{}, false
	}

	// Breakout above session high → BUY
	if c.Close.GreaterThan(sh.Add(atrVal.Mul(decimal.NewFromFloat(0.5)))) {
		v.resetSessionRange(c)
		return v.newSignal(c, domain.SideBuy,
			fmt.Sprintf("breakout above session high %.4f; ATR=%.4f",
				sh.InexactFloat64(), atrVal.InexactFloat64()),
		), true
	}

	// Breakdown below session low → SELL
	if c.Close.LessThan(sl.Sub(atrVal.Mul(decimal.NewFromFloat(0.5)))) {
		v.resetSessionRange(c)
		return v.newSignal(c, domain.SideSell,
			fmt.Sprintf("breakdown below session low %.4f; ATR=%.4f",
				sl.InexactFloat64(), atrVal.InexactFloat64()),
		), true
	}

	v.updateSessionRange(c)
	return domain.Signal{}, false
}

func (v *VolatilityBreakout) updateSessionRange(c domain.Candle) {
	sh := v.sessionHigh[c.Symbol]
	sl := v.sessionLow[c.Symbol]
	if sh.IsZero() || c.High.GreaterThan(sh) {
		v.sessionHigh[c.Symbol] = c.High
	}
	if sl.IsZero() || c.Low.LessThan(sl) {
		v.sessionLow[c.Symbol] = c.Low
	}
}

func (v *VolatilityBreakout) resetSessionRange(c domain.Candle) {
	v.sessionHigh[c.Symbol] = decimal.Zero
	v.sessionLow[c.Symbol] = decimal.Zero
}

func (v *VolatilityBreakout) newSignal(c domain.Candle, side domain.Side, reason string) domain.Signal {
	id := uuid.New().String()
	return domain.Signal{
		ID:            id,
		CorrelationID: id,
		Strategy:      v.cfg.Name,
		Symbol:        c.Symbol,
		Side:          side,
		Timeframe:     c.Timeframe,
		Reason:        reason,
		GeneratedAt:   time.Now().UTC(),
	}
}

// Warmup feeds historical candles through OnCandle to prime indicators.
// Signals produced during warmup are discarded.
func (v *VolatilityBreakout) Warmup(_ context.Context, candles []domain.Candle) {
	for _, c := range candles {
		v.OnCandle(context.Background(), c)
	}
}

func (v *VolatilityBreakout) isTargetSymbol(s domain.Symbol) bool {
	for _, sym := range v.symbols {
		if sym == s {
			return true
		}
	}
	return false
}

func (v *VolatilityBreakout) inSession(t time.Time) bool {
	if v.cfg.SessionFilter == domain.SessionAll || v.cfg.SessionFilter == "" {
		return true
	}
	win := domain.SessionWindowFor(v.cfg.SessionFilter)
	return win != nil && win.Contains(t)
}

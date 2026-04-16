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

// MeanReversion fires signals when RSI and Bollinger Bands indicate
// oversold/overbought conditions, optionally filtered by trend EMA.
// PRD §4.1: RSI + Bollinger Bands; AI Filter via bias alignment.
type MeanReversion struct {
	cfg     config.StrategyConfig
	symbols []domain.Symbol

	// Per-symbol indicator state.
	rsi      map[domain.Symbol]*indicators.RSI
	bollinger map[domain.Symbol]*indicators.Bollinger
	trendEMA map[domain.Symbol]*indicators.EMA
	atr      map[domain.Symbol]*indicators.ATR
	warmup   map[domain.Symbol]int
}

// NewMeanReversion creates a MeanReversion strategy from config.
func NewMeanReversion(cfg config.StrategyConfig) *MeanReversion {
	syms := make([]domain.Symbol, len(cfg.Markets))
	for i, m := range cfg.Markets {
		syms[i] = domain.Symbol(m)
	}
	s := &MeanReversion{
		cfg:      cfg,
		symbols:  syms,
		rsi:      make(map[domain.Symbol]*indicators.RSI),
		bollinger: make(map[domain.Symbol]*indicators.Bollinger),
		trendEMA: make(map[domain.Symbol]*indicators.EMA),
		atr:      make(map[domain.Symbol]*indicators.ATR),
		warmup:   make(map[domain.Symbol]int),
	}
	for _, sym := range syms {
		s.rsi[sym] = indicators.NewRSI(cfg.Indicators.RSI.Period)
		s.bollinger[sym] = indicators.NewBollinger(cfg.Indicators.Bollinger.Period, cfg.Indicators.Bollinger.StdDev)
		s.trendEMA[sym] = indicators.NewEMA(cfg.Indicators.EMA.Trend)
		s.atr[sym] = indicators.NewATR(cfg.Indicators.ATR.Period)
	}
	return s
}

func (m *MeanReversion) Name() domain.StrategyName { return m.cfg.Name }
func (m *MeanReversion) Symbols() []domain.Symbol   { return m.symbols }
func (m *MeanReversion) Timeframes() []domain.Timeframe {
	return []domain.Timeframe{m.cfg.PrimaryTimeframe}
}

// OnCandle evaluates one closed candle and emits a signal if conditions align.
func (m *MeanReversion) OnCandle(_ context.Context, c domain.Candle) (domain.Signal, bool) {
	if c.Timeframe != m.cfg.PrimaryTimeframe {
		return domain.Signal{}, false
	}
	if !m.isTargetSymbol(c.Symbol) {
		return domain.Signal{}, false
	}

	// Update indicators.
	rsi := m.rsi[c.Symbol]
	bb := m.bollinger[c.Symbol]
	trend := m.trendEMA[c.Symbol]
	atr := m.atr[c.Symbol]

	rsi.Add(c.Close)
	bb.Add(c.Close)
	trend.Add(c.Close)
	atr.Add(c)

	m.warmup[c.Symbol]++
	if m.warmup[c.Symbol] < m.cfg.WarmupCandles {
		return domain.Signal{}, false
	}

	// Check session filter.
	if !m.inSession(c.CloseTime) {
		return domain.Signal{}, false
	}

	// Oversold → BUY signal
	if rsi.IsOversold(m.cfg.Indicators.RSI.Oversold) && bb.IsBelowLower(c.Close) {
		if m.cfg.RequireTrendAlignment {
			if tv, ok := trend.Value(); ok && c.Close.LessThan(tv) {
				// Price below long trend EMA — skip bullish entry.
				return domain.Signal{}, false
			}
		}
		return m.newSignal(c, domain.SideBuy,
			fmt.Sprintf("RSI oversold + Bollinger lower breach; close=%.4f", c.Close.InexactFloat64()),
		), true
	}

	// Overbought → SELL signal
	if rsi.IsOverbought(m.cfg.Indicators.RSI.Overbought) && bb.IsAboveUpper(c.Close) {
		if m.cfg.RequireTrendAlignment {
			if tv, ok := trend.Value(); ok && c.Close.GreaterThan(tv) {
				return domain.Signal{}, false
			}
		}
		return m.newSignal(c, domain.SideSell,
			fmt.Sprintf("RSI overbought + Bollinger upper breach; close=%.4f", c.Close.InexactFloat64()),
		), true
	}

	return domain.Signal{}, false
}

func (m *MeanReversion) newSignal(c domain.Candle, side domain.Side, reason string) domain.Signal {
	id := uuid.New().String()
	return domain.Signal{
		ID:            id,
		CorrelationID: id,
		Strategy:      m.cfg.Name,
		Symbol:        c.Symbol,
		Side:          side,
		Timeframe:     c.Timeframe,
		Reason:        reason,
		GeneratedAt:   time.Now().UTC(),
	}
}

func (m *MeanReversion) isTargetSymbol(s domain.Symbol) bool {
	for _, sym := range m.symbols {
		if sym == s {
			return true
		}
	}
	return false
}

func (m *MeanReversion) inSession(t time.Time) bool {
	if m.cfg.SessionFilter == domain.SessionAll || m.cfg.SessionFilter == "" {
		return true
	}
	win := domain.SessionWindowFor(m.cfg.SessionFilter)
	return win != nil && win.Contains(t)
}

package marketdata

import (
	"sync"
	"time"

	"github.com/azhar/cerebro/internal/domain"
	"github.com/shopspring/decimal"
)

// CandleBuffer holds in-memory candle history for a single symbol+timeframe pair.
// The buffer is bounded; oldest candles are evicted when it overflows.
type CandleBuffer struct {
	mu       sync.RWMutex
	symbol   domain.Symbol
	tf       domain.Timeframe
	capacity int
	candles  []domain.Candle
}

// NewCandleBuffer creates a bounded candle history with the given capacity.
func NewCandleBuffer(symbol domain.Symbol, tf domain.Timeframe, capacity int) *CandleBuffer {
	return &CandleBuffer{
		symbol:   symbol,
		tf:       tf,
		capacity: capacity,
		candles:  make([]domain.Candle, 0, capacity),
	}
}

// Push appends a candle. If at capacity, the oldest is evicted.
func (b *CandleBuffer) Push(c domain.Candle) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.candles) >= b.capacity {
		b.candles = b.candles[1:]
	}
	b.candles = append(b.candles, c)
}

// All returns a copy of the candle slice (oldest first).
func (b *CandleBuffer) All() []domain.Candle {
	b.mu.RLock()
	defer b.mu.RUnlock()
	out := make([]domain.Candle, len(b.candles))
	copy(out, b.candles)
	return out
}

// Len returns the number of candles in the buffer.
func (b *CandleBuffer) Len() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.candles)
}

// Last returns the most recent candle, and false if empty.
func (b *CandleBuffer) Last() (domain.Candle, bool) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if len(b.candles) == 0 {
		return domain.Candle{}, false
	}
	return b.candles[len(b.candles)-1], true
}

// TickAggregator accumulates individual trade ticks into OHLCV candles.
// One aggregator per symbol+timeframe; thread-safe.
type TickAggregator struct {
	mu        sync.Mutex
	symbol    domain.Symbol
	tf        domain.Timeframe
	period    time.Duration
	current   *domain.Candle
	closeFunc func(domain.Candle)
}

// NewTickAggregator creates an aggregator that calls closeFunc each time a candle closes.
func NewTickAggregator(symbol domain.Symbol, tf domain.Timeframe, period time.Duration, closeFunc func(domain.Candle)) *TickAggregator {
	return &TickAggregator{
		symbol:    symbol,
		tf:        tf,
		period:    period,
		closeFunc: closeFunc,
	}
}

// AddTick updates the current open candle with a new price/volume tick.
func (a *TickAggregator) AddTick(price, volume decimal.Decimal, ts time.Time) {
	a.mu.Lock()
	defer a.mu.Unlock()

	barStart := ts.Truncate(a.period)
	barEnd := barStart.Add(a.period)

	// New bar: close the old one and start a fresh candle.
	if a.current == nil || !a.current.OpenTime.Equal(barStart) {
		if a.current != nil {
			closed := *a.current
			closed.Closed = true
			a.closeFunc(closed)
		}
		a.current = &domain.Candle{
			Symbol:    a.symbol,
			Timeframe: a.tf,
			OpenTime:  barStart,
			CloseTime: barEnd.Add(-time.Millisecond),
			Open:      price,
			High:      price,
			Low:       price,
			Close:     price,
			Volume:    volume,
			Closed:    false,
		}
		return
	}

	// Update the current bar.
	if price.GreaterThan(a.current.High) {
		a.current.High = price
	}
	if price.LessThan(a.current.Low) {
		a.current.Low = price
	}
	a.current.Close = price
	a.current.Volume = a.current.Volume.Add(volume)
}

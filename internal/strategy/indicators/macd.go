package indicators

import "github.com/shopspring/decimal"

// MACD computes the Moving Average Convergence Divergence indicator incrementally using Decimal EMAs.
type MACD struct {
	fastPeriod   int
	slowPeriod   int
	signalPeriod int
	fastEMA      *EMA
	slowEMA      *EMA
	signalEMA    *EMA
}

// NewMACD creates a MACD indicator. If any period is <= 0, standard defaults (12, 26, 9) are used.
func NewMACD(fast, slow, signal int) *MACD {
	if fast <= 0 {
		fast = 12
	}
	if slow <= 0 {
		slow = 26
	}
	if signal <= 0 {
		signal = 9
	}
	return &MACD{
		fastPeriod:   fast,
		slowPeriod:   slow,
		signalPeriod: signal,
		fastEMA:      NewEMA(fast),
		slowEMA:      NewEMA(slow),
		signalEMA:    NewEMA(signal),
	}
}

// Add updates the MACD indicator with a new price.
func (m *MACD) Add(price decimal.Decimal) {
	m.fastEMA.Add(price)
	m.slowEMA.Add(price)

	fastVal, fastOK := m.fastEMA.Value()
	slowVal, slowOK := m.slowEMA.Value()
	if fastOK && slowOK {
		macdVal := fastVal.Sub(slowVal)
		m.signalEMA.Add(macdVal)
	}
}

// Value returns the current MACD line, signal line, and histogram values.
// Returns (0, 0, 0, false) until both fast/slow EMAs and the signal EMA are ready.
func (m *MACD) Value() (macd, signal, hist decimal.Decimal, ready bool) {
	sigVal, sigOK := m.signalEMA.Value()
	if !sigOK {
		return decimal.Zero, decimal.Zero, decimal.Zero, false
	}
	fastVal, fastOK := m.fastEMA.Value()
	slowVal, slowOK := m.slowEMA.Value()
	if !fastOK || !slowOK {
		return decimal.Zero, decimal.Zero, decimal.Zero, false
	}

	macdVal := fastVal.Sub(slowVal)
	histVal := macdVal.Sub(sigVal)
	return macdVal, sigVal, histVal, true
}

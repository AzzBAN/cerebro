package indicators

import "github.com/shopspring/decimal"

// EMA computes an Exponential Moving Average incrementally.
type EMA struct {
	period int
	k      decimal.Decimal // smoothing factor = 2 / (period + 1)
	value  decimal.Decimal
	count  int
	ready  bool
}

// NewEMA creates an EMA calculator for the given period.
func NewEMA(period int) *EMA {
	k := decimal.NewFromInt(2).Div(decimal.NewFromInt(int64(period + 1)))
	return &EMA{period: period, k: k}
}

// Add updates the EMA with a new price.
func (e *EMA) Add(price decimal.Decimal) {
	e.count++
	if e.count < e.period {
		// Accumulate for SMA seed.
		e.value = e.value.Add(price)
		return
	}
	if e.count == e.period {
		// Seed: SMA of first period values.
		e.value = e.value.Add(price).Div(decimal.NewFromInt(int64(e.period)))
		e.ready = true
		return
	}
	// EMA formula: price × k + prev_ema × (1 − k)
	e.value = price.Mul(e.k).Add(e.value.Mul(decimal.NewFromInt(1).Sub(e.k)))
}

// Value returns the current EMA and whether it is ready (enough data).
func (e *EMA) Value() (decimal.Decimal, bool) {
	return e.value, e.ready
}

// CrossOver returns true if fast EMA crossed above slow EMA on this tick.
// fast and slow must both be ready. Call after adding the same price to both.
func CrossOver(fast, slow *EMA) bool {
	fv, fok := fast.Value()
	sv, sok := slow.Value()
	return fok && sok && fv.GreaterThan(sv)
}

// CrossUnder returns true if fast EMA crossed below slow EMA.
func CrossUnder(fast, slow *EMA) bool {
	fv, fok := fast.Value()
	sv, sok := slow.Value()
	return fok && sok && fv.LessThan(sv)
}

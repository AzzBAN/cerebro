package indicators

import "github.com/shopspring/decimal"

// RSI computes the Relative Strength Index using Wilder's smoothing.
// It maintains incremental state; call Add for each new close price.
type RSI struct {
	period  int
	prices  []decimal.Decimal
	avgGain decimal.Decimal
	avgLoss decimal.Decimal
	ready   bool
}

// NewRSI creates an RSI calculator for the given period.
func NewRSI(period int) *RSI {
	return &RSI{period: period}
}

// Add updates the RSI with a new closing price.
func (r *RSI) Add(price decimal.Decimal) {
	r.prices = append(r.prices, price)
	n := len(r.prices)

	if n < r.period+1 {
		return
	}

	if !r.ready {
		// Seed with simple average of first period gains/losses.
		var gains, losses decimal.Decimal
		for i := 1; i <= r.period; i++ {
			diff := r.prices[i].Sub(r.prices[i-1])
			if diff.IsPositive() {
				gains = gains.Add(diff)
			} else {
				losses = losses.Add(diff.Abs())
			}
		}
		pd := decimal.NewFromInt(int64(r.period))
		r.avgGain = gains.Div(pd)
		r.avgLoss = losses.Div(pd)
		r.ready = true
		return
	}

	diff := price.Sub(r.prices[n-2])
	gain, loss := decimal.Zero, decimal.Zero
	if diff.IsPositive() {
		gain = diff
	} else {
		loss = diff.Abs()
	}

	pd := decimal.NewFromInt(int64(r.period))
	r.avgGain = r.avgGain.Mul(pd.Sub(decimal.NewFromInt(1))).Add(gain).Div(pd)
	r.avgLoss = r.avgLoss.Mul(pd.Sub(decimal.NewFromInt(1))).Add(loss).Div(pd)
}

// Value returns the current RSI value (0–100). Returns (0, false) until enough data.
func (r *RSI) Value() (decimal.Decimal, bool) {
	if !r.ready {
		return decimal.Zero, false
	}
	if r.avgLoss.IsZero() {
		return decimal.NewFromInt(100), true
	}
	rs := r.avgGain.Div(r.avgLoss)
	rsi := decimal.NewFromInt(100).Sub(
		decimal.NewFromInt(100).Div(decimal.NewFromInt(1).Add(rs)),
	)
	return rsi, true
}

// IsOversold returns true when RSI is at or below the given threshold.
func (r *RSI) IsOversold(threshold int) bool {
	v, ok := r.Value()
	return ok && v.LessThanOrEqual(decimal.NewFromInt(int64(threshold)))
}

// IsOverbought returns true when RSI is at or above the given threshold.
func (r *RSI) IsOverbought(threshold int) bool {
	v, ok := r.Value()
	return ok && v.GreaterThanOrEqual(decimal.NewFromInt(int64(threshold)))
}

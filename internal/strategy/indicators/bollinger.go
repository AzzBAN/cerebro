package indicators

import (
	"math"

	"github.com/shopspring/decimal"
)

// Bollinger computes Bollinger Bands using a simple moving average and
// standard deviation over a rolling window.
type Bollinger struct {
	period int
	stdDev float64
	prices []decimal.Decimal
}

// NewBollinger creates a Bollinger Bands calculator.
func NewBollinger(period int, stdDev float64) *Bollinger {
	return &Bollinger{period: period, stdDev: stdDev}
}

// Add updates the rolling window with a new closing price.
func (b *Bollinger) Add(price decimal.Decimal) {
	b.prices = append(b.prices, price)
	if len(b.prices) > b.period {
		b.prices = b.prices[1:]
	}
}

// Bands returns (upper, middle/SMA, lower) bands and whether enough data exists.
func (b *Bollinger) Bands() (upper, middle, lower decimal.Decimal, ready bool) {
	if len(b.prices) < b.period {
		return decimal.Zero, decimal.Zero, decimal.Zero, false
	}

	// SMA
	sum := decimal.Zero
	for _, p := range b.prices {
		sum = sum.Add(p)
	}
	sma := sum.Div(decimal.NewFromInt(int64(b.period)))

	// Standard deviation
	var variance float64
	smaf, _ := sma.Float64()
	for _, p := range b.prices {
		pf, _ := p.Float64()
		diff := pf - smaf
		variance += diff * diff
	}
	variance /= float64(b.period)
	sd := math.Sqrt(variance)

	sdDec := decimal.NewFromFloat(sd * b.stdDev)
	return sma.Add(sdDec), sma, sma.Sub(sdDec), true
}

// IsBelowLower returns true when price is below the lower band.
func (b *Bollinger) IsBelowLower(price decimal.Decimal) bool {
	_, _, lower, ok := b.Bands()
	return ok && price.LessThan(lower)
}

// IsAboveUpper returns true when price is above the upper band.
func (b *Bollinger) IsAboveUpper(price decimal.Decimal) bool {
	upper, _, _, ok := b.Bands()
	return ok && price.GreaterThan(upper)
}

package indicators

import (
	"github.com/azhar/cerebro/internal/domain"
	"github.com/shopspring/decimal"
)

// ATR computes the Average True Range using Wilder's smoothing.
type ATR struct {
	period   int
	prevClose decimal.Decimal
	atr      decimal.Decimal
	count    int
	ready    bool
}

// NewATR creates an ATR calculator for the given period.
func NewATR(period int) *ATR {
	return &ATR{period: period}
}

// Add updates the ATR with a new candle.
func (a *ATR) Add(c domain.Candle) {
	var tr decimal.Decimal
	if a.count == 0 {
		// First candle: TR = High - Low
		tr = c.High.Sub(c.Low)
	} else {
		// TR = max(High-Low, |High-PrevClose|, |Low-PrevClose|)
		hl := c.High.Sub(c.Low)
		hpc := c.High.Sub(a.prevClose).Abs()
		lpc := c.Low.Sub(a.prevClose).Abs()
		tr = decimal.Max(hl, decimal.Max(hpc, lpc))
	}

	a.prevClose = c.Close
	a.count++

	if a.count < a.period {
		a.atr = a.atr.Add(tr)
		return
	}
	if a.count == a.period {
		a.atr = a.atr.Add(tr).Div(decimal.NewFromInt(int64(a.period)))
		a.ready = true
		return
	}

	// Wilder smoothing
	pd := decimal.NewFromInt(int64(a.period))
	a.atr = a.atr.Mul(pd.Sub(decimal.NewFromInt(1))).Add(tr).Div(pd)
}

// Value returns the current ATR and whether it is ready.
func (a *ATR) Value() (decimal.Decimal, bool) {
	return a.atr, a.ready
}

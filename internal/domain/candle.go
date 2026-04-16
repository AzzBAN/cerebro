package domain

import (
	"time"

	"github.com/shopspring/decimal"
)

// Candle is a single OHLCV bar.
type Candle struct {
	Symbol    Symbol
	Timeframe Timeframe
	OpenTime  time.Time
	CloseTime time.Time
	Open      decimal.Decimal
	High      decimal.Decimal
	Low       decimal.Decimal
	Close     decimal.Decimal
	Volume    decimal.Decimal
	Closed    bool // true once the candle's period has elapsed
}

// Quote is the latest bid/ask snapshot for a symbol.
type Quote struct {
	Symbol    Symbol
	Bid       decimal.Decimal
	Ask       decimal.Decimal
	Mid       decimal.Decimal
	Timestamp time.Time
}

// Spread returns (Ask - Bid) / Mid as a percentage.
func (q Quote) SpreadPct() decimal.Decimal {
	if q.Mid.IsZero() {
		return decimal.Zero
	}
	return q.Ask.Sub(q.Bid).Div(q.Mid).Mul(decimal.NewFromInt(100))
}

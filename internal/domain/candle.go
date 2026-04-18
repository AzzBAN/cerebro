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

// Quote is the latest market snapshot for a symbol.
// Fields are populated by multiple WS streams (bookTicker, 24hr ticker) and
// merged by the Hub before reaching the TUI.
type Quote struct {
	Symbol             Symbol
	Bid                decimal.Decimal
	Ask                decimal.Decimal
	Mid                decimal.Decimal
	Last               decimal.Decimal // last traded price
	PriceChange        decimal.Decimal // absolute 24h change
	PriceChangePercent decimal.Decimal // 24h change %
	Volume24h          decimal.Decimal // 24h quote volume
	Timestamp          time.Time
}

// Spread returns (Ask - Bid) / Mid as a percentage.
func (q Quote) SpreadPct() decimal.Decimal {
	if q.Mid.IsZero() {
		return decimal.Zero
	}
	return q.Ask.Sub(q.Bid).Div(q.Mid).Mul(decimal.NewFromInt(100))
}

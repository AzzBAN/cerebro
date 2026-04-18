package domain

import (
	"time"

	"github.com/shopspring/decimal"
)

// OrderIntent is an approved, sized order waiting to be submitted to a broker.
type OrderIntent struct {
	ID            string // client-generated UUID; used for idempotency
	CorrelationID string // traces back to Signal.CorrelationID
	Symbol        Symbol
	Venue         Venue
	Side          Side
	Quantity      decimal.Decimal
	StopLoss      decimal.Decimal
	TakeProfit1   decimal.Decimal
	ScaleOutPct   float64 // % of position to close at TP1 (0 = full close)
	Strategy      StrategyName
	Environment   Environment
	CreatedAt     time.Time
}

// Trade is a completed fill record.
type Trade struct {
	ID            string
	IntentID      string
	CorrelationID string
	Symbol        Symbol
	Side          Side
	Quantity      decimal.Decimal
	FillPrice     decimal.Decimal
	Fees          decimal.Decimal
	PnL           *decimal.Decimal // nil until the position closes
	Strategy      StrategyName
	Venue         Venue
	ClosedAt      *time.Time
	CreatedAt     time.Time
}

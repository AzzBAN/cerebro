package domain

import (
	"time"

	"github.com/shopspring/decimal"
)

// Position represents an open trade held at a broker.
type Position struct {
	Symbol        Symbol
	Venue         Venue
	Side          Side
	Quantity      decimal.Decimal
	EntryPrice    decimal.Decimal
	CurrentPrice  decimal.Decimal
	StopLoss      decimal.Decimal
	TakeProfit1   decimal.Decimal
	Strategy      StrategyName
	CorrelationID string
	OpenedAt      time.Time
}

// UnrealizedPnL returns the current unrealized profit/loss in quote currency.
func (p Position) UnrealizedPnL() decimal.Decimal {
	if p.Side == SideBuy {
		return p.CurrentPrice.Sub(p.EntryPrice).Mul(p.Quantity)
	}
	return p.EntryPrice.Sub(p.CurrentPrice).Mul(p.Quantity)
}

// UnrealizedPnLPct returns unrealized PnL as a percentage of entry value.
func (p Position) UnrealizedPnLPct() decimal.Decimal {
	entryValue := p.EntryPrice.Mul(p.Quantity)
	if entryValue.IsZero() {
		return decimal.Zero
	}
	return p.UnrealizedPnL().Div(entryValue).Mul(decimal.NewFromInt(100))
}

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
	// Leverage is the effective leverage for this position (e.g. 1 for spot,
	// 125 for a 125x isolated futures position). Zero means unknown.
	Leverage int
	// Margin is the actual margin allocated to this position in quote
	// currency, as reported by the exchange. For Binance USDT-M Futures
	// this is `isolatedWallet` (isolated mode) or `positionInitialMargin`
	// (cross mode). Zero means unknown — callers should fall back to the
	// derived `notional / leverage` minimum.
	Margin decimal.Decimal
	// Isolated reports whether the position is held in isolated margin
	// mode. Only meaningful for futures venues.
	Isolated bool
}

// InitialMargin returns the minimum margin required to hold this position
// (notional / leverage). For spot or unknown leverage it equals notional.
func (p Position) InitialMargin() decimal.Decimal {
	notional := p.EntryPrice.Mul(p.Quantity)
	if p.Leverage <= 1 {
		return notional
	}
	return notional.Div(decimal.NewFromInt(int64(p.Leverage)))
}

// EffectiveMargin returns the exchange-reported allocated margin if known,
// otherwise the derived InitialMargin. This is what the TUI / agents should
// display as "Margin (USDT)" — it matches the Binance app whenever the
// adapter populated `Margin` from `isolatedWallet` / `positionInitialMargin`.
func (p Position) EffectiveMargin() decimal.Decimal {
	if p.Margin.IsPositive() {
		return p.Margin
	}
	return p.InitialMargin()
}

// UnrealizedPnL returns the current unrealized profit/loss in quote currency.
func (p Position) UnrealizedPnL() decimal.Decimal {
	if p.Side == SideBuy {
		return p.CurrentPrice.Sub(p.EntryPrice).Mul(p.Quantity)
	}
	return p.EntryPrice.Sub(p.CurrentPrice).Mul(p.Quantity)
}

// UnrealizedPnLPct returns unrealized PnL as a percentage of entry value
// (i.e. price-move percentage, not leverage-adjusted).
func (p Position) UnrealizedPnLPct() decimal.Decimal {
	entryValue := p.EntryPrice.Mul(p.Quantity)
	if entryValue.IsZero() {
		return decimal.Zero
	}
	return p.UnrealizedPnL().Div(entryValue).Mul(decimal.NewFromInt(100))
}

// UnrealizedPnLROI returns unrealized PnL as a percentage of the initial
// margin (notional / leverage), matching Binance's "ROI%" display.
// Falls back to UnrealizedPnLPct when leverage is unknown or <= 1.
func (p Position) UnrealizedPnLROI() decimal.Decimal {
	if p.Leverage <= 1 {
		return p.UnrealizedPnLPct()
	}
	notional := p.EntryPrice.Mul(p.Quantity)
	if notional.IsZero() {
		return decimal.Zero
	}
	margin := notional.Div(decimal.NewFromInt(int64(p.Leverage)))
	if margin.IsZero() {
		return decimal.Zero
	}
	return p.UnrealizedPnL().Div(margin).Mul(decimal.NewFromInt(100))
}

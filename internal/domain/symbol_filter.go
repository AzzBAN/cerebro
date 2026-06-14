package domain

import "github.com/shopspring/decimal"

// SymbolFilter captures the exchange-imposed trading constraints for a single
// symbol. Values mirror the LOT_SIZE, PRICE_FILTER, and MIN_NOTIONAL filters
// returned by Binance's exchangeInfo endpoint.
//
// All fields may be zero when unknown; callers must treat a zero value as
// "no constraint" and log a warning rather than silently bypassing.
type SymbolFilter struct {
	Symbol      Symbol
	Venue       Venue
	BaseAsset   string
	QuoteAsset  string
	TickSize    decimal.Decimal // price granularity
	StepSize    decimal.Decimal // quantity granularity
	MinQty      decimal.Decimal // minimum base-asset quantity
	MaxQty      decimal.Decimal // maximum base-asset quantity per order
	MinNotional decimal.Decimal // minimum price * qty for a valid order
}

// QuantiseQty floors qty to the nearest stepSize multiple. Returns qty
// unchanged when StepSize is zero (unknown).
func (f SymbolFilter) QuantiseQty(qty decimal.Decimal) decimal.Decimal {
	if f.StepSize.IsZero() {
		return qty
	}
	return qty.Div(f.StepSize).Floor().Mul(f.StepSize)
}

// QuantisePriceDown floors price to the nearest tickSize multiple.
// Used for buy limit prices and buy stop prices where we want to avoid
// rejecting orders by overpaying past a price barrier.
func (f SymbolFilter) QuantisePriceDown(price decimal.Decimal) decimal.Decimal {
	if f.TickSize.IsZero() {
		return price
	}
	return price.Div(f.TickSize).Floor().Mul(f.TickSize)
}

// QuantisePriceUp ceilings price to the nearest tickSize multiple.
// Used for sell limit / sell stop prices.
func (f SymbolFilter) QuantisePriceUp(price decimal.Decimal) decimal.Decimal {
	if f.TickSize.IsZero() {
		return price
	}
	return price.Div(f.TickSize).Ceil().Mul(f.TickSize)
}

// QuantisePrice quantises a price for the given order side. Buy-side prices
// round down; sell-side prices round up. This choice preserves the intent of
// "at or better than" once the exchange tick constraint is applied.
func (f SymbolFilter) QuantisePrice(price decimal.Decimal, side Side) decimal.Decimal {
	if side == SideSell {
		return f.QuantisePriceUp(price)
	}
	return f.QuantisePriceDown(price)
}

// Validate reports whether a (qty, price) pair satisfies the filter.
// price may be zero for market orders; the notional check is skipped in that
// case. Returns nil when the order is acceptable.
func (f SymbolFilter) Validate(qty, price decimal.Decimal) error {
	if !f.MinQty.IsZero() && qty.LessThan(f.MinQty) {
		return ErrOrderBelowMinQty
	}
	if !f.MaxQty.IsZero() && qty.GreaterThan(f.MaxQty) {
		return ErrOrderAboveMaxQty
	}
	if !price.IsZero() && !f.MinNotional.IsZero() {
		if qty.Mul(price).LessThan(f.MinNotional) {
			return ErrOrderBelowMinNotional
		}
	}
	return nil
}

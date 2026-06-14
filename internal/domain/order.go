package domain

import (
	"time"

	"github.com/shopspring/decimal"
)

// OrderIntent is an approved, sized order waiting to be submitted to a broker.
//
// Field groups:
//   - Identity: ID, CorrelationID, Symbol, Venue, Strategy, Environment, CreatedAt.
//   - Entry: Side, OrderType, Quantity, LimitPrice, StopPrice, TIF.
//   - Bracket request: StopLoss, TakeProfit1, ScaleOutPct. When non-zero, the
//     execution layer attaches a bracket (OCO on spot, reduce-only STOP_MARKET
//     + TAKE_PROFIT_MARKET on futures) immediately after the entry order is
//     accepted. See port.Broker.PlaceBracket.
//   - Futures: ReduceOnly, PositionSide, Leverage. Leverage of 0 means
//     "use whatever the venue has already configured for this symbol".
type OrderIntent struct {
	// ── Identity ──────────────────────────────────────────────────────────
	ID            string // client-generated UUID; used for idempotency and newClientOrderID
	CorrelationID string // traces back to Signal.CorrelationID
	Symbol        Symbol
	Venue         Venue
	Strategy      StrategyName
	Environment   Environment
	CreatedAt     time.Time

	// ── Entry ─────────────────────────────────────────────────────────────
	Side      Side
	OrderType OrderType       // market | limit | stop_limit — zero value treated as market
	Quantity  decimal.Decimal // base-asset size; must be quantised to symbol stepSize before submission
	// LimitPrice is the limit price for OrderTypeLimit and the limit price
	// component of OrderTypeStopLimit. Ignored for OrderTypeMarket.
	LimitPrice decimal.Decimal
	// StopPrice is the trigger price for OrderTypeStopLimit. Ignored for
	// other entry types.
	StopPrice decimal.Decimal
	TIF       TimeInForce

	// ── Bracket request (filled post-entry) ───────────────────────────────
	StopLoss    decimal.Decimal // trigger price; zero = no protective stop
	TakeProfit1 decimal.Decimal // limit price for TP1; zero = no take profit
	ScaleOutPct float64         // % of position to close at TP1 (0 = full close)

	// ── Futures-only ──────────────────────────────────────────────────────
	ReduceOnly   bool
	PositionSide PositionSide // zero value (empty) = BOTH / one-way mode
	Leverage     int          // 0 = do not change existing venue leverage
}

// OrderTypeOrDefault returns OrderType, falling back to market when unset.
func (o OrderIntent) OrderTypeOrDefault() OrderType {
	if o.OrderType == "" {
		return OrderTypeMarket
	}
	return o.OrderType
}

// HasBracket reports whether the intent carries protective SL/TP levels that
// the execution layer should attach after the entry fills.
func (o OrderIntent) HasBracket() bool {
	return !o.StopLoss.IsZero() || !o.TakeProfit1.IsZero()
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

// BracketRequest describes a protective SL/TP pair to be attached to an open
// position. Spot implementations translate this into an OCO order; futures
// implementations into reduce-only STOP_MARKET + TAKE_PROFIT_MARKET pair
// driven by the mark price.
//
// Side here is the ENTRY side of the underlying position. The exit side is
// always the opposite.
type BracketRequest struct {
	ParentIntentID string // ID of the OrderIntent that created the underlying position
	CorrelationID  string
	Symbol         Symbol
	Venue          Venue
	Side           Side // entry side of the protected position
	Quantity       decimal.Decimal
	StopLoss       decimal.Decimal // trigger price for the protective stop
	TakeProfit     decimal.Decimal // limit / trigger price for the take profit
	ScaleOutPct    float64         // not yet used by brackets; carried for audit
	TIF            TimeInForce     // applied to the limit leg on spot
	PositionSide   PositionSide    // futures hedge mode; empty = BOTH
	ClientTag      string          // short string embedded in newClientOrderID for tracing
}

// BracketResponse carries the broker-assigned identifiers for a bracket.
// For Binance spot this is an OCO list with two child orders; for futures
// it is a pair of independent algo orders.
type BracketResponse struct {
	// ListID is the OCO list ID on spot; empty on futures.
	ListID string
	// StopOrderID identifies the protective stop leg.
	StopOrderID string
	// TakeProfitOrderID identifies the take-profit leg.
	TakeProfitOrderID string
	// Symbol is echoed back for the cancellation path, which needs it on Binance.
	Symbol Symbol
}

// HasStop reports whether the bracket produced a protective stop leg.
func (b BracketResponse) HasStop() bool { return b.StopOrderID != "" }

// HasTakeProfit reports whether the bracket produced a take-profit leg.
func (b BracketResponse) HasTakeProfit() bool { return b.TakeProfitOrderID != "" }

// CancelRequest uniquely identifies an open order on an exchange.
// Symbol is mandatory on Binance for every cancel call.
type CancelRequest struct {
	Symbol        Symbol
	BrokerOrderID string
	// ClientOrderID is an optional alternative lookup key; when non-empty,
	// adapters may use it instead of BrokerOrderID.
	ClientOrderID string
}

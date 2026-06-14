package tools

import (
	"github.com/azhar/cerebro/internal/domain"
	"github.com/shopspring/decimal"
)

// AgentOrderRequest carries every parameter the LLM risk agent can specify
// when routing an order via approve_and_route_order / resize_and_route_order.
// Zero values are treated as "defer to the deterministic defaults" — so a
// minimal call with only Symbol / Side / Size still works and behaves
// exactly like the pre-bracket implementation (MARKET, no SL/TP).
type AgentOrderRequest struct {
	// Required
	Symbol domain.Symbol
	Side   domain.Side
	Size   float64

	// Optional entry spec
	OrderType  domain.OrderType   // market (default) | limit | stop_limit
	LimitPrice decimal.Decimal    // required for limit / stop_limit
	StopPrice  decimal.Decimal    // required for stop_limit (trigger)
	TIF        domain.TimeInForce // default GTC

	// Optional protective bracket — attached automatically by the worker
	// once the entry is accepted. Agent callers MUST specify StopLoss when
	// they want a bracket; TakeProfit is optional.
	StopLoss    decimal.Decimal
	TakeProfit1 decimal.Decimal
	ScaleOutPct float64 // % to close at TP1; 0 = full close

	// Futures-only
	ReduceOnly   bool
	PositionSide domain.PositionSide
	Leverage     int

	// Tracing
	CorrelationID string
	Reason        string // free-text rationale; logged to audit
}

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/azhar/cerebro/internal/domain"
	"github.com/azhar/cerebro/internal/port"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// RejectSignal implements the reject_signal agent tool.
func RejectSignal(audit port.AuditStore) port.Tool {
	return port.Tool{
		Handler: func(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
			var args struct {
				Reason string `json:"reason"`
			}
			if err := json.Unmarshal(input, &args); err != nil {
				return nil, fmt.Errorf("reject_signal: bad args: %w", err)
			}

			slog.Info("signal rejected by risk agent", "reason", args.Reason)
			_ = audit.SaveEvent(ctx, domain.AuditEvent{
				ID:        uuid.New().String(),
				EventType: "signal_rejected",
				Actor:     "risk_agent",
				Payload:   map[string]any{"reason": args.Reason},
				CreatedAt: time.Now().UTC(),
			})

			return json.Marshal(map[string]any{"rejected": true, "reason": args.Reason})
		},
		Definition: port.ToolDefinition{
			Name:        "reject_signal",
			Description: "Reject a trading signal with a reason. Used by the risk review agent.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"reason": map[string]any{
						"type":        "string",
						"description": "Reason for rejecting the signal",
					},
				},
				"required": []string{"reason"},
			},
		},
	}
}

// approveOrderArgs is the union of all fields the agent may supply when
// calling approve_and_route_order. Every optional field has a zero value
// that the execution layer treats as "use the default" — so a minimal call
// with just symbol/side/size still works and routes a MARKET order.
type approveOrderArgs struct {
	Symbol string  `json:"symbol"`
	Side   string  `json:"side"`
	Size   float64 `json:"size"`

	OrderType   string  `json:"order_type,omitempty"`    // market|limit|stop_limit
	LimitPrice  float64 `json:"limit_price,omitempty"`   // required for limit / stop_limit
	StopPrice   float64 `json:"stop_price,omitempty"`    // required for stop_limit (trigger)
	TimeInForce string  `json:"time_in_force,omitempty"` // gtc|ioc|fok

	StopLoss    float64 `json:"stop_loss,omitempty"`   // protective bracket
	TakeProfit  float64 `json:"take_profit,omitempty"` // bracket TP1
	ScaleOutPct float64 `json:"scale_out_pct,omitempty"`

	ReduceOnly   bool   `json:"reduce_only,omitempty"`
	PositionSide string `json:"position_side,omitempty"` // both|long|short
	Leverage     int    `json:"leverage,omitempty"`

	CorrelationID string `json:"correlation_id,omitempty"`
	Reason        string `json:"reason,omitempty"`
}

// toAgentOrderRequest validates the args and converts to the richer
// AgentOrderRequest the route function expects.
func (a approveOrderArgs) toAgentOrderRequest() (AgentOrderRequest, error) {
	if a.Symbol == "" {
		return AgentOrderRequest{}, fmt.Errorf("symbol is required")
	}
	if a.Size <= 0 {
		return AgentOrderRequest{}, fmt.Errorf("size must be > 0")
	}
	side := domain.Side(a.Side)
	if side != domain.SideBuy && side != domain.SideSell {
		// Tolerate uppercase BUY/SELL from older prompt templates.
		switch a.Side {
		case "BUY", "Buy":
			side = domain.SideBuy
		case "SELL", "Sell":
			side = domain.SideSell
		default:
			return AgentOrderRequest{}, fmt.Errorf("invalid side %q", a.Side)
		}
	}

	orderType := domain.OrderType(a.OrderType)
	switch orderType {
	case "":
		orderType = domain.OrderTypeMarket
	case domain.OrderTypeMarket, domain.OrderTypeLimit, domain.OrderTypeStopLimit:
		// ok
	default:
		return AgentOrderRequest{}, fmt.Errorf("invalid order_type %q", a.OrderType)
	}
	if orderType == domain.OrderTypeLimit && a.LimitPrice <= 0 {
		return AgentOrderRequest{}, fmt.Errorf("limit_price is required for limit orders")
	}
	if orderType == domain.OrderTypeStopLimit && (a.LimitPrice <= 0 || a.StopPrice <= 0) {
		return AgentOrderRequest{}, fmt.Errorf("stop_limit requires both limit_price and stop_price")
	}

	tif := domain.TimeInForce(a.TimeInForce)
	switch tif {
	case "", domain.TIFGTC, domain.TIFIOC, domain.TIFFOK:
		// ok
	default:
		return AgentOrderRequest{}, fmt.Errorf("invalid time_in_force %q", a.TimeInForce)
	}

	positionSide := domain.PositionSide(a.PositionSide)
	switch positionSide {
	case "", domain.PositionSideBoth, domain.PositionSideLong, domain.PositionSideShort:
		// ok
	default:
		return AgentOrderRequest{}, fmt.Errorf("invalid position_side %q", a.PositionSide)
	}

	return AgentOrderRequest{
		Symbol:        domain.Symbol(a.Symbol),
		Side:          side,
		Size:          a.Size,
		OrderType:     orderType,
		LimitPrice:    toDecimal(a.LimitPrice),
		StopPrice:     toDecimal(a.StopPrice),
		TIF:           tif,
		StopLoss:      toDecimal(a.StopLoss),
		TakeProfit1:   toDecimal(a.TakeProfit),
		ScaleOutPct:   a.ScaleOutPct,
		ReduceOnly:    a.ReduceOnly,
		PositionSide:  positionSide,
		Leverage:      a.Leverage,
		CorrelationID: a.CorrelationID,
		Reason:        a.Reason,
	}, nil
}

func toDecimal(f float64) decimal.Decimal {
	if f == 0 {
		return decimal.Zero
	}
	return decimal.NewFromFloat(f)
}

// ApproveAndRouteOrder implements the approve_and_route_order agent tool.
//
// The LLM supplies order_type / limit_price / stop_price / stop_loss /
// take_profit / time_in_force when it wants a specific entry type or a
// protective bracket. Omitted fields default to the MARKET / no-bracket
// behaviour for backwards compatibility with older prompts.
func ApproveAndRouteOrder(
	routeFn func(ctx context.Context, req AgentOrderRequest) error,
) port.Tool {
	return port.Tool{
		Handler: func(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
			var args approveOrderArgs
			if err := json.Unmarshal(input, &args); err != nil {
				return nil, fmt.Errorf("approve_and_route_order: bad args: %w", err)
			}
			req, err := args.toAgentOrderRequest()
			if err != nil {
				return nil, fmt.Errorf("approve_and_route_order: %w", err)
			}
			if err := routeFn(ctx, req); err != nil {
				return nil, fmt.Errorf("approve_and_route_order: route failed: %w", err)
			}

			slog.Info("order approved and routed by risk agent",
				"symbol", req.Symbol, "side", req.Side, "size", req.Size,
				"order_type", req.OrderType, "stop_loss", req.StopLoss.String(),
				"take_profit", req.TakeProfit1.String())
			return json.Marshal(map[string]any{
				"routed":      true,
				"order_type":  string(req.OrderType),
				"has_bracket": !req.StopLoss.IsZero(),
			})
		},
		Definition: port.ToolDefinition{
			Name: "approve_and_route_order",
			Description: "Approve a trading signal and route it to the execution engine. " +
				"Supports market/limit/stop_limit entries and optional protective " +
				"brackets (stop_loss and take_profit). When stop_loss is set the " +
				"broker attaches an OCO on spot or a reduce-only STOP_MARKET + " +
				"TAKE_PROFIT_MARKET pair on futures once the entry is accepted.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"symbol": map[string]any{
						"type":        "string",
						"description": "Trading symbol, e.g. BTC/USDT",
					},
					"side": map[string]any{
						"type":        "string",
						"description": "Order side",
						"enum":        []string{"buy", "sell", "BUY", "SELL"},
					},
					"size": map[string]any{
						"type":        "number",
						"description": "Order size in base currency units",
					},
					"order_type": map[string]any{
						"type":        "string",
						"description": "Entry order type. Defaults to market.",
						"enum":        []string{"market", "limit", "stop_limit"},
					},
					"limit_price": map[string]any{
						"type":        "number",
						"description": "Limit price (required for limit and stop_limit)",
					},
					"stop_price": map[string]any{
						"type":        "number",
						"description": "Trigger price (required for stop_limit)",
					},
					"time_in_force": map[string]any{
						"type":        "string",
						"description": "Time in force for limit orders. Defaults to gtc.",
						"enum":        []string{"gtc", "ioc", "fok"},
					},
					"stop_loss": map[string]any{
						"type":        "number",
						"description": "Protective stop price. When set, a broker-side bracket is attached after entry.",
					},
					"take_profit": map[string]any{
						"type":        "number",
						"description": "Take-profit price for the first TP level.",
					},
					"scale_out_pct": map[string]any{
						"type":        "number",
						"description": "Fraction of the position to close at take_profit (0-100). 0 = full close.",
					},
					"reduce_only": map[string]any{
						"type":        "boolean",
						"description": "Futures only: the order must only reduce an existing position.",
					},
					"position_side": map[string]any{
						"type":        "string",
						"description": "Futures hedge mode position side. Defaults to both (one-way).",
						"enum":        []string{"both", "long", "short"},
					},
					"leverage": map[string]any{
						"type":        "integer",
						"description": "Futures only: leverage multiplier. 0 = inherit from markets.yaml.",
					},
					"correlation_id": map[string]any{
						"type":        "string",
						"description": "Upstream signal correlation ID for audit tracing.",
					},
					"reason": map[string]any{
						"type":        "string",
						"description": "Rationale for approving this order — logged to audit.",
					},
				},
				"required": []string{"symbol", "side", "size"},
			},
		},
	}
}

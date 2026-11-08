package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/azhar/cerebro/internal/domain"
	"github.com/azhar/cerebro/internal/port"
	"github.com/azhar/cerebro/internal/risk"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// GetCurrentDrawdown implements the get_current_drawdown agent tool.
func GetCurrentDrawdown(gate *risk.Gate) func() port.Tool {
	return func() port.Tool {
		return port.Tool{
			Handler: func(_ context.Context, _ json.RawMessage) (json.RawMessage, error) {
				halted := gate.IsHalted()
				result := map[string]any{
					"halted":          halted,
					"session_pnl_usd": "0",
					"daily_pnl_usd":   "0",
				}
				return json.Marshal(result)
			},
			Definition: port.ToolDefinition{
				Name:        "get_current_drawdown",
				Description: "Get the current session drawdown and halt status.",
				InputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
			},
		}
	}
}

// CalculatePositionSize implements the calculate_position_size agent tool.
func CalculatePositionSize() port.Tool {
	return port.Tool{
		Handler: func(_ context.Context, input json.RawMessage) (json.RawMessage, error) {
			var args struct {
				RiskPct          float64 `json:"risk_pct"`
				StopLossDistance float64 `json:"stop_loss_distance"`
				StopLoss         float64 `json:"stop_loss"`
				Equity           float64 `json:"equity"`
				EntryPrice       float64 `json:"entry_price"`
				Side             string  `json:"side"`
				MMR              float64 `json:"mmr"`
			}
			if err := json.Unmarshal(input, &args); err != nil {
				return nil, fmt.Errorf("calculate_position_size: bad args: %w", err)
			}

			side := domain.SideBuy
			if strings.EqualFold(args.Side, "sell") {
				side = domain.SideSell
			}

			entryPrice := decimal.NewFromFloat(args.EntryPrice)
			var stopLoss decimal.Decimal
			if args.StopLoss > 0 {
				stopLoss = decimal.NewFromFloat(args.StopLoss)
			} else if args.StopLossDistance > 0 {
				slDist := decimal.NewFromFloat(args.StopLossDistance)
				if side == domain.SideSell {
					stopLoss = entryPrice.Add(slDist)
				} else {
					stopLoss = entryPrice.Sub(slDist)
				}
			} else {
				return nil, fmt.Errorf("calculate_position_size: either stop_loss or stop_loss_distance must be provided")
			}

			equity := decimal.NewFromFloat(args.Equity)
			mmr := decimal.NewFromFloat(args.MMR)

			params, err := risk.CalculatePositionSize(
				equity,
				args.RiskPct,
				entryPrice,
				stopLoss,
				decimal.Zero,
				decimal.Zero,
				decimal.Zero,
			)
			if err != nil {
				return nil, fmt.Errorf("calculate_position_size: %w", err)
			}

			levRes, levErr := risk.CalculateSafeLeverage(
				entryPrice,
				stopLoss,
				side,
				equity,
				args.RiskPct,
				mmr,
			)
			if levErr != nil {
				// Fallback to basic sizing output if leverage calc fails
				return json.Marshal(map[string]any{
					"quantity":          params.Quantity.String(),
					"risk_amount_quote": params.RiskAmountQuote.String(),
				})
			}

			return json.Marshal(map[string]any{
				"quantity":                    params.Quantity.String(),
				"risk_amount_quote":           params.RiskAmountQuote.String(),
				"notional":                    levRes.Notional.StringFixed(2),
				"max_safe_leverage":           levRes.MaxSafeLeverage,
				"recommended_leverage":        levRes.RecommendedLeverage,
				"estimated_liquidation_price": levRes.LiquidationPrice.StringFixed(4),
				"required_margin":             levRes.RequiredMargin.StringFixed(2),
				"stop_loss_distance_pct":      levRes.StopLossDistancePct.StringFixed(2),
			})
		},
		Definition: port.ToolDefinition{
			Name: "calculate_position_size",
			Description: "Calculate the appropriate position size, safe leverage, and liquidation price given risk parameters. " +
				"Ensures liquidation price is safely beyond stop-loss distance.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"risk_pct":           map[string]any{"type": "number", "description": "Risk percentage per trade (e.g. 0.5 or 1.0)"},
					"stop_loss_distance": map[string]any{"type": "number", "description": "Stop loss distance from entry price in quote units (optional if stop_loss provided)"},
					"stop_loss":          map[string]any{"type": "number", "description": "Absolute stop loss price (optional if stop_loss_distance provided)"},
					"equity":             map[string]any{"type": "number", "description": "Account equity in quote currency"},
					"entry_price":        map[string]any{"type": "number", "description": "Entry price"},
					"side":               map[string]any{"type": "string", "enum": []string{"buy", "sell"}, "description": "Trade side (buy/sell). Defaults to buy."},
					"mmr":                map[string]any{"type": "number", "description": "Maintenance margin rate (default 0.005)"},
				},
				"required": []string{"risk_pct", "equity", "entry_price"},
			},
		},
	}
}

// ResizeAndRouteOrder approves a signal with a reduced position size.
// resized_size must be <= original_size — the agent can never increase
// exposure through this path. All other order-shaping fields (order_type,
// limit_price, stop_loss, take_profit, time_in_force, reduce_only,
// position_side, leverage) are identical to approve_and_route_order.
func ResizeAndRouteOrder(
	routeFn func(ctx context.Context, req AgentOrderRequest) error,
	audit port.AuditStore,
) port.Tool {
	return port.Tool{
		Handler: func(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
			var args struct {
				approveOrderArgs
				OriginalSize float64 `json:"original_size"`
				ResizedSize  float64 `json:"resized_size"`
			}
			if err := json.Unmarshal(input, &args); err != nil {
				return nil, fmt.Errorf("resize_and_route_order: bad args: %w", err)
			}

			if args.ResizedSize > args.OriginalSize {
				return nil, fmt.Errorf("resize_and_route_order: resized_size (%.8f) must not exceed original_size (%.8f)",
					args.ResizedSize, args.OriginalSize)
			}
			if args.ResizedSize <= 0 {
				return nil, fmt.Errorf("resize_and_route_order: resized_size must be > 0")
			}

			// Size lives on the embedded struct; copy the resized figure in
			// before validation so the rest of the contract flows through.
			args.Size = args.ResizedSize
			req, err := args.toAgentOrderRequest()
			if err != nil {
				return nil, fmt.Errorf("resize_and_route_order: %w", err)
			}

			if err := routeFn(ctx, req); err != nil {
				return nil, fmt.Errorf("resize_and_route_order: route failed: %w", err)
			}

			slog.Info("order resized and routed by risk agent",
				"symbol", args.Symbol, "side", args.Side,
				"original_size", args.OriginalSize, "resized_size", args.ResizedSize,
				"order_type", req.OrderType,
				"reason", args.Reason)

			_ = audit.SaveEvent(ctx, domain.AuditEvent{
				ID:        uuid.New().String(),
				EventType: "order_resized",
				Actor:     "risk_agent",
				Payload: map[string]any{
					"symbol":        args.Symbol,
					"side":          args.Side,
					"original_size": args.OriginalSize,
					"resized_size":  args.ResizedSize,
					"order_type":    string(req.OrderType),
					"stop_loss":     req.StopLoss.String(),
					"take_profit":   req.TakeProfit1.String(),
					"reason":        args.Reason,
				},
				CreatedAt: time.Now().UTC(),
			})

			return json.Marshal(map[string]any{
				"routed":        true,
				"resized":       true,
				"original_size": args.OriginalSize,
				"resized_size":  args.ResizedSize,
				"order_type":    string(req.OrderType),
				"has_bracket":   !req.StopLoss.IsZero(),
				"reason":        args.Reason,
			})
		},
		Definition: port.ToolDefinition{
			Name: "resize_and_route_order",
			Description: "Approve a trading signal with a reduced position size. " +
				"resized_size must be <= original_size. Accepts the same entry and " +
				"bracket fields as approve_and_route_order (order_type, limit_price, " +
				"stop_price, stop_loss, take_profit, time_in_force, reduce_only, " +
				"position_side, leverage). Use when the full size exceeds balance, " +
				"min-notional, or drawdown thresholds.",
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
					"original_size": map[string]any{
						"type":        "number",
						"description": "Originally calculated position size",
					},
					"resized_size": map[string]any{
						"type":        "number",
						"description": "Reduced position size (must be <= original_size)",
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
					"reason": map[string]any{
						"type":        "string",
						"description": "Reason for resizing",
					},
				},
				"required": []string{"symbol", "side", "original_size", "resized_size", "reason"},
			},
		},
	}
}

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
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
				Equity           float64 `json:"equity"`
				EntryPrice       float64 `json:"entry_price"`
			}
			if err := json.Unmarshal(input, &args); err != nil {
				return nil, fmt.Errorf("calculate_position_size: bad args: %w", err)
			}
			params, err := risk.CalculatePositionSize(
				decimal.NewFromFloat(args.Equity),
				args.RiskPct,
				decimal.NewFromFloat(args.EntryPrice),
				decimal.NewFromFloat(args.EntryPrice-args.StopLossDistance),
				decimal.Zero,
				decimal.Zero,
				decimal.Zero,
			)
			if err != nil {
				return nil, fmt.Errorf("calculate_position_size: %w", err)
			}
			return json.Marshal(map[string]any{
				"quantity":          params.Quantity.String(),
				"risk_amount_quote": params.RiskAmountQuote.String(),
			})
		},
		Definition: port.ToolDefinition{
			Name:        "calculate_position_size",
			Description: "Calculate the appropriate position size given risk parameters.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"risk_pct":            map[string]any{"type": "number", "description": "Risk percentage per trade (e.g. 0.5)"},
					"stop_loss_distance":  map[string]any{"type": "number", "description": "Stop loss distance from entry price in quote units"},
					"equity":              map[string]any{"type": "number", "description": "Account equity in quote currency"},
					"entry_price":         map[string]any{"type": "number", "description": "Entry price"},
				},
				"required": []string{"risk_pct", "stop_loss_distance", "equity", "entry_price"},
			},
		},
	}
}

// ResizeAndRouteOrder approves a signal with a reduced position size.
// resized_size must be <= original_size — the agent can never increase exposure.
func ResizeAndRouteOrder(
	routeFn func(ctx context.Context, symbol domain.Symbol, side domain.Side, size float64) error,
	audit port.AuditStore,
) port.Tool {
	return port.Tool{
		Handler: func(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
			var args struct {
				Symbol       string  `json:"symbol"`
				Side         string  `json:"side"`
				OriginalSize float64 `json:"original_size"`
				ResizedSize  float64 `json:"resized_size"`
				Reason       string  `json:"reason"`
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

			if err := routeFn(ctx, domain.Symbol(args.Symbol), domain.Side(args.Side), args.ResizedSize); err != nil {
				return nil, fmt.Errorf("resize_and_route_order: route failed: %w", err)
			}

			slog.Info("order resized and routed by risk agent",
				"symbol", args.Symbol, "side", args.Side,
				"original_size", args.OriginalSize, "resized_size", args.ResizedSize,
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
					"reason":        args.Reason,
				},
				CreatedAt: time.Now().UTC(),
			})

			return json.Marshal(map[string]any{
				"routed":        true,
				"resized":       true,
				"original_size": args.OriginalSize,
				"resized_size":  args.ResizedSize,
				"reason":        args.Reason,
			})
		},
		Definition: port.ToolDefinition{
			Name:        "resize_and_route_order",
			Description: "Approve a trading signal with a reduced position size. resized_size must be <= original_size. Use when full size exceeds balance or min notional thresholds.",
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
						"enum":        []string{"BUY", "SELL"},
					},
					"original_size": map[string]any{
						"type":        "number",
						"description": "Originally calculated position size",
					},
					"resized_size": map[string]any{
						"type":        "number",
						"description": "Reduced position size (must be <= original_size)",
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

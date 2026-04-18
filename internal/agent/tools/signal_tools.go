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

// ApproveAndRouteOrder implements the approve_and_route_order agent tool.
func ApproveAndRouteOrder(
	routeFn func(ctx context.Context, symbol domain.Symbol, side domain.Side, size float64) error,
) port.Tool {
	return port.Tool{
		Handler: func(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
			var args struct {
				Symbol string  `json:"symbol"`
				Side   string  `json:"side"`
				Size   float64 `json:"size"`
			}
			if err := json.Unmarshal(input, &args); err != nil {
				return nil, fmt.Errorf("approve_and_route_order: bad args: %w", err)
			}

			if err := routeFn(ctx, domain.Symbol(args.Symbol), domain.Side(args.Side), args.Size); err != nil {
				return nil, fmt.Errorf("approve_and_route_order: route failed: %w", err)
			}

			slog.Info("order approved and routed by risk agent",
				"symbol", args.Symbol, "side", args.Side, "size", args.Size)
			return json.Marshal(map[string]any{"routed": true})
		},
		Definition: port.ToolDefinition{
			Name:        "approve_and_route_order",
			Description: "Approve a trading signal and route it as an order to the execution engine.",
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
					"size": map[string]any{
						"type":        "number",
						"description": "Order size in base currency units",
					},
				},
				"required": []string{"symbol", "side", "size"},
			},
		},
	}
}

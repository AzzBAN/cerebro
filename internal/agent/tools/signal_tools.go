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
// Only Risk Agent may call this (enforced by tool policy).
// Input: { "reason": "drawdown limit approaching" }
func RejectSignal(audit port.AuditStore) func(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
	return func(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
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
	}
}

// ApproveAndRouteOrder implements the approve_and_route_order agent tool.
// Only Risk Agent may call this (enforced by tool policy).
// Input: { "symbol": "BTCUSDT", "side": "buy", "size": 0.01 }
func ApproveAndRouteOrder(
	routeFn func(ctx context.Context, symbol domain.Symbol, side domain.Side, size float64) error,
) func(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
	return func(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
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
	}
}

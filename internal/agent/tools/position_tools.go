package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/azhar/cerebro/internal/port"
)

// GetActivePositions implements the get_active_positions agent tool.
func GetActivePositions(brokers []port.Broker) port.Tool {
	return port.Tool{
		Handler: func(ctx context.Context, _ json.RawMessage) (json.RawMessage, error) {
			var all []any
			for _, broker := range brokers {
				positions, err := broker.Positions(ctx)
				if err != nil {
					return nil, fmt.Errorf("get_active_positions: broker %s: %w", broker.Venue(), err)
				}
				for _, p := range positions {
					all = append(all, map[string]any{
						"symbol":        string(p.Symbol),
						"venue":         string(p.Venue),
						"side":          string(p.Side),
						"quantity":      p.Quantity.String(),
						"entry_price":   p.EntryPrice.String(),
						"current_price": p.CurrentPrice.String(),
						"stop_loss":     p.StopLoss.String(),
						"pnl_pct":       p.UnrealizedPnLPct().String(),
						"strategy":      string(p.Strategy),
					})
				}
			}
			return json.Marshal(all)
		},
		Definition: port.ToolDefinition{
			Name:        "get_active_positions",
			Description: "Get all currently open trading positions across all venues.",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
		},
	}
}

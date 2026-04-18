package tools

import (
	"context"
	"encoding/json"

	"github.com/azhar/cerebro/internal/port"
)

// GetAccountBalance implements the get_account_balance agent tool.
// It queries all configured brokers and returns the USDT balance for each venue.
func GetAccountBalance(brokers []port.Broker) port.Tool {
	return port.Tool{
		Handler: func(ctx context.Context, _ json.RawMessage) (json.RawMessage, error) {
			var results []map[string]any
			for _, broker := range brokers {
				bal, err := broker.Balance(ctx)
				if err != nil {
					results = append(results, map[string]any{
						"venue":  string(broker.Venue()),
						"error":  err.Error(),
					})
					continue
				}
				entry := map[string]any{
					"venue":       string(bal.Venue),
					"total_usdt":  bal.TotalUSDT.String(),
					"free_usdt":   bal.FreeUSDT.String(),
					"locked_usdt": bal.LockedUSDT.String(),
				}
				if len(bal.Assets) > 0 {
					var assets []map[string]string
					for _, a := range bal.Assets {
						assets = append(assets, map[string]string{
							"asset":  a.Asset,
							"free":   a.Free.String(),
							"locked": a.Locked.String(),
						})
					}
					entry["assets"] = assets
				}
				results = append(results, entry)
			}
			if len(results) == 0 {
				return json.Marshal(map[string]any{"message": "No brokers configured."})
			}
			return json.Marshal(results)
		},
		Definition: port.ToolDefinition{
			Name:        "get_account_balance",
			Description: "Get current account balance (USDT free/locked/total) for all configured exchange venues. Use this to answer questions about available funds, account equity, or buying power.",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
		},
	}
}

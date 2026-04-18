package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/azhar/cerebro/internal/port"
)

// GetDerivativesData implements the get_derivatives_data agent tool.
func GetDerivativesData(feed port.DerivativesFeed) port.Tool {
	return port.Tool{
		Handler: func(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
			var args struct {
				Symbol string `json:"symbol"`
			}
			if err := json.Unmarshal(input, &args); err != nil {
				return nil, fmt.Errorf("get_derivatives_data: bad args: %w", err)
			}
			if args.Symbol == "" {
				return nil, fmt.Errorf("get_derivatives_data: symbol is required")
			}

			sym := normalizeToolSymbol(args.Symbol)
			snap, err := feed.Snapshot(ctx, sym)
			if err != nil {
				return nil, fmt.Errorf("get_derivatives_data: %w", err)
			}
			return json.Marshal(snap)
		},
		Definition: port.ToolDefinition{
			Name:        "get_derivatives_data",
			Description: "Fetch derivatives data (funding rate, open interest, liquidations, fear & greed) for a symbol. Requires CoinGlass API. Accepts any common format: BTCUSDT, BTC/USDT, BTC/USDT-PERP, BTC.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"symbol": map[string]any{
						"type":        "string",
						"description": "Trading symbol in any format: BTCUSDT, BTC/USDT, BTC/USDT-PERP, BTC",
					},
				},
				"required": []string{"symbol"},
			},
		},
	}
}

package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/azhar/cerebro/internal/domain"
	"github.com/azhar/cerebro/internal/marketdata"
	"github.com/azhar/cerebro/internal/port"
)

// QuoteProvider wraps the Hub's latest-quote lookup into a simple function
// signature so the tool doesn't import the Hub directly in tests.
type QuoteProvider func(symbol domain.Symbol) (domain.Quote, bool)

// QuoteProviderFromHub creates a QuoteProvider from a Hub.
func QuoteProviderFromHub(hub *marketdata.Hub) QuoteProvider {
	return hub.LatestQuote
}

// GetMarketData implements the get_market_data agent tool.
// It reads real-time prices from the WebSocket feed — always available,
// no external API key required.
func GetMarketData(lookup QuoteProvider) port.Tool {
	return port.Tool{
		Handler: func(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
			var args struct {
				Symbol string `json:"symbol"`
			}
			if err := json.Unmarshal(input, &args); err != nil {
				return nil, fmt.Errorf("get_market_data: bad args: %w", err)
			}
			if args.Symbol == "" {
				return nil, fmt.Errorf("get_market_data: symbol is required")
			}

			sym := normalizeToolSymbol(args.Symbol)
			q, ok := lookup(sym)
			if !ok {
				return json.Marshal(map[string]any{
					"symbol":  string(sym),
					"message": "No live market data available for this symbol. It may not be in the active markets config.",
				})
			}

			return json.Marshal(map[string]any{
				"symbol":               string(q.Symbol),
				"last_price":           q.Last.String(),
				"bid":                  q.Bid.String(),
				"ask":                  q.Ask.String(),
				"mid":                  q.Mid.String(),
				"price_change_24h":     q.PriceChange.String(),
				"price_change_pct_24h": q.PriceChangePercent.String(),
				"volume_24h":           q.Volume24h.String(),
				"timestamp":            q.Timestamp.UTC().Format("2006-01-02T15:04:05Z"),
			})
		},
		Definition: port.ToolDefinition{
			Name:        "get_market_data",
			Description: "Get real-time market data (price, 24h change, volume) for a symbol from the live WebSocket feed. Always available — no external API required. Use this as a baseline for every analysis. Accepts any symbol format: BTCUSDT, BTC/USDT, XAU/USDT-PERP.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"symbol": map[string]any{
						"type":        "string",
						"description": "Symbol in any format: XAUUSDT, XAU/USDT-PERP, BTC/USDT",
					},
				},
				"required": []string{"symbol"},
			},
		},
	}
}

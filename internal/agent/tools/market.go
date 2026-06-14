package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

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

// QuoteFallback fetches a one-shot 24h quote for symbols not present on the
// live WebSocket hub (typically discovery-surfaced symbols outside
// markets.yaml). Adapters return (zero, false, nil) when the symbol is
// unknown to that venue; an error is returned only on transient failures.
type QuoteFallback func(ctx context.Context, sym domain.Symbol) (domain.Quote, bool, error)

// GetMarketData implements the get_market_data agent tool.
// It reads real-time prices from the WebSocket feed — always available,
// no external API key required. When `fallback` is non-nil, hub misses
// trigger a REST lookup so discovery-surfaced symbols still resolve.
func GetMarketData(lookup QuoteProvider, fallback QuoteFallback) port.Tool {
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
			source := "hub"
			if !ok && fallback != nil {
				fbCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
				fbQuote, fbOk, fbErr := fallback(fbCtx, sym)
				cancel()
				if fbErr != nil {
					slog.Debug("get_market_data: fallback failed",
						"symbol", sym, "error", fbErr)
				} else if fbOk {
					q = fbQuote
					ok = true
					source = "rest_fallback"
				}
			}
			if !ok {
				return json.Marshal(map[string]any{
					"symbol":  string(sym),
					"message": "No live market data available for this symbol. It may not be in the active markets config or supported by the configured venue.",
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
				"source":               source,
			})
		},
		Definition: port.ToolDefinition{
			Name:        "get_market_data",
			Description: "Get real-time market data (price, 24h change, volume) for a symbol. Falls back to a Binance REST 24h ticker when the symbol is not on the live WebSocket feed (e.g. discovery candidates). Accepts any symbol format: BTCUSDT, BTC/USDT, XAU/USDT-PERP.",
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

package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/azhar/cerebro/internal/domain"
	"github.com/azhar/cerebro/internal/port"
)

// SymbolsProvider returns the current screening universe. It is resolved
// on each tool call so discovery-surfaced symbols are visible to the LLM
// without requiring a restart.
type SymbolsProvider func(ctx context.Context) []domain.Symbol

// GetAllMarketData returns real-time quotes for all monitored symbols at once.
// Use this for cross-symbol comparison and relative strength analysis.
//
// The symbol set is resolved lazily on each invocation via SymbolsProvider,
// so discovery-surfaced symbols (Phase 0) appear alongside the static
// markets.yaml universe automatically.
func GetAllMarketData(lookup QuoteProvider, symbolsFn SymbolsProvider) port.Tool {
	return port.Tool{
		Handler: func(ctx context.Context, _ json.RawMessage) (json.RawMessage, error) {
			type quoteEntry struct {
				Symbol           string `json:"symbol"`
				LastPrice        string `json:"last_price,omitempty"`
				Bid              string `json:"bid,omitempty"`
				Ask              string `json:"ask,omitempty"`
				Mid              string `json:"mid,omitempty"`
				PriceChangePct24 string `json:"price_change_pct_24h,omitempty"`
				Volume24h        string `json:"volume_24h,omitempty"`
				Available        bool   `json:"available"`
			}

			symbols := symbolsFn(ctx)
			entries := make([]quoteEntry, 0, len(symbols))
			for _, sym := range symbols {
				q, ok := lookup(sym)
				if !ok {
					entries = append(entries, quoteEntry{
						Symbol:    string(sym),
						Available: false,
					})
					continue
				}
				entries = append(entries, quoteEntry{
					Symbol:           string(q.Symbol),
					LastPrice:        q.Last.String(),
					Bid:              q.Bid.String(),
					Ask:              q.Ask.String(),
					Mid:              q.Mid.String(),
					PriceChangePct24: q.PriceChangePercent.String(),
					Volume24h:        q.Volume24h.String(),
					Available:        true,
				})
			}

			out, err := json.Marshal(map[string]any{
				"symbols": entries,
				"count":   len(entries),
			})
			if err != nil {
				return nil, fmt.Errorf("get_all_market_data: marshal: %w", err)
			}
			return out, nil
		},
		Definition: port.ToolDefinition{
			Name:        "get_all_market_data",
			Description: "Get real-time market data for ALL monitored symbols at once. Use this for cross-symbol comparison and relative strength analysis. No input parameters required.",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		},
	}
}

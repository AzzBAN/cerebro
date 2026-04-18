package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/azhar/cerebro/internal/port"
)

// FetchLatestNews implements the fetch_latest_news agent tool.
func FetchLatestNews(feed port.NewsFeed) port.Tool {
	return port.Tool{
		Handler: func(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
			var args struct {
				Query string `json:"query"`
				Limit int    `json:"limit"`
			}
			if err := json.Unmarshal(input, &args); err != nil {
				return nil, fmt.Errorf("fetch_latest_news: bad args: %w", err)
			}
			if args.Limit <= 0 {
				args.Limit = 10
			}
			items, err := feed.FetchLatest(ctx, args.Query, args.Limit)
			if err != nil {
				return nil, fmt.Errorf("fetch_latest_news: %w", err)
			}
			if len(items) == 0 {
				return json.Marshal(map[string]any{
					"items":  []port.NewsItem{},
					"message": "No news found for the given query. Try broader keywords like 'crypto', 'BTC', or 'market'.",
				})
			}
			return json.Marshal(map[string]any{"items": items})
		},
		Definition: port.ToolDefinition{
			Name:        "fetch_latest_news",
			Description: "Search for recent news headlines. Pass any relevant keywords as the query — asset names (BTC, ETH, XAU, GOLD), market topics (rates, inflation, ETF), or events (halving, FOMC, regulation). Try multiple searches with different keywords for comprehensive coverage.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query": map[string]any{
						"type":        "string",
						"description": "Search keywords: asset tickers (BTC, ETH, XAU), topics (inflation, ETF, regulation), or events (FOMC, halving). Use specific and broad queries for best results.",
					},
					"limit": map[string]any{
						"type":        "integer",
						"description": "Max number of items to return (default 10)",
					},
				},
			},
		},
	}
}

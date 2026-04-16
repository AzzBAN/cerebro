package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/azhar/cerebro/internal/port"
)

// FetchLatestNews implements the fetch_latest_news agent tool.
// Input: { "asset": "BTC", "limit": 10 }
func FetchLatestNews(feed port.NewsFeed) port.ToolHandler {
	return func(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
		var args struct {
			Asset string `json:"asset"`
			Limit int    `json:"limit"`
		}
		if err := json.Unmarshal(input, &args); err != nil {
			return nil, fmt.Errorf("fetch_latest_news: bad args: %w", err)
		}
		if args.Limit <= 0 {
			args.Limit = 10
		}
		items, err := feed.FetchLatest(ctx, args.Asset, args.Limit)
		if err != nil {
			return nil, fmt.Errorf("fetch_latest_news: %w", err)
		}
		return json.Marshal(items)
	}
}

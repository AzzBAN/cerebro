package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/azhar/cerebro/internal/port"
)

// GetEconomicEvents implements the get_economic_events agent tool.
func GetEconomicEvents(feed port.CalendarFeed) port.Tool {
	return port.Tool{
		Handler: func(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
			var args struct {
				Hours int `json:"hours"`
			}
			if err := json.Unmarshal(input, &args); err != nil {
				return nil, fmt.Errorf("get_economic_events: bad args: %w", err)
			}
			if args.Hours <= 0 {
				args.Hours = 24
			}
			events, err := feed.UpcomingEvents(ctx, args.Hours)
			if err != nil {
				return nil, fmt.Errorf("get_economic_events: %w", err)
			}
			return json.Marshal(events)
		},
		Definition: port.ToolDefinition{
			Name:        "get_economic_events",
			Description: "Fetch upcoming economic calendar events (GDP, CPI, NFP, etc.) for the next N hours.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"hours": map[string]any{
						"type":        "integer",
						"description": "Number of hours ahead to look (default 24)",
					},
				},
			},
		},
	}
}

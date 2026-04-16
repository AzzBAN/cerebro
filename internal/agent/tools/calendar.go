package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/azhar/cerebro/internal/port"
)

// GetEconomicEvents implements the get_economic_events agent tool.
// Input: { "hours": 24 }
func GetEconomicEvents(feed port.CalendarFeed) port.ToolHandler {
	return func(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
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
	}
}

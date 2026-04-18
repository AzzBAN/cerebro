package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/azhar/cerebro/internal/port"
)

// QueryAgentLogs implements the query_agent_logs agent tool.
func QueryAgentLogs(store port.AgentLogStore) port.Tool {
	return port.Tool{
		Handler: func(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
			var args struct {
				Agent      string `json:"agent"`
				TimeWindow string `json:"time_window"`
			}
			if err := json.Unmarshal(input, &args); err != nil {
				return nil, fmt.Errorf("query_agent_logs: bad args: %w", err)
			}

			duration, err := time.ParseDuration(args.TimeWindow)
			if err != nil {
				duration = time.Hour
			}

			from := time.Now().UTC().Add(-duration)
			to := time.Now().UTC()

			runs, err := store.RunsByWindow(ctx, args.Agent, from, to)
			if err != nil {
				return nil, fmt.Errorf("query_agent_logs: %w", err)
			}

			return json.Marshal(runs)
		},
		Definition: port.ToolDefinition{
			Name:        "query_agent_logs",
			Description: "Query past agent invocation logs within a time window.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"agent": map[string]any{
						"type":        "string",
						"description": "Agent name, e.g. screening, copilot, reviewer",
					},
					"time_window": map[string]any{
						"type":        "string",
						"description": "Go duration string, e.g. 1h, 24h",
					},
				},
			},
		},
	}
}

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/azhar/cerebro/internal/port"
)

// QueryAgentLogs implements the query_agent_logs agent tool.
// Available to Copilot.
// Input: { "agent": "screening", "time_window": "1h" }
func QueryAgentLogs(store port.AgentLogStore) func(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
	return func(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
		var args struct {
			Agent      string `json:"agent"`
			TimeWindow string `json:"time_window"` // e.g. "1h", "24h"
		}
		if err := json.Unmarshal(input, &args); err != nil {
			return nil, fmt.Errorf("query_agent_logs: bad args: %w", err)
		}

		duration, err := time.ParseDuration(args.TimeWindow)
		if err != nil {
			duration = time.Hour // default
		}

		from := time.Now().UTC().Add(-duration)
		to := time.Now().UTC()

		runs, err := store.RunsByWindow(ctx, args.Agent, from, to)
		if err != nil {
			return nil, fmt.Errorf("query_agent_logs: %w", err)
		}

		return json.Marshal(runs)
	}
}

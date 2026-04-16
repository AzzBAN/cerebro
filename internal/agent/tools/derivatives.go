package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/azhar/cerebro/internal/domain"
	"github.com/azhar/cerebro/internal/port"
)

// GetDerivativesData implements the get_derivatives_data agent tool.
// Allowed only for the Screening Agent (enforced by tool policy).
// Input: { "symbol": "BTCUSDT" }
// Output: JSON-serialised domain.DerivativesSnapshot (from Redis cache; direct API fallback).
func GetDerivativesData(feed port.DerivativesFeed) port.ToolHandler {
	return func(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
		var args struct {
			Symbol string `json:"symbol"`
		}
		if err := json.Unmarshal(input, &args); err != nil {
			return nil, fmt.Errorf("get_derivatives_data: bad args: %w", err)
		}
		if args.Symbol == "" {
			return nil, fmt.Errorf("get_derivatives_data: symbol is required")
		}

		snap, err := feed.Snapshot(ctx, domain.Symbol(args.Symbol))
		if err != nil {
			return nil, fmt.Errorf("get_derivatives_data: %w", err)
		}
		return json.Marshal(snap)
	}
}

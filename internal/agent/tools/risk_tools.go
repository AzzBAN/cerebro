package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/azhar/cerebro/internal/risk"
	"github.com/shopspring/decimal"
)

// GetCurrentDrawdown implements the get_current_drawdown agent tool.
func GetCurrentDrawdown(gate *risk.Gate) func() func(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
	return func() func(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
		return func(_ context.Context, _ json.RawMessage) (json.RawMessage, error) {
			halted := gate.IsHalted()
			result := map[string]any{
				"halted": halted,
				// Phase 4+ will expose real session PnL from the gate.
				"session_pnl_usd": "0",
				"daily_pnl_usd":   "0",
			}
			return json.Marshal(result)
		}
	}
}

// CalculatePositionSize implements the calculate_position_size agent tool.
// Input: { "risk_pct": 0.5, "stop_loss_distance": 150.0, "equity": 10000.0, "entry_price": 43000.0 }
func CalculatePositionSize() func(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
	return func(_ context.Context, input json.RawMessage) (json.RawMessage, error) {
		var args struct {
			RiskPct          float64 `json:"risk_pct"`
			StopLossDistance float64 `json:"stop_loss_distance"`
			Equity           float64 `json:"equity"`
			EntryPrice       float64 `json:"entry_price"`
		}
		if err := json.Unmarshal(input, &args); err != nil {
			return nil, fmt.Errorf("calculate_position_size: bad args: %w", err)
		}
		params, err := risk.CalculatePositionSize(
			decimal.NewFromFloat(args.Equity),
			args.RiskPct,
			decimal.NewFromFloat(args.EntryPrice),
			decimal.NewFromFloat(args.EntryPrice-args.StopLossDistance),
			decimal.Zero,
			decimal.Zero,
			decimal.Zero,
		)
		if err != nil {
			return nil, fmt.Errorf("calculate_position_size: %w", err)
		}
		return json.Marshal(map[string]any{
			"quantity":          params.Quantity.String(),
			"risk_amount_quote": params.RiskAmountQuote.String(),
		})
	}
}

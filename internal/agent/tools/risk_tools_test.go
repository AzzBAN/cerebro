package tools

import (
	"context"
	"encoding/json"
	"testing"
)

func TestCalculatePositionSize_ReturnsLeverageAndLiquidation(t *testing.T) {
	tool := CalculatePositionSize()

	input := map[string]any{
		"equity":             1000.0,
		"risk_pct":           1.0,
		"entry_price":        100.0,
		"stop_loss_distance": 2.0,
		"side":               "buy",
	}
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}

	outRaw, err := tool.Handler(context.Background(), raw)
	if err != nil {
		t.Fatalf("tool handler failed: %v", err)
	}

	var res map[string]any
	if err := json.Unmarshal(outRaw, &res); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}

	if res["quantity"] == nil || res["quantity"] == "" {
		t.Errorf("expected quantity in output, got nil")
	}
	if res["max_safe_leverage"] == nil {
		t.Errorf("expected max_safe_leverage in output")
	}
	if res["recommended_leverage"] == nil {
		t.Errorf("expected recommended_leverage in output")
	}
	if res["estimated_liquidation_price"] == nil {
		t.Errorf("expected estimated_liquidation_price in output")
	}
	if res["required_margin"] == nil {
		t.Errorf("expected required_margin in output")
	}
}

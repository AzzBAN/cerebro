package tools

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/azhar/cerebro/internal/domain"
	"github.com/shopspring/decimal"
)

// captureRouter records the most recent AgentOrderRequest so tests can
// assert on the exact contract received by the execution layer.
type captureRouter struct {
	last AgentOrderRequest
	err  error
}

func (c *captureRouter) route(_ context.Context, req AgentOrderRequest) error {
	c.last = req
	return c.err
}

func TestApproveAndRouteOrder_MarketDefault(t *testing.T) {
	cr := &captureRouter{}
	tool := ApproveAndRouteOrder(cr.route)

	input, _ := json.Marshal(map[string]any{
		"symbol": "BTC/USDT",
		"side":   "buy",
		"size":   0.1,
	})
	if _, err := tool.Handler(context.Background(), input); err != nil {
		t.Fatalf("handler: %v", err)
	}

	if cr.last.OrderType != domain.OrderTypeMarket {
		t.Errorf("default order_type = %q, want market", cr.last.OrderType)
	}
	if !cr.last.StopLoss.IsZero() || !cr.last.TakeProfit1.IsZero() {
		t.Errorf("unexpected bracket on market-only call: %+v", cr.last)
	}
}

func TestApproveAndRouteOrder_LimitWithBracket(t *testing.T) {
	cr := &captureRouter{}
	tool := ApproveAndRouteOrder(cr.route)

	input, _ := json.Marshal(map[string]any{
		"symbol":        "BTC/USDT",
		"side":          "buy",
		"size":          0.1,
		"order_type":    "limit",
		"limit_price":   65000,
		"time_in_force": "gtc",
		"stop_loss":     63000,
		"take_profit":   68000,
	})
	out, err := tool.Handler(context.Background(), input)
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	if cr.last.OrderType != domain.OrderTypeLimit {
		t.Errorf("order_type = %q, want limit", cr.last.OrderType)
	}
	if !cr.last.LimitPrice.Equal(decimal.NewFromInt(65000)) {
		t.Errorf("limit_price = %s, want 65000", cr.last.LimitPrice)
	}
	if !cr.last.StopLoss.Equal(decimal.NewFromInt(63000)) {
		t.Errorf("stop_loss = %s, want 63000", cr.last.StopLoss)
	}
	if !cr.last.TakeProfit1.Equal(decimal.NewFromInt(68000)) {
		t.Errorf("take_profit = %s, want 68000", cr.last.TakeProfit1)
	}

	// Handler reports bracket presence back to the LLM.
	var resp map[string]any
	if err := json.Unmarshal(out, &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp["has_bracket"] != true {
		t.Errorf("expected has_bracket=true in response, got %v", resp["has_bracket"])
	}
}

func TestApproveAndRouteOrder_StopLimitValidation(t *testing.T) {
	cr := &captureRouter{}
	tool := ApproveAndRouteOrder(cr.route)

	input, _ := json.Marshal(map[string]any{
		"symbol":     "BTC/USDT",
		"side":       "buy",
		"size":       0.1,
		"order_type": "stop_limit",
		// Missing both limit_price and stop_price.
	})
	if _, err := tool.Handler(context.Background(), input); err == nil {
		t.Fatalf("expected error for stop_limit without prices")
	}

	// With both prices → accepted.
	input, _ = json.Marshal(map[string]any{
		"symbol":      "BTC/USDT",
		"side":        "buy",
		"size":        0.1,
		"order_type":  "stop_limit",
		"limit_price": 65000,
		"stop_price":  64900,
	})
	if _, err := tool.Handler(context.Background(), input); err != nil {
		t.Fatalf("valid stop_limit rejected: %v", err)
	}
	if cr.last.OrderType != domain.OrderTypeStopLimit {
		t.Errorf("got %q, want stop_limit", cr.last.OrderType)
	}
	if !cr.last.StopPrice.Equal(decimal.NewFromInt(64900)) {
		t.Errorf("stop_price = %s, want 64900", cr.last.StopPrice)
	}
}

func TestApproveAndRouteOrder_FuturesFields(t *testing.T) {
	cr := &captureRouter{}
	tool := ApproveAndRouteOrder(cr.route)

	input, _ := json.Marshal(map[string]any{
		"symbol":        "BTC/USDT-PERP",
		"side":          "sell",
		"size":          0.5,
		"reduce_only":   true,
		"position_side": "short",
		"leverage":      10,
	})
	if _, err := tool.Handler(context.Background(), input); err != nil {
		t.Fatalf("handler: %v", err)
	}

	if !cr.last.ReduceOnly {
		t.Errorf("reduce_only not propagated")
	}
	if cr.last.PositionSide != domain.PositionSideShort {
		t.Errorf("position_side = %q, want short", cr.last.PositionSide)
	}
	if cr.last.Leverage != 10 {
		t.Errorf("leverage = %d, want 10", cr.last.Leverage)
	}
}

func TestApproveAndRouteOrder_RejectsBadArgs(t *testing.T) {
	cr := &captureRouter{}
	tool := ApproveAndRouteOrder(cr.route)

	tests := []struct {
		name  string
		input map[string]any
	}{
		{"missing symbol", map[string]any{"side": "buy", "size": 1.0}},
		{"zero size", map[string]any{"symbol": "BTC/USDT", "side": "buy", "size": 0}},
		{"bad side", map[string]any{"symbol": "BTC/USDT", "side": "flat", "size": 1.0}},
		{"bad order_type", map[string]any{"symbol": "BTC/USDT", "side": "buy", "size": 1.0, "order_type": "iceberg"}},
		{"limit without price", map[string]any{"symbol": "BTC/USDT", "side": "buy", "size": 1.0, "order_type": "limit"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input, _ := json.Marshal(tt.input)
			if _, err := tool.Handler(context.Background(), input); err == nil {
				t.Errorf("expected error for %q", tt.name)
			}
		})
	}
}

func TestApproveAndRouteOrder_PropagatesRouteError(t *testing.T) {
	wantErr := errors.New("boom")
	cr := &captureRouter{err: wantErr}
	tool := ApproveAndRouteOrder(cr.route)

	input, _ := json.Marshal(map[string]any{
		"symbol": "BTC/USDT", "side": "buy", "size": 0.1,
	})
	_, err := tool.Handler(context.Background(), input)
	if err == nil || !errors.Is(err, wantErr) {
		t.Errorf("expected wrapped error, got %v", err)
	}
}

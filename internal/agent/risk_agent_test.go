package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
	"github.com/azhar/cerebro/internal/agent/tools"
	"github.com/azhar/cerebro/internal/config"
	"github.com/azhar/cerebro/internal/domain"
	"github.com/azhar/cerebro/internal/port"
	"github.com/shopspring/decimal"
)

// decisionLLM simulates the real tool-calling loop: when Complete runs it
// invokes one named decision tool from the supplied set (mirroring what the
// model does), then returns scripted final text. This lets us assert that
// Evaluate derives its verdict from the tool that actually fired rather than
// blindly trusting a successful completion.
type decisionLLM struct {
	toolToCall string          // registry name of the tool to invoke; "" = none
	input      json.RawMessage // args passed to that tool
	reply      string          // final assistant text
	err        error           // when set, Complete fails (LLM error path)
}

func (l *decisionLLM) Complete(ctx context.Context, _ string, _ string, ts map[string]port.Tool) (string, error) {
	if l.toolToCall != "" {
		if t, ok := ts[l.toolToCall]; ok {
			_, _ = t.Handler(ctx, l.input)
		}
	}
	if l.err != nil {
		return "", l.err
	}
	return l.reply, nil
}

func (l *decisionLLM) Provider() string { return "stub" }
func (l *decisionLLM) ModelID() string  { return "stub-model" }

// stubAudit captures audit events without persisting.
type stubAudit struct{ events []domain.AuditEvent }

func (s *stubAudit) SaveEvent(_ context.Context, e domain.AuditEvent) error {
	s.events = append(s.events, e)
	return nil
}

func newTestRiskAgent(llm port.LLM, agentTools map[string]port.Tool) *RiskAgent {
	rt := NewRuntime(llm, noopLogStore{}, config.AgentConfig{MaxTurns: 1, TimeoutTotalSeconds: 5})
	return NewRiskAgent(rt, agentTools)
}

// riskToolset builds the decision tool set the way the registry would, with a
// route function that records whether an order was actually routed.
func riskToolset(routed *bool) (map[string]port.Tool, *stubAudit) {
	audit := &stubAudit{}
	routeFn := func(_ context.Context, _ tools.AgentOrderRequest) error {
		*routed = true
		return nil
	}
	return map[string]port.Tool{
		"reject_signal":           tools.RejectSignal(audit),
		"approve_and_route_order": tools.ApproveAndRouteOrder(routeFn),
		"resize_and_route_order":  tools.ResizeAndRouteOrder(routeFn, audit),
	}, audit
}

func testSignal() domain.Signal {
	return domain.Signal{
		ID:       "sig-1",
		Symbol:   "XAU/USDT-PERP",
		Side:     domain.SideBuy,
		Strategy: "mean_reversion",
		Reason:   "test",
	}
}

func TestRiskAgent_Evaluate_Verdict(t *testing.T) {
	approveArgs, _ := json.Marshal(map[string]any{
		"symbol": "XAU/USDT-PERP", "side": "buy", "size": 1.0, "reason": "ok",
	})
	// Bracket fields supplied by the LLM must reach the route function
	// verbatim — this is the regression guard for the "no SL/TP levels
	// to attach" flatten race on BTC/USDT-PERP / SOL/USDT-PERP.
	approveArgsWithBracket, _ := json.Marshal(map[string]any{
		"symbol":      "XAU/USDT-PERP",
		"side":        "buy",
		"size":        1.0,
		"reason":      "ok",
		"stop_loss":   2900.0,
		"take_profit": 3100.0,
	})
	resizeArgs, _ := json.Marshal(map[string]any{
		"symbol": "XAU/USDT-PERP", "side": "buy",
		"original_size": 2.0, "resized_size": 1.0, "reason": "trim",
	})
	rejectArgs, _ := json.Marshal(map[string]any{"reason": "incomplete parameters"})

	tests := []struct {
		name         string
		tool         string
		input        json.RawMessage
		reply        string
		wantApproved bool
		wantErr      bool
		wantRouted   bool
		wantSL       float64 // 0 = don't assert
		wantTP       float64 // 0 = don't assert
	}{
		{
			name:         "reject tool fired returns not-approved",
			tool:         "reject_signal",
			input:        rejectArgs,
			reply:        "I've rejected the signal due to incomplete parameters.",
			wantApproved: false,
			wantRouted:   false,
		},
		{
			name:         "approve tool fired returns approved",
			tool:         "approve_and_route_order",
			input:        approveArgs,
			reply:        "approved and routed",
			wantApproved: true,
			wantRouted:   true,
		},
		{
			name:         "resize tool fired returns approved",
			tool:         "resize_and_route_order",
			input:        resizeArgs,
			reply:        "resized and routed",
			wantApproved: true,
			wantRouted:   true,
		},
		{
			name:         "no decision tool fails closed without routing",
			tool:         "",
			reply:        "let me think about this some more",
			wantApproved: false,
			wantRouted:   false,
		},
		{
			name:         "approve with bracket passes SL/TP through",
			tool:         "approve_and_route_order",
			input:        approveArgsWithBracket,
			reply:        "approved with bracket",
			wantApproved: true,
			wantRouted:   true,
			wantSL:       2900.0,
			wantTP:       3100.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var routedReq tools.AgentOrderRequest
			var routed bool
			audit := &stubAudit{}
			toolset := map[string]port.Tool{
				"reject_signal": tools.RejectSignal(audit),
				"approve_and_route_order": tools.ApproveAndRouteOrder(func(_ context.Context, req tools.AgentOrderRequest) error {
					routedReq = req
					routed = true
					return nil
				}),
				"resize_and_route_order": tools.ResizeAndRouteOrder(
					func(_ context.Context, req tools.AgentOrderRequest) error {
						routedReq = req
						routed = true
						return nil
					}, audit),
			}
			llm := &decisionLLM{toolToCall: tt.tool, input: tt.input, reply: tt.reply}
			ra := newTestRiskAgent(llm, toolset)

			approved, err := ra.Evaluate(context.Background(), testSignal(), nil)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Evaluate() err = %v, wantErr %v", err, tt.wantErr)
			}
			if approved != tt.wantApproved {
				t.Errorf("Evaluate() approved = %v, want %v", approved, tt.wantApproved)
			}
			if routed != tt.wantRouted {
				t.Errorf("order routed = %v, want %v", routed, tt.wantRouted)
			}
			if tt.wantSL != 0 {
				if got := routedReq.StopLoss.InexactFloat64(); got != tt.wantSL {
					t.Errorf("StopLoss = %v, want %v", got, tt.wantSL)
				}
			}
			if tt.wantTP != 0 {
				if got := routedReq.TakeProfit1.InexactFloat64(); got != tt.wantTP {
					t.Errorf("TakeProfit1 = %v, want %v", got, tt.wantTP)
				}
			}
		})
	}
}

func TestRiskAgent_Evaluate_LLMErrorFailsClosed(t *testing.T) {
	var routed bool
	toolset, _ := riskToolset(&routed)
	llm := &decisionLLM{err: errors.New("provider down")}
	ra := newTestRiskAgent(llm, toolset)

	approved, err := ra.Evaluate(context.Background(), testSignal(), nil)
	if err == nil {
		t.Fatal("Evaluate() err = nil, want non-nil on LLM failure")
	}
	if approved {
		t.Error("Evaluate() approved = true, want false on LLM failure")
	}
	if routed {
		t.Error("order routed on LLM failure, want no routing")
	}
}

func TestRiskAgent_Evaluate_OrderAlreadyRoutedOnError(t *testing.T) {
	var routed bool
	toolset, _ := riskToolset(&routed)
	approveArgs, _ := json.Marshal(map[string]any{
		"symbol":    "XAU/USDT-PERP",
		"side":      "BUY",
		"size":      0.1,
		"stop_loss": 2900.0,
	})
	llm := &decisionLLM{
		toolToCall: "approve_and_route_order",
		input:      approveArgs,
		err:        errors.New("timeout on final assistant turn"),
	}
	ra := newTestRiskAgent(llm, toolset)

	approved, err := ra.Evaluate(context.Background(), testSignal(), nil)
	if !approved {
		t.Error("Evaluate() approved = false, want true when order already routed")
	}
	if !errors.Is(err, ErrOrderAlreadyRouted) {
		t.Errorf("Evaluate() err = %v, want ErrOrderAlreadyRouted", err)
	}
	if !routed {
		t.Error("order was not routed by tool")
	}
}

// stubQuoteSource satisfies QuoteSource for buildUserMessage tests.
type stubQuoteSource struct {
	quote domain.Quote
	ok    bool
}

func (s *stubQuoteSource) LatestQuote(domain.Symbol) (domain.Quote, bool) {
	return s.quote, s.ok
}

func TestRiskAgent_BuildUserMessage_IncludesEntryPriceAndATR(t *testing.T) {
	ra := newTestRiskAgent(&decisionLLM{}, riskToolsetOnly(nil))
	ra.SetQuoteSource(&stubQuoteSource{
		quote: domain.Quote{Symbol: "XAU/USDT-PERP", Last: decimal.NewFromInt(3000)},
		ok:    true,
	})

	sig := testSignal()
	sig.Reason = "scalp long: EMA8/21 golden cross, RSI=55.4, ATR=2.7197"

	msg := ra.buildUserMessage(context.Background(), sig, sig.Reason, nil)
	if !strings.Contains(msg, "entry_price=3000") {
		t.Errorf("user message missing entry price: %s", msg)
	}
	if !strings.Contains(msg, "atr=2.7197") {
		t.Errorf("user message missing ATR hint: %s", msg)
	}
	if !strings.Contains(msg, "with stop_loss") {
		t.Errorf("user message should instruct LLM to pass stop_loss: %s", msg)
	}
}

func TestRiskAgent_BuildUserMessage_NoQuoteOmitsEntryPrice(t *testing.T) {
	ra := newTestRiskAgent(&decisionLLM{}, riskToolsetOnly(nil))

	sig := testSignal()
	sig.Reason = "scalp long: ATR=2.7197"

	msg := ra.buildUserMessage(context.Background(), sig, sig.Reason, nil)
	if strings.Contains(msg, "entry_price=") {
		t.Errorf("user message must not include entry_price when no quote wired: %s", msg)
	}
	if !strings.Contains(msg, "atr=2.7197") {
		t.Errorf("user message must still include ATR from reason: %s", msg)
	}
}
func TestRiskAgent_BuildUserMessage_IncludesMarketBias(t *testing.T) {
	ra := newTestRiskAgent(&decisionLLM{}, riskToolsetOnly(nil))
	cache := newMemCache()
	bias := domain.BiasResult{
		Symbol:    "BTC/USDT-PERP",
		Score:     domain.BiasBullish,
		Reasoning: "Strong volume and rising OI",
	}
	bBytes, _ := json.Marshal(bias)
	_ = cache.Set(context.Background(), "bias:BTC/USDT-PERP", bBytes, 0)
	ra.SetCache(cache)

	sig := domain.Signal{
		Symbol:   "BTC/USDT-PERP",
		Side:     domain.SideSell,
		Strategy: "mean_reversion",
		Reason:   "RSI overbought",
	}
	msg := ra.buildUserMessage(context.Background(), sig, sig.Reason, nil)
	if !strings.Contains(msg, "market_bias=Bullish") {
		t.Errorf("user message should include market_bias=Bullish, got: %s", msg)
	}
	if !strings.Contains(msg, "bias_score=1") {
		t.Errorf("user message should include bias_score=1, got: %s", msg)
	}
}
type stubRiskTradeStore struct {
	trades []domain.Trade
}

func (s *stubRiskTradeStore) SaveIntent(_ context.Context, _ domain.OrderIntent) error { return nil }
func (s *stubRiskTradeStore) UpdateIntentStatus(_ context.Context, _ string, _ domain.OrderStatus, _ string) error {
	return nil
}
func (s *stubRiskTradeStore) SaveTrade(_ context.Context, _ domain.Trade) error { return nil }
func (s *stubRiskTradeStore) TradesByWindow(_ context.Context, _, _ time.Time) ([]domain.Trade, error) {
	return s.trades, nil
}
func (s *stubRiskTradeStore) IntentsByWindow(_ context.Context, _, _ time.Time) ([]domain.OrderIntent, error) {
	return nil, nil
}

func TestRiskAgent_InjectPerformanceContext_IncludesRecentLosingLessons(t *testing.T) {
	loss := decimal.RequireFromString("-15.50")
	price := decimal.RequireFromString("82700.00")
	ts := &stubRiskTradeStore{
		trades: []domain.Trade{
			{
				Symbol:    "BTC/USDT-PERP",
				Side:      domain.SideSell,
				Strategy:  "mean_reversion",
				FillPrice: price,
				PnL:       &loss,
				CreatedAt: time.Now().Add(-2 * time.Hour),
			},
		},
	}

	ra := NewRiskAgentWithPerf(
		NewRuntime(&decisionLLM{}, noopLogStore{}, config.AgentConfig{MaxTurns: 1, TimeoutTotalSeconds: 5}),
		riskToolsetOnly(nil),
		ts,
	)

	prompt := ra.injectPerformanceContextSystem(context.Background(), 7, "Base System Prompt")
	if !strings.Contains(prompt, "Recent Strategy Performance") {
		t.Fatalf("expected Recent Strategy Performance section, got: %s", prompt)
	}
	if !strings.Contains(prompt, "Lessons from Recent Losing Trades") {
		t.Errorf("expected Lessons from Recent Losing Trades section, got: %s", prompt)
	}
	if !strings.Contains(prompt, "BTC/USDT-PERP") || !strings.Contains(prompt, "-15.5") {
		t.Errorf("expected losing trade details in lessons, got: %s", prompt)
	}
}



// riskToolsetOnly is a slimmer helper that returns just the three decision
// tools (used by buildUserMessage tests which never invoke the LLM).
func riskToolsetOnly(audit *stubAudit) map[string]port.Tool {
	if audit == nil {
		audit = &stubAudit{}
	}
	return map[string]port.Tool{
		"reject_signal":           tools.RejectSignal(audit),
		"approve_and_route_order": tools.ApproveAndRouteOrder(func(_ context.Context, _ tools.AgentOrderRequest) error { return nil }),
		"resize_and_route_order":  tools.ResizeAndRouteOrder(func(_ context.Context, _ tools.AgentOrderRequest) error { return nil }, audit),
	}
}

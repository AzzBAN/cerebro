package agent

import (
	"strings"
	"testing"
	"time"

	"github.com/azhar/cerebro/internal/config"
	"github.com/azhar/cerebro/internal/domain"
	"github.com/shopspring/decimal"
)

// ── parsePMResponse ───────────────────────────────────────────────────────────

func TestParsePMResponse(t *testing.T) {
	tests := []struct {
		name      string
		raw       string
		wantDec   domain.ActionDecision
		wantNewSL float64
		wantErr   bool
	}{
		{
			name:    "HOLD",
			raw:     `{"action":"HOLD","symbol":"BTCUSDT","reasoning":"no trigger","confidence":0.8}`,
			wantDec: domain.ActionHold,
		},
		{
			name:      "MOVE_STOP",
			raw:       `{"action":"MOVE_STOP","symbol":"BTCUSDT","reasoning":"trail","new_stop":98000.5,"confidence":0.7}`,
			wantDec:   domain.ActionTightenStop,
			wantNewSL: 98000.5,
		},
		{
			name:    "FLATTEN maps to ActionClose",
			raw:     `{"action":"FLATTEN","symbol":"BTCUSDT","reasoning":"news spike","confidence":0.9}`,
			wantDec: domain.ActionClose,
		},
		{
			name:    "PARTIAL_CLOSE maps to ActionClose",
			raw:     `{"action":"PARTIAL_CLOSE","symbol":"BTCUSDT","reasoning":"TP1 hit","confidence":0.85}`,
			wantDec: domain.ActionClose,
		},
		{
			name:    "FLIP maps to ActionFlip",
			raw:     `{"action":"FLIP","symbol":"BTCUSDT","reasoning":"decisive reversal","confidence":0.7}`,
			wantDec: domain.ActionFlip,
		},
		{
			name:    "unknown action returns error",
			raw:     `{"action":"TELEPORT","symbol":"BTCUSDT","reasoning":"nonsense"}`,
			wantErr: true,
		},
		{
			name:    "invalid JSON returns error",
			raw:     `{"action":"HOLD"`,
			wantErr: true,
		},
		{
			name:    "empty string returns error",
			raw:     ``,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			action, err := parsePMResponse(tt.raw)
			if (err != nil) != tt.wantErr {
				t.Fatalf("parsePMResponse() err = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if action.Decision != tt.wantDec {
				t.Errorf("decision = %q, want %q", action.Decision, tt.wantDec)
			}
			if tt.wantNewSL != 0 {
				want := decimal.NewFromFloat(tt.wantNewSL)
				if !action.NewStopLoss.Equal(want) {
					t.Errorf("NewStopLoss = %s, want %s", action.NewStopLoss, want)
				}
			}
		})
	}
}

// ── renderPMUserMsg ───────────────────────────────────────────────────────────

func TestRenderPMUserMsg_ContainsSymbol(t *testing.T) {
	review := domain.PositionReview{
		Position: domain.Position{
			Symbol:       "ETHUSDT",
			Venue:        domain.VenueBinanceFutures,
			Side:         domain.SideBuy,
			EntryPrice:   decimal.NewFromInt(3000),
			CurrentPrice: decimal.NewFromInt(3100),
			StopLoss:     decimal.NewFromInt(2900),
			TakeProfit1:  decimal.NewFromInt(3300),
			Quantity:     decimal.NewFromFloat(0.5),
			OpenedAt:     time.Now().Add(-2 * time.Hour),
		},
	}

	msg, err := renderPMUserMsg(review)
	if err != nil {
		t.Fatalf("renderPMUserMsg() error = %v", err)
	}
	if !strings.Contains(msg, "ETHUSDT") {
		t.Error("rendered message does not contain symbol ETHUSDT")
	}
	if !strings.Contains(msg, "3000") {
		t.Error("rendered message does not contain entry price")
	}
}

func TestRenderPMUserMsg_ZeroOpenedAt(t *testing.T) {
	// OpenedAt zero should not cause a negative hours value or panic.
	review := domain.PositionReview{
		Position: domain.Position{
			Symbol:       "BTCUSDT",
			Venue:        domain.VenueBinanceFutures,
			Side:         domain.SideBuy,
			EntryPrice:   decimal.NewFromInt(100000),
			CurrentPrice: decimal.NewFromInt(101000),
			Quantity:     decimal.NewFromInt(1),
		},
	}

	_, err := renderPMUserMsg(review)
	if err != nil {
		t.Fatalf("renderPMUserMsg() with zero OpenedAt error = %v", err)
	}
}

// ── llmFallback ───────────────────────────────────────────────────────────────

func TestPositionManagerAgent_FallbackHold(t *testing.T) {
	agent := &PositionManagerAgent{
		cfg: config.PositionManagerConfig{LLMFailureAction: "hold"},
	}
	pos := domain.Position{
		Symbol:     "BTCUSDT",
		EntryPrice: decimal.NewFromInt(100000),
	}
	action := agent.llmFallback(pos, "test reason")
	if action.Decision != domain.ActionHold {
		t.Errorf("expected ActionHold, got %q", action.Decision)
	}
}

func TestPositionManagerAgent_FallbackTightenBreakeven(t *testing.T) {
	agent := &PositionManagerAgent{
		cfg: config.PositionManagerConfig{LLMFailureAction: "tighten_breakeven"},
	}
	entry := decimal.NewFromInt(50000)
	pos := domain.Position{
		Symbol:     "BTCUSDT",
		EntryPrice: entry,
	}
	action := agent.llmFallback(pos, "test reason")
	if action.Decision != domain.ActionTightenStop {
		t.Errorf("expected ActionTightenStop, got %q", action.Decision)
	}
	if !action.NewStopLoss.Equal(entry) {
		t.Errorf("NewStopLoss = %s, want %s", action.NewStopLoss, entry)
	}
}

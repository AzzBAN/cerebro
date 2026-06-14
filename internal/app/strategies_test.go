package app

import (
	"testing"

	"github.com/azhar/cerebro/internal/config"
	"github.com/shopspring/decimal"
)

// TestApplyTechOnlyMultiplier verifies the safety envelope around the
// technical-only fallback position-size reducer. Invalid / disabled
// configurations must leave the quantity unchanged, a valid (0,1)
// multiplier must actually shrink the size.
func TestApplyTechOnlyMultiplier(t *testing.T) {
	qty := decimal.NewFromFloat(0.1) // baseline position size

	tests := []struct {
		name       string
		cfg        config.LLMConfig
		wantQtyStr string // decimal string we expect
	}{
		{
			name:       "fallback disabled → no change",
			cfg:        config.LLMConfig{TechnicalOnlyFallback: false, TechnicalOnlySizeMultiplier: 0.5},
			wantQtyStr: "0.1",
		},
		{
			name:       "fallback on, multiplier 0.5 → half",
			cfg:        config.LLMConfig{TechnicalOnlyFallback: true, TechnicalOnlySizeMultiplier: 0.5},
			wantQtyStr: "0.05",
		},
		{
			name:       "fallback on, multiplier 0.25 → quarter",
			cfg:        config.LLMConfig{TechnicalOnlyFallback: true, TechnicalOnlySizeMultiplier: 0.25},
			wantQtyStr: "0.025",
		},
		{
			name:       "multiplier unset (0) → no change (safety)",
			cfg:        config.LLMConfig{TechnicalOnlyFallback: true, TechnicalOnlySizeMultiplier: 0},
			wantQtyStr: "0.1",
		},
		{
			name:       "multiplier 1.0 → no-op (no amplify)",
			cfg:        config.LLMConfig{TechnicalOnlyFallback: true, TechnicalOnlySizeMultiplier: 1.0},
			wantQtyStr: "0.1",
		},
		{
			name:       "multiplier >=1 → rejected, no amplify",
			cfg:        config.LLMConfig{TechnicalOnlyFallback: true, TechnicalOnlySizeMultiplier: 1.5},
			wantQtyStr: "0.1",
		},
		{
			name:       "negative multiplier → rejected",
			cfg:        config.LLMConfig{TechnicalOnlyFallback: true, TechnicalOnlySizeMultiplier: -0.5},
			wantQtyStr: "0.1",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := applyTechOnlyMultiplier(qty, tc.cfg)
			want := decimal.RequireFromString(tc.wantQtyStr)
			if !got.Equal(want) {
				t.Errorf("applyTechOnlyMultiplier(%s, %+v) = %s; want %s",
					qty, tc.cfg, got, want)
			}
		})
	}
}

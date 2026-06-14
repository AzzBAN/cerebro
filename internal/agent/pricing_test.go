package agent

import "testing"

func TestLookupPricing(t *testing.T) {
	tests := []struct {
		model       string
		wantInput   float64 // 0 when unknown
	}{
		{"claude-haiku-4-5", 1.00},
		{"claude-haiku-4-5-20250514", 1.00}, // prefix match survives versioned IDs
		{"claude-3-haiku-20240307", 0.25},
		{"CLAUDE-SONNET-4", 3.00}, // case-insensitive
		{"gemini-1.5-flash", 0.075},
		{"gemini-2.5-flash", 0.30},
		{"gpt-4o-mini", 0.15},
		{"minimax-m2.5", 0.30},
		{"some-unknown-model", 0},
		{"", 0},
	}
	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			got := LookupPricing(tt.model)
			if got.InputPerMTok != tt.wantInput {
				t.Errorf("InputPerMTok = %v, want %v", got.InputPerMTok, tt.wantInput)
			}
		})
	}
}

func TestPricing_EstimateCostMicroUSD(t *testing.T) {
	t.Run("unknown model returns 0", func(t *testing.T) {
		if got := (Pricing{}).EstimateCostMicroUSD(1_000_000, 500_000, 0); got != 0 {
			t.Fatalf("unknown pricing must return 0, got %d", got)
		}
	})

	t.Run("claude-haiku 1M input + 1M output", func(t *testing.T) {
		p := LookupPricing("claude-haiku-4-5")
		// 1M input @ $1 + 1M output @ $5 = $6 = 6_000_000 μUSD
		got := p.EstimateCostMicroUSD(1_000_000, 1_000_000, 0)
		if got < 5_950_000 || got > 6_050_000 { // allow rounding fuzz
			t.Fatalf("expected ~6_000_000 μUSD, got %d", got)
		}
	})

	t.Run("cached portion is discounted", func(t *testing.T) {
		p := LookupPricing("claude-haiku-4-5")
		// 1M input, 900k cached (@ $0.10/M = $0.09) + 100k uncached (@ $1/M = $0.10)
		// + 100k output (@ $5/M = $0.50) = $0.69 total = 690_000 μUSD.
		got := p.EstimateCostMicroUSD(1_000_000, 100_000, 900_000)
		if got < 680_000 || got > 700_000 {
			t.Fatalf("expected ~690_000 μUSD with cache hit, got %d", got)
		}
		// Sanity: paying full price for everything would be ~$1.50.
		uncached := p.EstimateCostMicroUSD(1_000_000, 100_000, 0)
		if uncached <= got {
			t.Errorf("uncached cost (%d) should exceed cached cost (%d)", uncached, got)
		}
	})

	t.Run("cached exceeding input is clamped", func(t *testing.T) {
		p := LookupPricing("claude-haiku-4-5")
		// pathological case: cached > input. Uncached must clamp to 0,
		// cached is billed, result stays finite and non-negative.
		got := p.EstimateCostMicroUSD(1000, 0, 5000)
		if got < 0 {
			t.Fatalf("clamped estimate must be non-negative, got %d", got)
		}
	})

	t.Run("zero tokens is 0 μUSD", func(t *testing.T) {
		p := LookupPricing("claude-haiku-4-5")
		if got := p.EstimateCostMicroUSD(0, 0, 0); got != 0 {
			t.Fatalf("zero tokens must return 0, got %d", got)
		}
	})

	// Regression: the old int-cent estimator rounded sub-penny calls to 0,
	// losing cost data entirely for cheap models. The μUSD estimator must
	// register a non-zero value for a small call on a cheap model.
	t.Run("sub-cent calls are captured", func(t *testing.T) {
		p := LookupPricing("gemini-2.5-flash") // $0.30/M input, $2.50/M output
		// 500 input + 500 output tokens = (500*0.30 + 500*2.50) = 1400 μUSD
		// (= $0.0014 = 0.14 cents — would have rounded to 0 cents).
		got := p.EstimateCostMicroUSD(500, 500, 0)
		if got == 0 {
			t.Fatalf("small cheap-model call must register a cost, got 0 μUSD")
		}
		if got < 1_000 || got > 2_000 {
			t.Errorf("expected ~1_400 μUSD, got %d", got)
		}
	})
}

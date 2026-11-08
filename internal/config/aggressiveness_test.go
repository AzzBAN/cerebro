package config

import (
	"testing"

	"github.com/azhar/cerebro/internal/domain"
)

func baseStrategy() StrategyConfig {
	return StrategyConfig{
		Name:                     "mean_reversion",
		Enabled:                  true,
		RiskPctPerTrade:          0.5,
		MaxPositionSizePct:       4.0,
		SignalDedupWindowSeconds: 900,
		ConfirmationCandles:      1,
		RequireBiasAlignment:     false,
	}
}

func TestNormalizeAggressiveness(t *testing.T) {
	tests := []struct {
		name    string
		in      domain.Aggressiveness
		wantLvl domain.Aggressiveness
		wantOK  bool
	}{
		{"empty defaults to balanced", "", domain.AggressivenessBalanced, true},
		{"conservative", domain.AggressivenessConservative, domain.AggressivenessConservative, true},
		{"balanced", domain.AggressivenessBalanced, domain.AggressivenessBalanced, true},
		{"aggressive", domain.AggressivenessAggressive, domain.AggressivenessAggressive, true},
		{"unknown", "reckless", "reckless", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lvl, ok := normalizeAggressiveness(tt.in)
			if lvl != tt.wantLvl || ok != tt.wantOK {
				t.Errorf("normalizeAggressiveness(%q) = (%q,%v), want (%q,%v)",
					tt.in, lvl, ok, tt.wantLvl, tt.wantOK)
			}
		})
	}
}

func TestApplyAggressiveness_Balanced_IsIdentity(t *testing.T) {
	cfg := &Config{
		Aggressiveness: domain.AggressivenessBalanced,
		Strategies:     StrategiesConfig{Strategies: []StrategyConfig{baseStrategy()}},
	}
	applyAggressiveness(cfg)

	got := cfg.Strategies.Strategies[0]
	want := baseStrategy()
	if got.RiskPctPerTrade != want.RiskPctPerTrade ||
		got.MaxPositionSizePct != want.MaxPositionSizePct ||
		got.SignalDedupWindowSeconds != want.SignalDedupWindowSeconds ||
		got.ConfirmationCandles != want.ConfirmationCandles ||
		got.RequireBiasAlignment != want.RequireBiasAlignment {
		t.Errorf("balanced changed values: got %+v want %+v", got, want)
	}
}

func TestApplyAggressiveness_EmptyNormalisesToBalanced(t *testing.T) {
	cfg := &Config{
		Strategies: StrategiesConfig{Strategies: []StrategyConfig{baseStrategy()}},
	}
	applyAggressiveness(cfg)

	if cfg.Aggressiveness != domain.AggressivenessBalanced {
		t.Errorf("empty aggressiveness should normalise to balanced, got %q", cfg.Aggressiveness)
	}
	if cfg.Strategies.Strategies[0].RiskPctPerTrade != 0.5 {
		t.Errorf("empty (balanced) should not scale risk, got %v",
			cfg.Strategies.Strategies[0].RiskPctPerTrade)
	}
}

func TestApplyAggressiveness_Conservative(t *testing.T) {
	cfg := &Config{
		Aggressiveness: domain.AggressivenessConservative,
		Strategies:     StrategiesConfig{Strategies: []StrategyConfig{baseStrategy()}},
	}
	applyAggressiveness(cfg)
	got := cfg.Strategies.Strategies[0]

	if got.RiskPctPerTrade != 0.25 { // 0.5 * 0.5
		t.Errorf("risk_pct: got %v want 0.25", got.RiskPctPerTrade)
	}
	if got.MaxPositionSizePct != 2.4 { // 4.0 * 0.6
		t.Errorf("max_pos: got %v want 2.4", got.MaxPositionSizePct)
	}
	if got.SignalDedupWindowSeconds != 1800 { // 900 * 2.0
		t.Errorf("dedup: got %v want 1800", got.SignalDedupWindowSeconds)
	}
	if got.ConfirmationCandles != 2 { // 1 + 1
		t.Errorf("confirm: got %v want 2", got.ConfirmationCandles)
	}
	if !got.RequireBiasAlignment {
		t.Error("conservative must force require_bias_alignment on")
	}
}

func TestApplyAggressiveness_Aggressive(t *testing.T) {
	cfg := &Config{
		Aggressiveness: domain.AggressivenessAggressive,
		Strategies:     StrategiesConfig{Strategies: []StrategyConfig{baseStrategy()}},
	}
	applyAggressiveness(cfg)
	got := cfg.Strategies.Strategies[0]

	if got.RiskPctPerTrade != 0.875 { // 0.5 * 1.75
		t.Errorf("risk_pct: got %v want 0.875", got.RiskPctPerTrade)
	}
	if got.MaxPositionSizePct != 6.0 { // 4.0 * 1.5
		t.Errorf("max_pos: got %v want 6.0", got.MaxPositionSizePct)
	}
	if got.SignalDedupWindowSeconds != 450 { // 900 * 0.5
		t.Errorf("dedup: got %v want 450", got.SignalDedupWindowSeconds)
	}
	if got.ConfirmationCandles != 0 { // 1 - 1
		t.Errorf("confirm: got %v want 0", got.ConfirmationCandles)
	}
}

// Scaling must never produce risk_pct_per_trade > max_position_size_pct,
// which Validate() would reject. A strategy already at its cap stays clamped.
func TestApplyAggressiveness_ClampsRiskToCap(t *testing.T) {
	s := baseStrategy()
	s.RiskPctPerTrade = 4.0 // equal to the cap
	s.MaxPositionSizePct = 4.0
	cfg := &Config{
		Aggressiveness: domain.AggressivenessAggressive,
		Strategies:     StrategiesConfig{Strategies: []StrategyConfig{s}},
	}
	applyAggressiveness(cfg)
	got := cfg.Strategies.Strategies[0]

	// risk would be 7.0 (4*1.75), cap scales to 6.0 (4*1.5) → clamp to 6.0.
	if got.RiskPctPerTrade > got.MaxPositionSizePct {
		t.Errorf("risk_pct %v exceeds max_pos %v after scaling",
			got.RiskPctPerTrade, got.MaxPositionSizePct)
	}
}

// Disabled strategies must be left untouched by the dial.
func TestApplyAggressiveness_SkipsDisabled(t *testing.T) {
	s := baseStrategy()
	s.Enabled = false
	cfg := &Config{
		Aggressiveness: domain.AggressivenessAggressive,
		Strategies:     StrategiesConfig{Strategies: []StrategyConfig{s}},
	}
	applyAggressiveness(cfg)

	if cfg.Strategies.Strategies[0].RiskPctPerTrade != 0.5 {
		t.Errorf("disabled strategy was scaled: got %v want 0.5",
			cfg.Strategies.Strategies[0].RiskPctPerTrade)
	}
}

// An unknown level leaves strategies untouched (Validate surfaces the error).
func TestApplyAggressiveness_UnknownLeavesUntouched(t *testing.T) {
	cfg := &Config{
		Aggressiveness: "reckless",
		Strategies:     StrategiesConfig{Strategies: []StrategyConfig{baseStrategy()}},
	}
	applyAggressiveness(cfg)

	if cfg.Strategies.Strategies[0].RiskPctPerTrade != 0.5 {
		t.Errorf("unknown level should not scale, got %v",
			cfg.Strategies.Strategies[0].RiskPctPerTrade)
	}
	if err := validateAggressiveness(cfg.Aggressiveness); err == nil {
		t.Error("validateAggressiveness should reject unknown level")
	}
}

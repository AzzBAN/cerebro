package config

import (
	"fmt"

	"github.com/azhar/cerebro/internal/domain"
)

// aggressivenessProfile holds the multipliers and deltas applied to every
// enabled strategy's entry parameters for a given aggressiveness level.
//
// The transform is multiplicative so it layers on top of each strategy's own
// tuning rather than replacing it: a strategy that was already conservative
// stays comparatively conservative within the chosen level.
//
// Only entry-side behaviour is scaled. The global `risk:` hard caps and the
// risk gate (internal/risk) are never touched — aggressiveness cannot widen a
// safety limit, only how the strategies behave inside those limits.
type aggressivenessProfile struct {
	// riskPctMul scales risk_pct_per_trade (position size per signal).
	riskPctMul float64
	// maxPosSizeMul scales the per-strategy max_position_size_pct cap.
	maxPosSizeMul float64
	// dedupWindowMul scales signal_dedup_window_seconds. Values < 1 shorten
	// the window (more frequent re-entries); > 1 lengthen it.
	dedupWindowMul float64
	// confirmDelta is added to confirmation_candles (floored at 0). Negative
	// values enter sooner; positive values wait for more confirmation.
	confirmDelta int
	// forceBiasAlignment, when true, forces require_bias_alignment on so the
	// LLM screening bias must agree before any entry. Used by the conservative
	// level only; the more aggressive levels leave the configured value alone.
	forceBiasAlignment bool
}

// aggressivenessProfiles maps each level to its scaling profile. Balanced is
// the identity transform, so an unset or balanced config behaves exactly as
// the raw strategies.yaml values.
var aggressivenessProfiles = map[domain.Aggressiveness]aggressivenessProfile{
	domain.AggressivenessConservative: {
		riskPctMul:         0.5,
		maxPosSizeMul:      0.6,
		dedupWindowMul:     2.0,
		confirmDelta:       1,
		forceBiasAlignment: true,
	},
	domain.AggressivenessBalanced: {
		riskPctMul:     1.0,
		maxPosSizeMul:  1.0,
		dedupWindowMul: 1.0,
		confirmDelta:   0,
	},
	domain.AggressivenessAggressive: {
		riskPctMul:     1.75,
		maxPosSizeMul:  1.5,
		dedupWindowMul: 0.5,
		confirmDelta:   -1,
	},
}

// normalizeAggressiveness resolves an empty value to Balanced and reports
// whether the level is recognised.
func normalizeAggressiveness(a domain.Aggressiveness) (domain.Aggressiveness, bool) {
	if a == "" {
		return domain.AggressivenessBalanced, true
	}
	if _, ok := aggressivenessProfiles[a]; ok {
		return a, true
	}
	return a, false
}

// applyAggressiveness scales every enabled strategy's entry parameters by the
// profile for cfg.Aggressiveness. It is a no-op for the balanced level (and for
// an unset value, which normalises to balanced).
//
// The function mutates cfg.Strategies in place and must run after
// loadStrategies but before Validate, so the validator sees (and can reject)
// the post-scaling values.
func applyAggressiveness(cfg *Config) {
	level, ok := normalizeAggressiveness(cfg.Aggressiveness)
	if !ok {
		// Unknown level: leave strategies untouched. Validate() surfaces the
		// error to the operator rather than silently picking a profile.
		return
	}
	cfg.Aggressiveness = level

	prof := aggressivenessProfiles[level]
	if prof == (aggressivenessProfile{
		riskPctMul: 1.0, maxPosSizeMul: 1.0, dedupWindowMul: 1.0,
	}) {
		return // balanced identity — nothing to scale
	}

	for i := range cfg.Strategies.Strategies {
		s := &cfg.Strategies.Strategies[i]
		if !s.Enabled {
			continue
		}
		applyProfileToStrategy(s, prof)
	}
}

// applyProfileToStrategy scales a single strategy's entry parameters and
// preserves the config invariant that risk_pct_per_trade never exceeds
// max_position_size_pct (the validator enforces this; we keep it true by
// construction so scaling can't produce an invalid config).
func applyProfileToStrategy(s *StrategyConfig, prof aggressivenessProfile) {
	if s.RiskPctPerTrade > 0 {
		s.RiskPctPerTrade *= prof.riskPctMul
	}
	if s.MaxPositionSizePct > 0 {
		s.MaxPositionSizePct *= prof.maxPosSizeMul
	}
	// Clamp risk to the (scaled) per-strategy cap so we never emit a config
	// the validator would reject.
	if s.MaxPositionSizePct > 0 && s.RiskPctPerTrade > s.MaxPositionSizePct {
		s.RiskPctPerTrade = s.MaxPositionSizePct
	}

	if s.SignalDedupWindowSeconds > 0 {
		scaled := float64(s.SignalDedupWindowSeconds) * prof.dedupWindowMul
		s.SignalDedupWindowSeconds = int(scaled + 0.5) // round to nearest second
	}

	s.ConfirmationCandles += prof.confirmDelta
	if s.ConfirmationCandles < 0 {
		s.ConfirmationCandles = 0
	}

	if prof.forceBiasAlignment {
		s.RequireBiasAlignment = true
	}
}

// validateAggressiveness reports an error when the configured level is set but
// not one of the recognised values.
func validateAggressiveness(a domain.Aggressiveness) error {
	if _, ok := normalizeAggressiveness(a); !ok {
		return fmt.Errorf("aggressiveness (%q) must be one of conservative|balanced|aggressive", a)
	}
	return nil
}

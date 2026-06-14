package screening

import (
	"sort"

	"github.com/azhar/cerebro/internal/domain"
)

// StrategyFit represents a candidate ↔ strategy match with a confidence
// score. Higher confidence means the regime + features align with the
// strategy's preconditions more strongly.
type StrategyFit struct {
	Strategy   domain.StrategyName
	Confidence float64 // 0..1
	Reasons    []string
}

// regimePref maps a regime to the strategies it favours, in priority
// order. The map is the single source of truth for "which strategy
// suits which regime"; the matcher then filters by the operator's
// `enabled` set so it never recommends an off strategy.
//
// The presets must be defined in strategies.yaml (see configs).
// `funding_arb`, `squeeze_fade`, `breakout_continuation`, `liq_magnet`
// are thin yaml-only variants of the three executable engines
// (mean_reversion, trend_following, volatility_breakout).
var regimePref = map[domain.Regime][]domain.StrategyName{
	domain.RegimeTrending: {"trend_following", "breakout_continuation"},
	domain.RegimeBreakout: {"volatility_breakout", "breakout_continuation", "trend_following"},
	domain.RegimeRange:    {"mean_reversion"},
	domain.RegimeSqueeze:  {"squeeze_fade", "mean_reversion", "funding_arb"},
	domain.RegimeLiqHunt:  {"liq_magnet", "volatility_breakout"},
}

// MatchStrategies returns up to `topN` strategy fits for the given
// candidate, restricted to the operator's `enabled` set. The result is
// sorted by confidence (descending). Empty result means "no fit"; the
// caller should drop the candidate or surface it as advisory only.
//
// Confidence formula combines:
//   - regime base (0.6 for the top match in regimePref, 0.4 for second, ...)
//   - feature alignment bonus (e.g. extreme funding in a squeeze pushes
//     squeeze_fade up, big move in a trend pushes trend_following up)
//
// All bonuses are clamped to [0, 1] so confidence never exceeds 1.
func MatchStrategies(f Features, regime domain.Regime, side domain.Side, enabled map[domain.StrategyName]bool, topN int) []StrategyFit {
	prefs := regimePref[regime]
	if len(prefs) == 0 {
		return nil
	}

	enabledSet := enabled
	if enabledSet == nil {
		enabledSet = map[domain.StrategyName]bool{}
	}

	out := make([]StrategyFit, 0, len(prefs))
	for i, name := range prefs {
		if len(enabledSet) > 0 && !enabledSet[name] {
			continue
		}
		base := 0.6 - 0.1*float64(i)
		if base < 0.2 {
			base = 0.2
		}
		bonus, reasons := alignmentBonus(f, regime, side, name)
		conf := base + bonus
		if conf > 1 {
			conf = 1
		}
		out = append(out, StrategyFit{
			Strategy:   name,
			Confidence: conf,
			Reasons:    reasons,
		})
	}

	sort.SliceStable(out, func(i, j int) bool {
		return out[i].Confidence > out[j].Confidence
	})
	if topN > 0 && len(out) > topN {
		out = out[:topN]
	}
	return out
}

// alignmentBonus computes a [0, 0.4] bonus based on how strongly the
// candidate's features line up with the strategy's textbook
// preconditions. It also returns short human-readable reasons that the
// operator sees in the Telegram report.
//
// The bonuses are intentionally conservative; the regime classifier
// already encodes the bulk of the signal. This function only adds tie-
// breakers and surfaces the reasoning.
func alignmentBonus(f Features, regime domain.Regime, side domain.Side, strat domain.StrategyName) (float64, []string) {
	var bonus float64
	var reasons []string

	abs := func(x float64) float64 {
		if x < 0 {
			return -x
		}
		return x
	}

	switch strat {
	case "trend_following", "breakout_continuation":
		if abs(f.PriceChangePct24) >= 6 {
			bonus += 0.15
			reasons = append(reasons, "Δ24h ≥ 6%")
		}
		if abs(f.OIChange24hPct) >= 5 {
			bonus += 0.1
			reasons = append(reasons, "OI Δ24h ≥ 5%")
		}
		if abs(f.FundingRate) <= 0.03 {
			bonus += 0.05
			reasons = append(reasons, "funding still neutral")
		}

	case "volatility_breakout":
		if abs(f.OIChange4hPct) >= 4 {
			bonus += 0.15
			reasons = append(reasons, "OI Δ4h ≥ 4%")
		}
		if f.IsNewListing {
			bonus += 0.1
			reasons = append(reasons, "fresh listing")
		}

	case "mean_reversion":
		if abs(f.PriceChangePct24) <= 1.5 {
			bonus += 0.1
			reasons = append(reasons, "quiet move")
		}
		if abs(f.LongShortRatio-1) >= 0.5 {
			bonus += 0.1
			reasons = append(reasons, "L/S lopsided")
		}

	case "squeeze_fade", "funding_arb":
		if abs(f.FundingRate) >= 0.08 {
			bonus += 0.2
			reasons = append(reasons, "funding extreme")
		}
		if abs(f.LongShortRatio-1) >= 0.6 {
			bonus += 0.1
			reasons = append(reasons, "crowd one-sided")
		}

	case "liq_magnet":
		if f.Liquidations24h >= 10_000_000 {
			bonus += 0.2
			reasons = append(reasons, "$10M+ liquidations 24h")
		}
		// Crowd that just got liquidated implies a snap-back in the
		// opposite direction — surface that to the operator.
		if (f.LiqLongRatio > 0.6 && side == domain.SideSell) ||
			(f.LiqLongRatio < 0.4 && side == domain.SideBuy) {
			bonus += 0.05
			reasons = append(reasons, "side aligns with recent liq skew")
		}
	}

	if bonus > 0.4 {
		bonus = 0.4
	}
	return bonus, reasons
}

// EnabledFromList builds the lookup map MatchStrategies expects from a
// flat slice of strategy names (typically `cfg.Strategies` enabled set).
func EnabledFromList(names []domain.StrategyName) map[domain.StrategyName]bool {
	out := make(map[domain.StrategyName]bool, len(names))
	for _, n := range names {
		out[n] = true
	}
	return out
}

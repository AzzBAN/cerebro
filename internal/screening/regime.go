package screening

import (
	"math"

	"github.com/azhar/cerebro/internal/domain"
)

// Thresholds groups the tunable cutoffs the regime classifier uses.
// Defaults are calibrated for the Binance USDT-M perp universe; tests
// pass thresholds explicitly so behaviour is deterministic.
type Thresholds struct {
	// Trend
	StrongMovePct     float64 // |Δ24h%|; below this we won't claim "trending"
	OIConfirmingPct   float64 // |OI Δ24h%| considered confirming
	FundingNeutralAbs float64 // |funding| above this is no longer "neutral"

	// Squeeze
	FundingExtremeAbs    float64 // |funding| above this = crowd over-extended
	LongShortLopsidedAbs float64 // |L/S − 1| above this = crowd lopsided

	// Liq hunt
	BigLiqUSD float64 // 24h liq total above this is a "big" magnet

	// Range
	QuietMovePct float64 // |Δ24h%| below this is considered range-bound
}

// DefaultThresholds returns production defaults.
func DefaultThresholds() Thresholds {
	return Thresholds{
		StrongMovePct:        4.0,
		OIConfirmingPct:      3.0,
		FundingNeutralAbs:    0.05, // 0.05% per 8h
		FundingExtremeAbs:    0.08,
		LongShortLopsidedAbs: 0.6, // ratio further than 0.6 from 1.0
		BigLiqUSD:            5_000_000,
		QuietMovePct:         2.0,
	}
}

// Classify assigns one Regime to a feature row using the given
// thresholds. The classifier is deliberately deterministic and pure —
// it returns the *first* matching regime in this priority order:
//
//  1. liq_hunt   — big nearby cluster dominates microstructure
//  2. squeeze    — extreme funding + crowded L/S + OI rising
//  3. breakout   — moderate move with OI confirming in same direction
//  4. trending   — strong move + OI confirming + funding still neutral
//  5. range      — quiet move (low |Δ24h|)
//  6. unknown    — none of the above (returned as RegimeUnknown)
//
// `Side` is implied by the regime + sign of Δ24h or funding (returned
// alongside) so the planner does not have to re-derive it.
func Classify(f Features, t Thresholds) (domain.Regime, domain.Side) {
	abs := func(x float64) float64 { return math.Abs(x) }

	// 1. liquidation hunt — the cluster acts as a magnet for either side
	if f.Liquidations24h >= t.BigLiqUSD {
		side := domain.SideBuy
		if f.LiqLongRatio > 0.55 {
			// More longs got liquidated → recent dominant side was selling.
			side = domain.SideSell
		}
		return domain.RegimeLiqHunt, side
	}

	// 2. squeeze — extreme funding and crowded one-sided positioning
	if abs(f.FundingRate) >= t.FundingExtremeAbs &&
		abs(f.LongShortRatio-1) >= t.LongShortLopsidedAbs {
		// Fade the crowd: when ratio>1 (longs crowded), short; vice versa.
		if f.LongShortRatio > 1 {
			return domain.RegimeSqueeze, domain.SideSell
		}
		return domain.RegimeSqueeze, domain.SideBuy
	}

	// 3. breakout — moderate move with OI confirming and BB likely expanding
	if abs(f.PriceChangePct24) >= t.StrongMovePct/2 &&
		abs(f.OIChange4hPct) >= t.OIConfirmingPct &&
		math.Signbit(f.PriceChangePct24) == math.Signbit(f.OIChange4hPct) {
		side := domain.SideBuy
		if f.PriceChangePct24 < 0 {
			side = domain.SideSell
		}
		return domain.RegimeBreakout, side
	}

	// 4. trending — strong sustained move + OI confirming on the 24h frame.
	// When no derivatives data is available at all (demo mode without
	// CoinGlass), we still tag a strong move as trending using price
	// alone — the matcher's confidence floor and the operator's eyes
	// catch the false positives. Without this fallback the planner
	// would produce zero plans whenever the scanner is absent.
	derivativesAvailable := f.OIChange24hPct != 0 || f.OIChange4hPct != 0 ||
		f.FundingRate != 0 || f.LongShortRatio != 0 || f.Liquidations24h != 0
	trendOK := false
	if derivativesAvailable {
		trendOK = abs(f.OIChange24hPct) >= t.OIConfirmingPct &&
			math.Signbit(f.PriceChangePct24) == math.Signbit(f.OIChange24hPct) &&
			abs(f.FundingRate) <= t.FundingNeutralAbs
	} else {
		trendOK = true // price-only fallback
	}
	if abs(f.PriceChangePct24) >= t.StrongMovePct && trendOK {
		side := domain.SideBuy
		if f.PriceChangePct24 < 0 {
			side = domain.SideSell
		}
		return domain.RegimeTrending, side
	}

	// 5. range — quiet enough to fade extremes
	if abs(f.PriceChangePct24) <= t.QuietMovePct {
		side := domain.SideBuy
		if f.PriceChangePct24 > 0 {
			side = domain.SideSell // mean-revert against the recent direction
		}
		return domain.RegimeRange, side
	}

	return domain.RegimeUnknown, domain.SideBuy
}

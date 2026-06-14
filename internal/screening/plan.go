package screening

import (
	"fmt"
	"time"

	"github.com/azhar/cerebro/internal/domain"
	"github.com/shopspring/decimal"
)

// PlanParams collects the deterministic price-derivation inputs
// BuildTradePlan needs. It mirrors a subset of config.StrategyConfig so
// callers can pass either a cached preset or a hard-coded default
// (used by tests and by the agent layer when a preset is missing).
type PlanParams struct {
	StopLossPct   float64 // 0.5 means 0.5% off entry; required, must be > 0
	TP1RR         float64 // first take-profit as a multiple of SL distance
	TP2RR         float64 // optional second TP; 0 disables
	EntryWidthPct float64 // half-width of the entry zone in %; 0 → 0.05% default
}

// DefaultPlanParams returns a sensible fallback. The agent layer
// overrides per-strategy via configs/strategies.yaml when available.
func DefaultPlanParams() PlanParams {
	return PlanParams{
		StopLossPct:   0.6,
		TP1RR:         1.8,
		TP2RR:         3.0,
		EntryWidthPct: 0.05,
	}
}

// BuildTradePlan turns a feature row + regime + matched strategy into a
// concrete plan with entry zone, SL and TP prices.
//
// All prices are deterministic functions of `LastPrice`, so the
// operator can verify them by hand. Side is honoured: BUY plans place
// entries below the last price (limit pull-back) and SL further below;
// SELL plans mirror.
//
// `bias` is the LLM screener's cached directional read (or
// BiasNeutral when missing). It is recorded on the plan for context
// but does NOT override the matcher's side — the matcher already
// derived side from microstructure.
func BuildTradePlan(
	f Features,
	regime domain.Regime,
	side domain.Side,
	fit StrategyFit,
	bias domain.BiasScore,
	params PlanParams,
	now time.Time,
) (domain.TradePlan, bool) {
	if f.LastPrice.IsZero() || params.StopLossPct <= 0 || params.TP1RR <= 0 {
		return domain.TradePlan{}, false
	}

	entryWidth := params.EntryWidthPct
	if entryWidth <= 0 {
		entryWidth = 0.05
	}

	last := f.LastPrice
	pct := func(p float64) decimal.Decimal { return last.Mul(decimal.NewFromFloat(p / 100)) }

	half := pct(entryWidth)
	slDist := pct(params.StopLossPct)
	tp1Dist := slDist.Mul(decimal.NewFromFloat(params.TP1RR))
	var tp2 decimal.Decimal
	if params.TP2RR > 0 {
		tp2Dist := slDist.Mul(decimal.NewFromFloat(params.TP2RR))
		if side == domain.SideBuy {
			tp2 = last.Add(tp2Dist)
		} else {
			tp2 = last.Sub(tp2Dist)
		}
	}

	var entryLow, entryHigh, sl, tp1 decimal.Decimal
	switch side {
	case domain.SideBuy:
		// limit pull-back: zone sits just below the last price
		entryHigh = last
		entryLow = last.Sub(half.Mul(decimal.NewFromInt(2)))
		sl = entryLow.Sub(slDist)
		tp1 = last.Add(tp1Dist)
	case domain.SideSell:
		entryLow = last
		entryHigh = last.Add(half.Mul(decimal.NewFromInt(2)))
		sl = entryHigh.Add(slDist)
		tp1 = last.Sub(tp1Dist)
	default:
		return domain.TradePlan{}, false
	}

	reasons := append([]string(nil), fit.Reasons...)
	reasons = append(reasons,
		fmt.Sprintf("regime=%s", regime),
		fmt.Sprintf("Δ24h=%+.2f%%", f.PriceChangePct24),
	)
	if f.FundingRate != 0 {
		reasons = append(reasons, fmt.Sprintf("funding=%+.4f%%", f.FundingRate*100))
	}
	if f.OIChange24hPct != 0 {
		reasons = append(reasons, fmt.Sprintf("OI24h=%+.2f%%", f.OIChange24hPct))
	}
	if f.LongShortRatio != 0 {
		reasons = append(reasons, fmt.Sprintf("L/S=%.2f", f.LongShortRatio))
	}

	return domain.TradePlan{
		Symbol:      f.Symbol,
		Venue:       f.Venue,
		BaseAsset:   f.BaseAsset,
		Regime:      regime,
		Strategy:    fit.Strategy,
		Bias:        bias,
		Side:        side,
		LastPrice:   last,
		EntryLow:    entryLow,
		EntryHigh:   entryHigh,
		StopLoss:    sl,
		TakeProfit1: tp1,
		TakeProfit2: tp2,
		RRRatio:     params.TP1RR,
		Confidence:  fit.Confidence,
		Reasoning:   reasons,
		GeneratedAt: now,
	}, true
}

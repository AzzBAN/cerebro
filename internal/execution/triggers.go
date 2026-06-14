package execution

import (
	"sync"
	"time"

	"github.com/azhar/cerebro/internal/domain"
	"github.com/shopspring/decimal"
)

// triggerKey uniquely identifies a (symbol, trigger-type) pair for debouncing.
type triggerKey struct {
	Symbol  domain.Symbol
	Trigger domain.TriggerType
}

// BiasFunc maps a symbol to its current BiasScore.
// Returns (BiasNeutral, false) when no bias is available for the symbol.
type BiasFunc func(domain.Symbol) (domain.BiasScore, bool)

// TriggerDetector inspects open positions and emits ReviewTriggers when
// conditions are met. Each (symbol, trigger-type) pair is debounced so the
// same trigger does not fire repeatedly within the configured window.
//
// Safe for concurrent use.
type TriggerDetector struct {
	debounceSec        int
	profitThresholdPct float64
	nearTPSLPct        float64
	biasFlipEnabled    bool

	mu       sync.Mutex
	lastFire map[triggerKey]time.Time
}

// NewTriggerDetector creates a TriggerDetector.
//   - debounceSec: minimum seconds between repeated fires for the same key.
//   - profitThresholdPct: unrealized PnL% at which TriggerProfitThreshold fires (0 = disabled).
//   - nearTPSLPct: distance-to-SL/TP as % of current price at which TriggerNearTPSL fires (0 = disabled).
//   - biasFlipEnabled: when true, TriggerBiasFlipAgainst fires when bias opposes the position side.
func NewTriggerDetector(debounceSec int, profitThresholdPct, nearTPSLPct float64, biasFlipEnabled bool) *TriggerDetector {
	return &TriggerDetector{
		debounceSec:        debounceSec,
		profitThresholdPct: profitThresholdPct,
		nearTPSLPct:        nearTPSLPct,
		biasFlipEnabled:    biasFlipEnabled,
		lastFire:           make(map[triggerKey]time.Time),
	}
}

// Detect evaluates each position against the configured rules and returns any
// triggers that pass the debounce gate. biasFor may be nil to skip bias checks.
func (d *TriggerDetector) Detect(positions []domain.Position, biasFor BiasFunc) []domain.ReviewTrigger {
	now := time.Now().UTC()
	var triggers []domain.ReviewTrigger

	for _, pos := range positions {
		// 1. Profit threshold
		if d.profitThresholdPct > 0 {
			pnlPct, _ := pos.UnrealizedPnLPct().Float64()
			if pnlPct >= d.profitThresholdPct {
				if t, ok := d.maybeEmit(pos, domain.TriggerProfitThreshold, now); ok {
					triggers = append(triggers, t)
				}
			}
		}

		// 2. Near TP/SL
		if d.nearTPSLPct > 0 && isNearTPSL(pos, d.nearTPSLPct) {
			if t, ok := d.maybeEmit(pos, domain.TriggerNearTPSL, now); ok {
				triggers = append(triggers, t)
			}
		}

		// 3. Bias flip against
		if d.biasFlipEnabled && biasFor != nil {
			if bias, ok := biasFor(pos.Symbol); ok && domain.BiasOpposesSide(bias, pos.Side) {
				if t, ok := d.maybeEmit(pos, domain.TriggerBiasFlipAgainst, now); ok {
					triggers = append(triggers, t)
				}
			}
		}
	}

	return triggers
}

// maybeEmit fires a trigger if the debounce window has elapsed for this key.
func (d *TriggerDetector) maybeEmit(pos domain.Position, trigType domain.TriggerType, now time.Time) (domain.ReviewTrigger, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()

	key := triggerKey{Symbol: pos.Symbol, Trigger: trigType}
	if last, fired := d.lastFire[key]; fired {
		if now.Sub(last) < time.Duration(d.debounceSec)*time.Second {
			return domain.ReviewTrigger{}, false
		}
	}
	d.lastFire[key] = now

	return domain.ReviewTrigger{
		Type:       trigType,
		Symbol:     pos.Symbol,
		Venue:      pos.Venue,
		Side:       pos.Side,
		DetectedAt: now,
	}, true
}

// isNearTPSL reports whether the position's current price is within pct% of
// either its stop-loss or take-profit level.
func isNearTPSL(pos domain.Position, pct float64) bool {
	if pos.CurrentPrice.IsZero() {
		return false
	}
	threshold := decimal.NewFromFloat(pct / 100.0)

	if !pos.StopLoss.IsZero() {
		dist := pos.CurrentPrice.Sub(pos.StopLoss).Abs()
		if dist.Div(pos.CurrentPrice).LessThanOrEqual(threshold) {
			return true
		}
	}
	if !pos.TakeProfit1.IsZero() {
		dist := pos.TakeProfit1.Sub(pos.CurrentPrice).Abs()
		if dist.Div(pos.CurrentPrice).LessThanOrEqual(threshold) {
			return true
		}
	}
	return false
}

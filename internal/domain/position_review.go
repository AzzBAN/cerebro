package domain

import (
	"fmt"
	"time"

	"github.com/shopspring/decimal"
)

// ActionDecision is the Position Manager agent's verdict on an open position.
type ActionDecision string

const (
	ActionHold        ActionDecision = "hold"
	ActionTightenStop ActionDecision = "tighten_stop"
	ActionClose       ActionDecision = "close"
	ActionFlip        ActionDecision = "flip"
)

// TriggerType identifies why a position was queued for review.
type TriggerType string

const (
	TriggerBiasFlipAgainst TriggerType = "bias_flip_against"
	TriggerProfitThreshold TriggerType = "profit_threshold"
	TriggerNearTPSL        TriggerType = "near_tp_sl"
)

// ReviewTrigger is a deterministic signal that an open position warrants a
// Position Manager evaluation. Produced by the reconciler, consumed by the
// agent. Carries no judgment — only the reason and the position it concerns.
type ReviewTrigger struct {
	Type       TriggerType
	Symbol     Symbol
	Venue      Venue
	Side       Side
	DetectedAt time.Time
}

// ManagedAction is the agent's decision for a reviewed position.
type ManagedAction struct {
	Decision    ActionDecision
	NewStopLoss decimal.Decimal // required when Decision == ActionTightenStop
	// CloseQuantity, when positive and Decision == ActionClose, closes only this
	// many units (a partial close). Zero or negative means close the full
	// position. Ignored for non-close decisions.
	CloseQuantity decimal.Decimal
	Reason        string
	Confidence    float64 // 0..1
}

// Validate checks the action is internally consistent.
func (a ManagedAction) Validate() error {
	switch a.Decision {
	case ActionHold, ActionClose, ActionFlip:
		return nil
	case ActionTightenStop:
		if a.NewStopLoss.IsZero() {
			return fmt.Errorf("tighten_stop action requires a non-zero NewStopLoss")
		}
		return nil
	default:
		return fmt.Errorf("unknown action decision %q", a.Decision)
	}
}

// BiasOpposesSide reports whether a bias score runs against an open position's
// side: Bullish opposes a SELL (short), Bearish opposes a BUY (long). Neutral
// never opposes.
func BiasOpposesSide(bias BiasScore, side Side) bool {
	switch bias {
	case BiasBullish:
		return side == SideSell
	case BiasBearish:
		return side == SideBuy
	default:
		return false
	}
}

// PositionReview is the full input the Position Manager agent needs to judge an
// open position. It lives in domain so both the execution package (which
// produces it) and the agent package (which consumes it) can reference it
// without an import cycle.
type PositionReview struct {
	Position           Position
	Trigger            ReviewTrigger
	BiasScore          BiasScore
	BiasReasoning      string
	IndicatorSummary   string
	PerformanceSummary string
}

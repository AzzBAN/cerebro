package domain

import (
	"time"

	"github.com/shopspring/decimal"
)

// Regime classifies a candidate's market microstructure. The screening
// pipeline tags every discovery row with one regime; the strategy
// matcher uses the tag to pick a best-fit strategy preset.
type Regime string

const (
	RegimeUnknown  Regime = ""
	RegimeTrending Regime = "trending" // sustained directional move + OI confirming
	RegimeRange    Regime = "range"    // small Δ24h, BB width normal, OI flat
	RegimeSqueeze  Regime = "squeeze"  // RSI extreme + L/S extreme + OI rising (mean-revert setup)
	RegimeBreakout Regime = "breakout" // BB compressed → expanding, OI rising in same direction
	RegimeLiqHunt  Regime = "liq_hunt" // large nearby liquidation cluster acts as price magnet
)

// TradePlan is the headline output of one screening cycle for a single
// candidate. It bundles a directional bias (from the LLM screener's
// Phase 1 cache when available, deterministic fallback otherwise) with
// concrete entry, stop and take-profit prices derived from the matched
// strategy preset.
//
// TradePlans are advisory. The risk gate still enforces the
// markets.yaml allow-list before any execution. Operators read the plan
// and decide whether to promote the symbol manually.
type TradePlan struct {
	Symbol      Symbol          `json:"symbol"`
	Venue       Venue           `json:"venue"`
	BaseAsset   string          `json:"base_asset"`
	Regime      Regime          `json:"regime"`
	Strategy    StrategyName    `json:"strategy"`
	Bias        BiasScore       `json:"bias"`
	Side        Side            `json:"side"`
	LastPrice   decimal.Decimal `json:"last_price"`
	EntryLow    decimal.Decimal `json:"entry_low"`
	EntryHigh   decimal.Decimal `json:"entry_high"`
	StopLoss    decimal.Decimal `json:"stop_loss"`
	TakeProfit1 decimal.Decimal `json:"take_profit_1"`
	TakeProfit2 decimal.Decimal `json:"take_profit_2,omitempty"`
	RRRatio     float64         `json:"rr_ratio"`
	Confidence  float64         `json:"confidence"` // 0..1 from matcher + features
	Reasoning   []string        `json:"reasoning"`
	GeneratedAt time.Time       `json:"generated_at"`
	ExpiresAt   time.Time       `json:"expires_at,omitempty"`
}

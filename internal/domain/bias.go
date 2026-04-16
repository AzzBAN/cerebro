package domain

import "time"

// BiasScore is the Screening Agent's directional read on a symbol.
type BiasScore int8

const (
	BiasBearish BiasScore = -1
	BiasNeutral BiasScore = 0
	BiasBullish BiasScore = 1
)

func (b BiasScore) String() string {
	switch b {
	case BiasBullish:
		return "Bullish"
	case BiasBearish:
		return "Bearish"
	default:
		return "Neutral"
	}
}

// BiasResult is the cached output of one Screening Agent run.
type BiasResult struct {
	Symbol    Symbol
	Score     BiasScore
	Reasoning string
	CachedAt  time.Time
	ExpiresAt time.Time
}

// IsExpired returns true if the bias TTL has elapsed.
func (b BiasResult) IsExpired() bool {
	return time.Now().After(b.ExpiresAt)
}

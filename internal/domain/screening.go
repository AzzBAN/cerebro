package domain

import "time"

// ScreeningOpportunity is a ranked, actionable entry signal produced by the
// cross-symbol screening phase (Phase 2).
type ScreeningOpportunity struct {
	ID           string
	Symbol       Symbol
	Venue        Venue
	Side         Side
	Confidence   float64 // 0.0–1.0
	Bias         BiasScore
	Reasoning    string
	Correlations []SymbolCorrelation
	Avoided      bool // high-impact event nearby
	CachedAt     time.Time
	ExpiresAt    time.Time
}

// SymbolCorrelation captures how a related symbol's bias relates to the
// opportunity's direction.
type SymbolCorrelation struct {
	Symbol Symbol
	Bias   BiasScore
	Impact string // "confirming" | "diverging" | "neutral"
	Note   string
}

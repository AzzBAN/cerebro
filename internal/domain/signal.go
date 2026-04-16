package domain

import "time"

// Signal is an entry recommendation produced by a strategy.
// It is a candidate only — it must pass the Risk Gate before becoming an OrderIntent.
type Signal struct {
	ID            string
	CorrelationID string
	Strategy      StrategyName
	Symbol        Symbol
	Side          Side
	Timeframe     Timeframe
	Reason        string // human-readable; logged to Postgres
	GeneratedAt   time.Time
}

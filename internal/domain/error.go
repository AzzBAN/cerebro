package domain

import "errors"

// Sentinel domain errors. Use errors.Is for matching.
var (
	ErrSignalRejected   = errors.New("signal rejected by risk gate")
	ErrAgentTimeout     = errors.New("agent LLM did not respond within deadline")
	ErrBudgetExceeded   = errors.New("LLM token or cost budget exceeded")
	ErrHaltActive       = errors.New("trading halted; no new orders accepted")
	ErrVenueUnavailable = errors.New("broker venue temporarily unavailable")
	ErrPositionMismatch = errors.New("reconcile: broker position differs from local state")
	ErrConfigInvalid    = errors.New("configuration validation failed")
	ErrDuplicateSignal  = errors.New("signal deduplicated within window")
	ErrIPBanned         = errors.New("IP banned by broker (HTTP 418); halting all operations")
	ErrRateLimitWeight  = errors.New("Binance request weight limit approached; backing off")
)

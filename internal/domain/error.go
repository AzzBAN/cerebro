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
	// ErrCircuitOpen signals that an LLM circuit breaker is short-circuiting
	// calls to a failing provider. Distinct from ErrAgentTimeout so callers
	// can fail fast (do NOT retry) — the breaker exists precisely for that.
	ErrCircuitOpen = errors.New("circuit breaker open")

	// Exchange filter violations — returned by SymbolFilter.Validate before
	// an order is submitted to the broker, so we fail fast locally rather
	// than burn weight and audit noise on a guaranteed -1013 rejection.
	ErrOrderBelowMinQty      = errors.New("order quantity below symbol minQty filter")
	ErrOrderAboveMaxQty      = errors.New("order quantity above symbol maxQty filter")
	ErrOrderBelowMinNotional = errors.New("order notional below symbol minNotional filter")
	ErrSymbolFilterUnknown   = errors.New("no exchange filter loaded for symbol")
)

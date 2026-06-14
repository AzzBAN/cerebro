package port

import (
	"context"

	"github.com/azhar/cerebro/internal/domain"
)

// ExchangeInfoStore exposes the symbol filters (tickSize, stepSize,
// minNotional, …) required for safe order construction. Implementations
// typically load filters once at startup via the venue's exchangeInfo REST
// endpoint and serve them from memory; Refresh is called periodically to
// pick up exchange-initiated filter changes without a restart.
//
// Filter returns domain.ErrSymbolFilterUnknown when the symbol has not been
// loaded. Callers MUST treat this as a hard error and refuse to submit the
// order — submitting without filters is an easy way to trigger -1013.
type ExchangeInfoStore interface {
	// Filter returns the cached filter for a symbol on this venue.
	Filter(symbol domain.Symbol) (domain.SymbolFilter, error)

	// Refresh reloads the filter cache from the exchange.
	Refresh(ctx context.Context) error

	// Venue identifies this store's venue.
	Venue() domain.Venue
}

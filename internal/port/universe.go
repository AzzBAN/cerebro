package port

import (
	"context"
	"time"

	"github.com/azhar/cerebro/internal/domain"
)

// UniverseFeed exposes the full tradable symbol universe of a single venue.
// Adapters are expected to be public-REST only (no auth required) so the
// discovery pipeline can run even when the trading account has no API keys
// configured for that venue.
//
// Implementations must be safe for concurrent use; the discovery service
// fans out across venues with an errgroup.
type UniverseFeed interface {
	// Venue identifies which venue this feed represents.
	Venue() domain.Venue

	// AllTickers returns a 24h-stats snapshot for every TRADING symbol on
	// the venue. Every row should have ListedAt populated when available
	// (falls back to time.Time{} otherwise); callers may filter by age.
	AllTickers(ctx context.Context) ([]domain.TickerSummary, error)

	// NewListings returns only the symbols onboarded within the given
	// window. Implementations may satisfy this by calling AllTickers and
	// filtering, or by hitting a cheaper dedicated endpoint.
	NewListings(ctx context.Context, maxAge time.Duration) ([]domain.TickerSummary, error)
}

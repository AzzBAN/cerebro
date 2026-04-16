package port

import (
	"context"

	"github.com/azhar/cerebro/internal/domain"
	"github.com/shopspring/decimal"
)

// DerivativesFeed fetches institutional-grade derivatives data from CoinGlass v4.
// Results are cached in Redis by the ingest scheduler; this interface is also
// used for direct on-demand calls when the cache is stale.
type DerivativesFeed interface {
	// Snapshot returns the full derivatives picture for a symbol.
	// Used by the get_derivatives_data agent tool.
	Snapshot(ctx context.Context, symbol domain.Symbol) (*domain.DerivativesSnapshot, error)

	// FundingRate returns the current OI-weighted funding rate.
	FundingRate(ctx context.Context, symbol domain.Symbol) (*domain.FundingRate, error)

	// OpenInterest returns current aggregated OI and recent changes.
	OpenInterest(ctx context.Context, symbol domain.Symbol) (*domain.OpenInterest, error)

	// LiquidationZones returns the top N liquidation clusters within
	// pricePct% of the given reference price.
	LiquidationZones(ctx context.Context, symbol domain.Symbol, refPrice decimal.Decimal, pricePct float64) ([]domain.LiquidationZone, error)

	// FearGreed returns the latest Crypto Fear & Greed Index.
	FearGreed(ctx context.Context) (*domain.FearGreedIndex, error)
}

package domain

import (
	"time"

	"github.com/shopspring/decimal"
)

// TickerSummary is a venue-native 24h market snapshot used by the discovery
// pipeline to screen the full symbol universe. It is intentionally narrower
// than Quote — no bid/ask/mid — because discovery only needs relative
// strength + liquidity metrics.
type TickerSummary struct {
	Symbol           Symbol          // canonical form (e.g. BTC/USDT-PERP)
	Venue            Venue           // binance_futures, binance_spot, …
	ContractType     ContractType    // spot | futures_perpetual
	QuoteAsset       string          // USDT, BUSD, …
	LastPrice        decimal.Decimal // last trade price
	PriceChangePct24 float64         // 24h percent change
	Volume24h        decimal.Decimal // base-asset 24h volume
	QuoteVolume24h   decimal.Decimal // quote-asset (USDT) 24h volume
	ListedAt         time.Time       // onboard date (zero if unknown)
	FetchedAt        time.Time
}

// IsNewListing reports whether this ticker was onboarded within maxAge.
// Returns false when ListedAt is unset (zero time).
func (t TickerSummary) IsNewListing(maxAge time.Duration) bool {
	if t.ListedAt.IsZero() || maxAge <= 0 {
		return false
	}
	return time.Since(t.ListedAt) <= maxAge
}

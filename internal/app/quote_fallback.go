package app

import (
	"context"
	"strings"
	"time"

	"github.com/azhar/cerebro/internal/agent/tools"
	"github.com/azhar/cerebro/internal/domain"
	"github.com/azhar/cerebro/internal/port"
	"github.com/shopspring/decimal"
)

// buildQuoteFallback returns a tools.QuoteFallback that resolves a missing
// hub quote by reading the cached UniverseFeed snapshot for the matching
// venue. The UniverseFeed already pulls every symbol per cycle, so this
// adds zero extra REST calls — it just looks up the row.
//
// Returns nil when no UniverseFeeds are wired (discovery disabled), in
// which case GetMarketData simply skips the fallback.
func buildQuoteFallback(feeds map[domain.Venue]port.UniverseFeed) tools.QuoteFallback {
	if len(feeds) == 0 {
		return nil
	}
	return func(ctx context.Context, sym domain.Symbol) (domain.Quote, bool, error) {
		venue := guessVenue(sym)
		feed, ok := feeds[venue]
		if !ok {
			// Try every wired venue as a last resort.
			for _, f := range feeds {
				if q, found, err := lookupTicker(ctx, f, sym); err != nil {
					return domain.Quote{}, false, err
				} else if found {
					return q, true, nil
				}
			}
			return domain.Quote{}, false, nil
		}
		return lookupTicker(ctx, feed, sym)
	}
}

// guessVenue picks the most likely venue from the symbol shape. PERP suffix
// implies futures; bare quote pair implies spot.
func guessVenue(sym domain.Symbol) domain.Venue {
	if strings.HasSuffix(strings.ToUpper(string(sym)), "-PERP") {
		return domain.VenueBinanceFutures
	}
	return domain.VenueBinanceSpot
}

func lookupTicker(ctx context.Context, feed port.UniverseFeed, sym domain.Symbol) (domain.Quote, bool, error) {
	tickers, err := feed.AllTickers(ctx)
	if err != nil {
		return domain.Quote{}, false, err
	}
	for _, t := range tickers {
		if t.Symbol == sym {
			return domain.Quote{
				Symbol:             t.Symbol,
				Last:               t.LastPrice,
				Bid:                t.LastPrice,
				Ask:                t.LastPrice,
				Mid:                t.LastPrice,
				PriceChangePercent: decimal.NewFromFloat(t.PriceChangePct24),
				Volume24h:          t.QuoteVolume24h,
				Timestamp:          time.Now().UTC(),
			}, true, nil
		}
	}
	return domain.Quote{}, false, nil
}

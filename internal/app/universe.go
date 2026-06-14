package app

import (
	"log/slog"
	"strings"

	binancefutures "github.com/azhar/cerebro/internal/adapter/binance/futures"
	"github.com/azhar/cerebro/internal/config"
	"github.com/azhar/cerebro/internal/domain"
	"github.com/azhar/cerebro/internal/port"
)

// buildUniverseFeeds constructs a map of UniverseFeed per venue listed in
// the discovery config. Unknown / unsupported venues are skipped with a
// warning (rather than returning an error) so a typo in include_venues
// degrades gracefully to "static symbols only".
func buildUniverseFeeds(cfg config.DiscoveryConfig) map[domain.Venue]port.UniverseFeed {
	feeds := make(map[domain.Venue]port.UniverseFeed, len(cfg.IncludeVenues))
	for _, raw := range cfg.IncludeVenues {
		v := domain.Venue(strings.TrimSpace(raw))
		switch v {
		case domain.VenueBinanceFutures:
			feeds[v] = binancefutures.NewUniverseFeed()
		case domain.VenueBinanceSpot:
			// MVP: spot discovery deferred to a follow-up milestone.
			slog.Warn("discovery: binance_spot universe feed not implemented yet; skipping")
		default:
			slog.Warn("discovery: unsupported venue for discovery; skipping", "venue", v)
		}
	}
	return feeds
}

// venueKeys returns the venue keys of a feed map as strings for logging.
func venueKeys(m map[domain.Venue]port.UniverseFeed) []string {
	out := make([]string, 0, len(m))
	for v := range m {
		out = append(out, string(v))
	}
	return out
}

package app

import (
	"context"
	"log/slog"
	"strings"
	"time"

	binancefutures "github.com/azhar/cerebro/internal/adapter/binance/futures"
	"github.com/azhar/cerebro/internal/agent"
	"github.com/azhar/cerebro/internal/config"
	"github.com/azhar/cerebro/internal/domain"
	"github.com/azhar/cerebro/internal/port"
)

// BuildUniverseFeeds constructs a map of UniverseFeed per venue listed in
// the discovery config. Unknown / unsupported venues are skipped with a
// warning (rather than returning an error) so a typo in include_venues
// degrades gracefully to "static symbols only".
//
// This is exported so CLI commands can reuse it for one-off discovery runs.
func BuildUniverseFeeds(cfg config.DiscoveryConfig) map[domain.Venue]port.UniverseFeed {
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

// NewDiscovery creates a Discovery agent with the given config, cache, and feeds.
// This is a convenience wrapper for CLI commands that need to run discovery once.
func NewDiscovery(cfg config.DiscoveryConfig, biasTTL time.Duration, cache port.Cache, feeds map[domain.Venue]port.UniverseFeed) (*agent.Discovery, error) {
	if biasTTL <= 0 {
		biasTTL = 4 * time.Hour
	}
	discovery := agent.NewDiscovery(feeds, cache, cfg, biasTTL)
	return discovery, nil
}

// RunDiscoveryCycle is a one-shot helper for CLI commands that want to run
// discovery and get the candidates list.
func RunDiscoveryCycle(ctx context.Context, fullCfg *config.Config, cache port.Cache) ([]agent.DiscoveryCandidate, error) {
	feeds := BuildUniverseFeeds(fullCfg.Agent.Discovery)
	if len(feeds) == 0 {
		return nil, nil
	}
	discovery, err := NewDiscovery(fullCfg.Agent.Discovery, fullCfg.BiasTTL(), cache, feeds)
	if err != nil {
		return nil, err
	}
	return discovery.Candidates(ctx)
}

// venueKeys returns the venue keys of a feed map as strings for logging.
func venueKeys(m map[domain.Venue]port.UniverseFeed) []string {
	out := make([]string, 0, len(m))
	for v := range m {
		out = append(out, string(v))
	}
	return out
}

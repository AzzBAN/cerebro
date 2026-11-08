package app

import (
	"context"
	"log/slog"
	"time"

	"github.com/azhar/cerebro/internal/agent"
	"github.com/azhar/cerebro/internal/config"
	"github.com/azhar/cerebro/internal/domain"
	"github.com/azhar/cerebro/internal/port"
	"github.com/azhar/cerebro/internal/risk"
)

// runDiscoveryWatcher polls Redis for new discovery candidates and dynamically
// updates WebSocket subscriptions and the risk-gate allow-list so that
// discovery-surfaced symbols receive market data and can reach a broker.
//
// The watcher runs every pollInterval and merges discovery candidates with the
// static symbols from markets.yaml before updating each subsystem.
func runDiscoveryWatcher(
	ctx context.Context,
	connectors *klineConnectors,
	cache port.Cache,
	gate *risk.Gate,
	cfg config.DiscoveryConfig,
	staticSymbols []domain.Symbol,
) error {
	pollInterval := 5 * time.Minute
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	slog.Info("discovery watcher started", "poll_interval", pollInterval, "static_symbols", len(staticSymbols))

	for {
		select {
		case <-ctx.Done():
			slog.Info("discovery watcher stopped")
			return ctx.Err()
		case <-ticker.C:
			candidates, err := agent.LoadCachedCandidates(ctx, cache)
			if err != nil {
				slog.Warn("discovery watcher: failed to load candidates", "error", err)
				continue
			}

			if len(candidates) == 0 {
				slog.Debug("discovery watcher: no candidates in cache")
				continue
			}

			// Merge static + discovery symbols (deduplicated via set)
			merged := mergeSymbols(staticSymbols, candidates)
			slog.Info("discovery watcher: updating subscriptions",
				"static", len(staticSymbols),
				"discovery", len(candidates),
				"merged", len(merged))

			// Update risk gate to allow discovery symbols through
			gate.SetAllowedSymbols(merged)

			// Update WebSocket subscriptions for both venues
			connectors.updateSpotSymbols(merged)
			connectors.updateFuturesSymbols(merged)
		}
	}
}

// mergeSymbols combines static symbols with discovery candidates into a
// deduplicated list.
func mergeSymbols(static []domain.Symbol, candidates []agent.DiscoveryCandidate) []domain.Symbol {
	seen := make(map[domain.Symbol]struct{}, len(static)+len(candidates))
	for _, s := range static {
		seen[s] = struct{}{}
	}
	for _, c := range candidates {
		seen[c.Symbol] = struct{}{}
	}

	out := make([]domain.Symbol, 0, len(seen))
	for s := range seen {
		out = append(out, s)
	}
	return out
}

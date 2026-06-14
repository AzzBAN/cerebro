package app

import (
	"context"
	"log/slog"

	"github.com/azhar/cerebro/internal/agent"
	"github.com/azhar/cerebro/internal/domain"
	"github.com/azhar/cerebro/internal/port"
)

// staticSource wraps the markets.yaml-derived symbol list. The slice is
// captured once at startup; the risk gate still guards execution, so
// mutating markets.yaml requires a restart (intentional).
type staticSource struct {
	syms []domain.Symbol
}

// NewStaticSource returns a SymbolSource backed by a fixed slice, typically
// collectSymbolList(cfg.Markets).
func NewStaticSource(syms []domain.Symbol) port.SymbolSource {
	return &staticSource{syms: append([]domain.Symbol(nil), syms...)}
}

func (s *staticSource) Symbols(_ context.Context) []domain.Symbol {
	// Return a copy so callers can freely append.
	return append([]domain.Symbol(nil), s.syms...)
}

// discoverySource reads the cached DiscoveryCandidate slice from Redis
// (the agent.Discovery service populates it once per screening cycle)
// and projects the symbols only. It never triggers discovery itself;
// that job belongs to the scheduler in runtime.go.
type discoverySource struct {
	cache port.Cache
}

// NewDiscoverySource returns a SymbolSource that reads discovery:candidates
// from the cache. It is safe to pass a nil cache — Symbols returns nil.
func NewDiscoverySource(cache port.Cache) port.SymbolSource {
	return &discoverySource{cache: cache}
}

func (s *discoverySource) Symbols(ctx context.Context) []domain.Symbol {
	cands, err := agent.LoadCachedCandidates(ctx, s.cache)
	if err != nil {
		slog.Warn("discovery source: cache read failed", "error", err)
		return nil
	}
	out := make([]domain.Symbol, 0, len(cands))
	for _, c := range cands {
		out = append(out, c.Symbol)
	}
	return out
}

// unionSource returns the dedup'd union of its children's symbols. The
// order of the first source is preserved; new symbols from later sources
// are appended in the order they are first seen.
type unionSource struct {
	sources []port.SymbolSource
}

// NewUnionSource composes multiple SymbolSources into one.
func NewUnionSource(sources ...port.SymbolSource) port.SymbolSource {
	// Filter out nil sources so callers can safely pass optional feeds.
	filtered := make([]port.SymbolSource, 0, len(sources))
	for _, s := range sources {
		if s != nil {
			filtered = append(filtered, s)
		}
	}
	return &unionSource{sources: filtered}
}

func (u *unionSource) Symbols(ctx context.Context) []domain.Symbol {
	seen := make(map[domain.Symbol]bool, 16)
	out := make([]domain.Symbol, 0, 16)
	for _, src := range u.sources {
		for _, sym := range src.Symbols(ctx) {
			if seen[sym] {
				continue
			}
			seen[sym] = true
			out = append(out, sym)
		}
	}
	return out
}

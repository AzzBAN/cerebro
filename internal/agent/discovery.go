package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/shopspring/decimal"
	"golang.org/x/sync/errgroup"

	"github.com/azhar/cerebro/internal/config"
	"github.com/azhar/cerebro/internal/domain"
	"github.com/azhar/cerebro/internal/port"
)

// DiscoveryCacheKey is the Redis key under which the current-cycle candidate
// list is stored. Consumers (TUI, get_discovery_candidates tool) read from
// this key. Exported so consumers can import it without duplicating the
// literal string.
const DiscoveryCacheKey = "discovery:candidates"

// DiscoveryCandidate is a single enriched row produced by the discovery
// service. It is cached in Redis as a JSON array under DiscoveryCacheKey
// and consumed by the get_discovery_candidates tool and the TUI.
type DiscoveryCandidate struct {
	Symbol           domain.Symbol   `json:"symbol"`
	Venue            domain.Venue    `json:"venue"`
	ContractType     domain.ContractType `json:"contract_type"`
	QuoteAsset       string          `json:"quote_asset"`
	LastPrice        decimal.Decimal `json:"last_price"`
	PriceChangePct24 float64         `json:"price_change_pct_24h"`
	QuoteVolume24h   decimal.Decimal `json:"quote_volume_24h"`
	ListedAt         time.Time       `json:"listed_at,omitempty"`
	IsNewListing     bool            `json:"is_new_listing"`
	Tags             []string        `json:"tags,omitempty"`
	Score            float64         `json:"score"`
	FetchedAt        time.Time       `json:"fetched_at"`
}

// Discovery screens the full venue universe each cycle and caches a
// ranked candidate list. It is invoked by the ScreeningAgent's runCycle
// before Phase 1.
type Discovery struct {
	feeds map[domain.Venue]port.UniverseFeed
	cache port.Cache
	cfg   config.DiscoveryConfig
	ttl   time.Duration
}

// NewDiscovery builds a Discovery service. `ttl` should equal the screening
// interval so the cached key stays fresh across exactly one cycle.
func NewDiscovery(feeds map[domain.Venue]port.UniverseFeed, cache port.Cache, cfg config.DiscoveryConfig, ttl time.Duration) *Discovery {
	return &Discovery{
		feeds: feeds,
		cache: cache,
		cfg:   cfg,
		ttl:   ttl,
	}
}

// Candidates runs the discovery pipeline once: fetch all tickers per
// configured venue, filter, score, dedup by base asset, take top-K, and
// cache the result. The returned slice is the same data that was cached.
//
// Per-venue failures degrade gracefully: a single broken feed does not
// abort the whole cycle.
func (d *Discovery) Candidates(ctx context.Context) ([]DiscoveryCandidate, error) {
	if !d.cfg.Enabled {
		return nil, nil
	}

	quote := strings.ToUpper(strings.TrimSpace(d.cfg.QuoteAsset))
	maxAge := time.Duration(d.cfg.NewListingMaxAgeDays) * 24 * time.Hour

	var (
		mu     sync.Mutex
		raw    []domain.TickerSummary
	)
	g, gctx := errgroup.WithContext(ctx)
	for _, rawVenue := range d.cfg.IncludeVenues {
		v := domain.Venue(strings.TrimSpace(rawVenue))
		feed, ok := d.feeds[v]
		if !ok {
			slog.Warn("discovery: no UniverseFeed wired for venue; skipping", "venue", v)
			continue
		}
		g.Go(func() error {
			tickers, err := feed.AllTickers(gctx)
			if err != nil {
				slog.Warn("discovery: AllTickers failed; skipping venue", "venue", v, "error", err)
				return nil // best-effort
			}
			mu.Lock()
			raw = append(raw, tickers...)
			mu.Unlock()
			return nil
		})
	}
	_ = g.Wait()

	if len(raw) == 0 {
		slog.Warn("discovery: no tickers returned from any venue")
		return nil, nil
	}

	// Filter + enrich.
	minQVol := decimal.NewFromFloat(d.cfg.MinQuoteVolume24hUSD)
	minAbsPct := d.cfg.MinAbsPriceChangePct24h

	enriched := make([]DiscoveryCandidate, 0, len(raw))
	for _, t := range raw {
		if !strings.EqualFold(t.QuoteAsset, quote) {
			continue
		}
		if t.QuoteVolume24h.LessThan(minQVol) {
			continue
		}
		if math.Abs(t.PriceChangePct24) < minAbsPct {
			continue
		}
		isNew := t.IsNewListing(maxAge)
		enriched = append(enriched, DiscoveryCandidate{
			Symbol:           t.Symbol,
			Venue:            t.Venue,
			ContractType:     t.ContractType,
			QuoteAsset:       t.QuoteAsset,
			LastPrice:        t.LastPrice,
			PriceChangePct24: t.PriceChangePct24,
			QuoteVolume24h:   t.QuoteVolume24h,
			ListedAt:         t.ListedAt,
			IsNewListing:     isNew,
			Tags:             buildTags(t, isNew),
			FetchedAt:        t.FetchedAt,
		})
	}
	if len(enriched) == 0 {
		slog.Info("discovery: no candidates passed filters",
			"raw", len(raw), "min_qvol_usd", d.cfg.MinQuoteVolume24hUSD,
			"min_abs_pct", d.cfg.MinAbsPriceChangePct24h)
		return nil, nil
	}

	// Score every candidate.
	for i := range enriched {
		enriched[i].Score = scoreCandidate(enriched[i], d.cfg.BoostNewListings)
	}

	sort.SliceStable(enriched, func(i, j int) bool {
		return enriched[i].Score > enriched[j].Score
	})

	// Dedup by base asset — keep the highest-scored row per base.
	dedup := make([]DiscoveryCandidate, 0, len(enriched))
	seenBase := make(map[string]bool, len(enriched))
	for _, c := range enriched {
		base := baseAsset(c.Symbol)
		if seenBase[base] {
			continue
		}
		seenBase[base] = true
		dedup = append(dedup, c)
	}

	// Top-K cap.
	if d.cfg.MaxCandidates > 0 && len(dedup) > d.cfg.MaxCandidates {
		dedup = dedup[:d.cfg.MaxCandidates]
	}

	// Cache for consumers.
	if d.cache != nil && d.ttl > 0 {
		if b, err := json.Marshal(dedup); err == nil {
			if err := d.cache.Set(ctx, DiscoveryCacheKey, b, d.ttl); err != nil {
				slog.Warn("discovery: cache write failed", "error", err)
			}
		}
	}

	slog.Info("discovery: candidates updated",
		"count", len(dedup), "new_listings", countNewListings(dedup))
	return dedup, nil
}

// scoreCandidate combines |Δ24h|, log-scaled quote volume, and a new-listing
// bonus. Weights are intentionally simple; operators tune behaviour via
// filter thresholds rather than re-weighting.
//
//	w1=1.0 for |Δ24h| in %
//	w2=0.2 for log10(quoteVolume+1)
//	w3=+15 for new_listing (when boost is enabled)
func scoreCandidate(c DiscoveryCandidate, boostNew bool) float64 {
	absPct := math.Abs(c.PriceChangePct24)
	qvol, _ := c.QuoteVolume24h.Float64()
	logVol := math.Log10(qvol + 1)

	score := absPct + 0.2*logVol
	if boostNew && c.IsNewListing {
		score += 15
	}
	return score
}

func buildTags(t domain.TickerSummary, isNew bool) []string {
	var tags []string
	if isNew {
		tags = append(tags, "new_listing")
	}
	if t.PriceChangePct24 >= 10 {
		tags = append(tags, "top_mover_up")
	} else if t.PriceChangePct24 <= -10 {
		tags = append(tags, "top_mover_down")
	}
	return tags
}

func countNewListings(cs []DiscoveryCandidate) int {
	n := 0
	for _, c := range cs {
		if c.IsNewListing {
			n++
		}
	}
	return n
}

// baseAsset returns the base asset of a canonical symbol, e.g. "BTC/USDT-PERP" → "BTC".
// Returns the raw string when the symbol is not in canonical form, which is
// fine for dedup purposes (each unknown string is its own bucket).
func baseAsset(sym domain.Symbol) string {
	s := strings.ToUpper(strings.TrimSpace(string(sym)))
	s = strings.TrimSuffix(s, "-PERP")
	if i := strings.Index(s, "/"); i >= 0 {
		return s[:i]
	}
	return s
}

// LoadCachedCandidates reads the cached DiscoveryCandidate slice from Redis.
// Returns (nil, nil) if the key is missing or the cache is nil — callers
// should treat that as "discovery has not run yet this cycle".
func LoadCachedCandidates(ctx context.Context, cache port.Cache) ([]DiscoveryCandidate, error) {
	if cache == nil {
		return nil, nil
	}
	raw, err := cache.Get(ctx, DiscoveryCacheKey)
	if err != nil {
		return nil, fmt.Errorf("discovery: cache get: %w", err)
	}
	if raw == nil {
		return nil, nil
	}
	var out []DiscoveryCandidate
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("discovery: unmarshal: %w", err)
	}
	return out, nil
}

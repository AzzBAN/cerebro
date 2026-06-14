package agent

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/azhar/cerebro/internal/config"
	"github.com/azhar/cerebro/internal/domain"
	"github.com/azhar/cerebro/internal/port"
)

// --- test doubles ------------------------------------------------------------

type fakeUniverseFeed struct {
	venue   domain.Venue
	tickers []domain.TickerSummary
	err     error
}

func (f *fakeUniverseFeed) Venue() domain.Venue { return f.venue }

func (f *fakeUniverseFeed) AllTickers(_ context.Context) ([]domain.TickerSummary, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.tickers, nil
}

func (f *fakeUniverseFeed) NewListings(_ context.Context, maxAge time.Duration) ([]domain.TickerSummary, error) {
	if f.err != nil {
		return nil, f.err
	}
	out := make([]domain.TickerSummary, 0, len(f.tickers))
	for _, t := range f.tickers {
		if t.IsNewListing(maxAge) {
			out = append(out, t)
		}
	}
	return out, nil
}

type fakeCache struct {
	mu   sync.Mutex
	data map[string][]byte
}

func newFakeCache() *fakeCache { return &fakeCache{data: map[string][]byte{}} }

func (c *fakeCache) Set(_ context.Context, key string, v []byte, _ time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.data[key] = append([]byte(nil), v...)
	return nil
}
func (c *fakeCache) Get(_ context.Context, key string) ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v, ok := c.data[key]
	if !ok {
		return nil, nil
	}
	return append([]byte(nil), v...), nil
}
func (c *fakeCache) Delete(_ context.Context, key string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.data, key)
	return nil
}
func (c *fakeCache) Keys(_ context.Context, _ string) ([]string, error) {
	return nil, nil
}
func (c *fakeCache) Exists(_ context.Context, key string) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, ok := c.data[key]
	return ok, nil
}
func (c *fakeCache) IncrBy(_ context.Context, _ string, _ int64, _ time.Duration) (int64, error) {
	return 0, nil
}

// --- fixtures ----------------------------------------------------------------

func mkTicker(sym domain.Symbol, pct float64, qvol float64, listedDaysAgo int) domain.TickerSummary {
	var listedAt time.Time
	if listedDaysAgo > 0 {
		listedAt = time.Now().UTC().Add(-time.Duration(listedDaysAgo) * 24 * time.Hour)
	}
	return domain.TickerSummary{
		Symbol:           sym,
		Venue:            domain.VenueBinanceFutures,
		ContractType:     domain.ContractFuturesPerp,
		QuoteAsset:       "USDT",
		LastPrice:        decimal.NewFromFloat(1),
		PriceChangePct24: pct,
		QuoteVolume24h:   decimal.NewFromFloat(qvol),
		ListedAt:         listedAt,
		FetchedAt:        time.Now().UTC(),
	}
}

func baseCfg() config.DiscoveryConfig {
	return config.DiscoveryConfig{
		Enabled:                 true,
		IncludeVenues:           []string{string(domain.VenueBinanceFutures)},
		QuoteAsset:              "USDT",
		MinQuoteVolume24hUSD:    10_000_000,
		MinAbsPriceChangePct24h: 5.0,
		MaxCandidates:           5,
		NewListingMaxAgeDays:    30,
		BoostNewListings:        true,
	}
}

func newTestDiscovery(t *testing.T, feed port.UniverseFeed, cfg config.DiscoveryConfig) (*Discovery, *fakeCache) {
	t.Helper()
	cache := newFakeCache()
	d := NewDiscovery(
		map[domain.Venue]port.UniverseFeed{domain.VenueBinanceFutures: feed},
		cache, cfg, 10*time.Minute,
	)
	return d, cache
}

// --- tests -------------------------------------------------------------------

func TestDiscovery_Candidates_FiltersAndRanks(t *testing.T) {
	feed := &fakeUniverseFeed{
		venue: domain.VenueBinanceFutures,
		tickers: []domain.TickerSummary{
			mkTicker("BTC/USDT-PERP", 4.0, 1_000_000_000, 0),  // filtered: |Δ| < 5
			mkTicker("ETH/USDT-PERP", 6.0, 50_000_000, 0),     // pass
			mkTicker("PEPE/USDT-PERP", 35.0, 200_000_000, 10), // pass, new listing
			mkTicker("DOGE/USDT-PERP", 12.0, 800_000_000, 0),  // pass
			mkTicker("LOW/USDT-PERP", 50.0, 1_000_000, 0),     // filtered: qvol too low
		},
	}
	d, cache := newTestDiscovery(t, feed, baseCfg())

	out, err := d.Candidates(context.Background())
	if err != nil {
		t.Fatalf("Candidates() error = %v", err)
	}
	if len(out) != 3 {
		t.Fatalf("got %d candidates, want 3 (ETH, PEPE, DOGE); got %+v", len(out), symbolList(out))
	}

	// New-listing boost pushes PEPE to the top despite DOGE having more volume.
	if out[0].Symbol != "PEPE/USDT-PERP" {
		t.Errorf("top candidate = %q, want PEPE/USDT-PERP (new-listing boost)", out[0].Symbol)
	}
	if !out[0].IsNewListing {
		t.Error("PEPE should be flagged as new listing")
	}
	containsTag := func(tags []string, want string) bool {
		for _, tg := range tags {
			if tg == want {
				return true
			}
		}
		return false
	}
	if !containsTag(out[0].Tags, "new_listing") {
		t.Errorf("PEPE tags = %v, want new_listing", out[0].Tags)
	}

	// Cache must have been written.
	cached, err := LoadCachedCandidates(context.Background(), cache)
	if err != nil {
		t.Fatalf("LoadCachedCandidates error = %v", err)
	}
	if len(cached) != len(out) {
		t.Errorf("cached len = %d, want %d", len(cached), len(out))
	}
}

func TestDiscovery_Candidates_DisabledReturnsNil(t *testing.T) {
	cfg := baseCfg()
	cfg.Enabled = false
	feed := &fakeUniverseFeed{venue: domain.VenueBinanceFutures}
	d, _ := newTestDiscovery(t, feed, cfg)

	out, err := d.Candidates(context.Background())
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	if out != nil {
		t.Errorf("expected nil candidates when disabled, got %d", len(out))
	}
}

func TestDiscovery_Candidates_VenueErrorIsBestEffort(t *testing.T) {
	feed := &fakeUniverseFeed{
		venue: domain.VenueBinanceFutures,
		err:   errors.New("boom"),
	}
	d, _ := newTestDiscovery(t, feed, baseCfg())

	out, err := d.Candidates(context.Background())
	if err != nil {
		t.Fatalf("Candidates should not error on single-venue failure, got %v", err)
	}
	if len(out) != 0 {
		t.Errorf("expected 0 candidates on venue failure, got %d", len(out))
	}
}

func TestDiscovery_Candidates_DedupByBaseAsset(t *testing.T) {
	// Same base (PEPE) on both spot-like and perp — dedup should keep the higher score.
	feed := &fakeUniverseFeed{
		venue: domain.VenueBinanceFutures,
		tickers: []domain.TickerSummary{
			mkTicker("PEPE/USDT", 30.0, 100_000_000, 0),
			mkTicker("PEPE/USDT-PERP", 35.0, 200_000_000, 0),
		},
	}
	d, _ := newTestDiscovery(t, feed, baseCfg())

	out, err := d.Candidates(context.Background())
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("got %d candidates, want 1 after dedup", len(out))
	}
	if out[0].Symbol != "PEPE/USDT-PERP" {
		t.Errorf("dedup kept %q, want PEPE/USDT-PERP (higher score)", out[0].Symbol)
	}
}

func TestDiscovery_Candidates_CapsAtMaxCandidates(t *testing.T) {
	tickers := make([]domain.TickerSummary, 0, 20)
	for i := 0; i < 20; i++ {
		sym := domain.Symbol("SYM" + string(rune('A'+i)) + "/USDT-PERP")
		tickers = append(tickers, mkTicker(sym, 10+float64(i), 100_000_000, 0))
	}
	feed := &fakeUniverseFeed{venue: domain.VenueBinanceFutures, tickers: tickers}

	cfg := baseCfg()
	cfg.MaxCandidates = 5
	d, _ := newTestDiscovery(t, feed, cfg)

	out, err := d.Candidates(context.Background())
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	if len(out) != 5 {
		t.Errorf("got %d candidates, want 5 (capped)", len(out))
	}
}

func symbolList(cs []DiscoveryCandidate) []domain.Symbol {
	out := make([]domain.Symbol, len(cs))
	for i, c := range cs {
		out[i] = c.Symbol
	}
	return out
}

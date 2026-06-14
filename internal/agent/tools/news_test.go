package tools

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/azhar/cerebro/internal/port"
)

// memCache is a minimal in-memory Cache for tests.
type memCache struct {
	data map[string][]byte
}

func newMemCache() *memCache { return &memCache{data: map[string][]byte{}} }

func (c *memCache) Set(_ context.Context, k string, v []byte, _ time.Duration) error {
	c.data[k] = append([]byte(nil), v...)
	return nil
}
func (c *memCache) Get(_ context.Context, k string) ([]byte, error) {
	return c.data[k], nil
}
func (c *memCache) Delete(_ context.Context, k string) error { delete(c.data, k); return nil }
func (c *memCache) IncrBy(_ context.Context, _ string, _ int64, _ time.Duration) (int64, error) {
	return 0, nil
}
func (c *memCache) Keys(_ context.Context, _ string) ([]string, error) { return nil, nil }
func (c *memCache) Exists(_ context.Context, k string) (bool, error)   { _, ok := c.data[k]; return ok, nil }

// erroringFeed fails every call — lets us prove the cache path sidesteps
// the live feed when data is already present.
type erroringFeed struct{ callCount int }

func (f *erroringFeed) FetchLatest(_ context.Context, _ string, _ int) ([]port.NewsItem, error) {
	f.callCount++
	return nil, errorString("live feed should not be called when cache is hot")
}

type errorString string

func (e errorString) Error() string { return string(e) }

func TestFetchLatestNews_CacheHitSkipsFeed(t *testing.T) {
	cache := newMemCache()
	cached := []port.NewsItem{
		{ID: "1", Title: "Bitcoin surges", Domain: "coindesk.com", Currencies: []string{"BTC"}},
		{ID: "2", Title: "ETH Shanghai upgrade", Domain: "theblock.co", Currencies: []string{"ETH"}},
	}
	b, _ := json.Marshal(cached)
	_ = cache.Set(context.Background(), "news:latest", b, 0)

	feed := &erroringFeed{}
	tool := FetchLatestNews(feed, cache)

	raw, err := tool.Handler(context.Background(), json.RawMessage(`{"query":"","limit":5}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if feed.callCount != 0 {
		t.Errorf("live feed called %d times; want 0 (cache hit)", feed.callCount)
	}

	var out struct {
		Items []port.NewsItem `json:"items"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if len(out.Items) != 2 {
		t.Errorf("got %d items, want 2", len(out.Items))
	}
}

func TestFetchLatestNews_TickerHitsPerAssetKey(t *testing.T) {
	cache := newMemCache()
	btc := []port.NewsItem{{ID: "b1", Title: "BTC hits ATH", Currencies: []string{"BTC"}}}
	b, _ := json.Marshal(btc)
	_ = cache.Set(context.Background(), "news:by_asset:BTC", b, 0)

	feed := &erroringFeed{}
	tool := FetchLatestNews(feed, cache)

	raw, err := tool.Handler(context.Background(), json.RawMessage(`{"query":"BTC","limit":10}`))
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if feed.callCount != 0 {
		t.Errorf("live feed called; want cache hit")
	}
	if !containsTitle(raw, "BTC hits ATH") {
		t.Errorf("response missing BTC item: %s", string(raw))
	}
}

func TestFetchLatestNews_KeywordFiltersGlobal(t *testing.T) {
	cache := newMemCache()
	items := []port.NewsItem{
		{ID: "1", Title: "Bitcoin surges on ETF news", Currencies: []string{"BTC"}},
		{ID: "2", Title: "Ethereum devs ship upgrade", Currencies: []string{"ETH"}},
		{ID: "3", Title: "Solana outage continues", Currencies: []string{"SOL"}},
	}
	b, _ := json.Marshal(items)
	_ = cache.Set(context.Background(), "news:latest", b, 0)

	tool := FetchLatestNews(nil, cache)
	raw, err := tool.Handler(context.Background(), json.RawMessage(`{"query":"etf","limit":10}`))
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if !containsTitle(raw, "Bitcoin surges on ETF news") {
		t.Errorf("expected ETF match, got: %s", string(raw))
	}
	if containsTitle(raw, "Solana outage continues") {
		t.Errorf("keyword 'etf' should not match Solana headline")
	}
}

func TestFetchLatestNews_EmptyCacheFallsBackToFeed(t *testing.T) {
	cache := newMemCache()
	feed := &stubFeed{items: []port.NewsItem{{ID: "live1", Title: "live result"}}}
	tool := FetchLatestNews(feed, cache)

	raw, err := tool.Handler(context.Background(), json.RawMessage(`{"query":"","limit":5}`))
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if feed.calls != 1 {
		t.Errorf("live feed called %d times, want 1", feed.calls)
	}
	if !containsTitle(raw, "live result") {
		t.Errorf("expected live result, got: %s", string(raw))
	}
}

type stubFeed struct {
	items []port.NewsItem
	calls int
}

func (f *stubFeed) FetchLatest(_ context.Context, _ string, _ int) ([]port.NewsItem, error) {
	f.calls++
	return f.items, nil
}

func containsTitle(raw []byte, title string) bool {
	var out struct {
		Items []port.NewsItem `json:"items"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return false
	}
	for _, it := range out.Items {
		if it.Title == title {
			return true
		}
	}
	return false
}

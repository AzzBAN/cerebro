package scrape

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"
)

// TestParseRetryAfter covers both Retry-After encodings (integer seconds
// and HTTP-date) plus pathological inputs.
func TestParseRetryAfter(t *testing.T) {
	now := time.Date(2026, 5, 2, 2, 0, 0, 0, time.UTC)
	tests := []struct {
		name   string
		header string
		want   time.Duration
	}{
		{"empty header", "", 0},
		{"whitespace only", "   ", 0},
		{"integer seconds", "30", 30 * time.Second},
		{"zero seconds", "0", 0},
		{"negative rejected", "-5", 0},
		{"unparseable string", "soon", 0},
		{"http date in future", now.Add(90 * time.Second).Format(http.TimeFormat), 90 * time.Second},
		{"http date in past", now.Add(-30 * time.Second).Format(http.TimeFormat), 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := parseRetryAfter(tc.header, now)
			// Allow 1s slop for the date-arithmetic variants because
			// http.TimeFormat has 1s granularity.
			diff := got - tc.want
			if diff < 0 {
				diff = -diff
			}
			if diff > time.Second {
				t.Errorf("parseRetryAfter(%q) = %v, want %v", tc.header, got, tc.want)
			}
		})
	}
}

// TestFinancialJuiceCooldownOn429 verifies that a 429 response from the
// upstream puts the scraper into a cooldown, and that subsequent calls
// short-circuit (i.e. the HTTP server never sees a second request) until
// the cooldown elapses.
func TestFinancialJuiceCooldownOn429(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Retry-After", "60")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	s := newTestFJ(srv.URL, 100*time.Millisecond)

	// First call: upstream returns 429 → scraper records cooldown.
	items, err := s.FetchLatest(context.Background(), "", 10)
	if err != nil {
		t.Fatalf("first call unexpected error: %v", err)
	}
	if items != nil {
		t.Errorf("first call expected nil items on 429, got %d", len(items))
	}
	if got := hits.Load(); got != 1 {
		t.Fatalf("first call expected 1 upstream hit, got %d", got)
	}

	// Second call during cooldown: must short-circuit and NOT hit upstream.
	items, err = s.FetchLatest(context.Background(), "", 10)
	if err != nil {
		t.Fatalf("second call unexpected error: %v", err)
	}
	if items != nil {
		t.Errorf("second call expected nil items while cooling down, got %d", len(items))
	}
	if got := hits.Load(); got != 1 {
		t.Errorf("second call should not have hit upstream, but total hits=%d", got)
	}

	// Sanity: cooldownRemaining reports a sensible window.
	remain, active := s.cooldownRemaining(time.Now())
	if !active {
		t.Fatal("expected active cooldown after 429")
	}
	if remain < 30*time.Second || remain > 61*time.Second {
		t.Errorf("cooldown window outside Retry-After bounds: %v", remain)
	}
}

// TestFinancialJuiceCooldownExpires verifies the scraper retries the
// upstream once the cooldown window has elapsed.
func TestFinancialJuiceCooldownExpires(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if hits.Load() == 1 {
			// First request: rate-limit response.
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		// Second request: healthy RSS payload with a single squawk.
		w.Header().Set("Content-Type", "application/rss+xml")
		fmt.Fprintln(w, rssFixtureOneItem)
	}))
	defer srv.Close()

	s := newTestFJ(srv.URL, 100*time.Millisecond)

	if _, err := s.FetchLatest(context.Background(), "", 10); err != nil {
		t.Fatalf("first call error: %v", err)
	}
	if hits.Load() != 1 {
		t.Fatalf("expected 1 hit after first call, got %d", hits.Load())
	}

	// Force the cooldown to expire by reaching directly into the scraper.
	s.mu.Lock()
	s.cooldownUntil = time.Now().Add(-time.Second)
	s.mu.Unlock()

	items, err := s.FetchLatest(context.Background(), "", 10)
	if err != nil {
		t.Fatalf("post-cooldown call error: %v", err)
	}
	if hits.Load() != 2 {
		t.Errorf("expected second upstream hit after cooldown, hits=%d", hits.Load())
	}
	if len(items) != 1 {
		t.Errorf("expected 1 squawk in healthy response, got %d", len(items))
	}
	if items[0].Source != "financialjuice" || items[0].Domain != "financialjuice.com" {
		t.Errorf("item metadata wrong: %+v", items[0])
	}
}

// TestFinancialJuiceDefaultCooldownWithoutHeader verifies the fallback
// cooldown kicks in when the upstream omits Retry-After entirely.
func TestFinancialJuiceDefaultCooldownWithoutHeader(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	s := newTestFJ(srv.URL, 100*time.Millisecond)
	s.defaultCooldown = 5 * time.Minute

	if _, err := s.FetchLatest(context.Background(), "", 10); err != nil {
		t.Fatalf("fetch error: %v", err)
	}
	remain, active := s.cooldownRemaining(time.Now())
	if !active {
		t.Fatal("expected cooldown after 403")
	}
	if remain < 4*time.Minute || remain > 5*time.Minute {
		t.Errorf("expected ~5m default cooldown, got %v", remain)
	}
	if s.lastBlockStatus() != http.StatusForbidden {
		t.Errorf("lastBlockStatus = %d, want 403", s.lastBlockStatus())
	}
}

// newTestFJ builds a scraper pointed at a test httptest URL by overriding
// the package-level RSS URL for the duration of one test. The override
// is achieved by wrapping the scraper's client.Transport to rewrite the
// request URL, which lets us exercise the cooldown and fetch code paths
// against a local server without touching the production constant.
func newTestFJ(upstreamURL string, timeout time.Duration) *FinancialJuiceScraper {
	s := NewFinancialJuiceWithTimeout(timeout)
	s.client.Transport = &urlRewriteTransport{target: upstreamURL, base: http.DefaultTransport}
	return s
}

type urlRewriteTransport struct {
	target string
	base   http.RoundTripper
}

func (t *urlRewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	u, err := url.Parse(t.target)
	if err != nil {
		return nil, fmt.Errorf("urlRewriteTransport: %w", err)
	}
	clone := req.Clone(req.Context())
	clone.URL.Scheme = u.Scheme
	clone.URL.Host = u.Host
	clone.Host = u.Host
	return t.base.RoundTrip(clone)
}

const rssFixtureOneItem = `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0"><channel><title>FinancialJuice</title>
<item>
  <title>Gold hits record high</title>
  <link>https://financialjuice.com/test/1</link>
  <description>Spot gold prints new all-time high</description>
  <guid>fj-1</guid>
  <pubDate>Fri, 02 May 2026 01:30:00 +0000</pubDate>
</item>
</channel></rss>`

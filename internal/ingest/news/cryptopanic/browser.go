package cryptopanic

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/azhar/cerebro/internal/port"
	"github.com/chromedp/chromedp"
)

// Browser is the headless-Chromium fallback: we let CryptoPanic's own JS
// decrypt the response inside a real browser, then harvest the decrypted
// posts via a JSON.parse override. This path is slower (~5s per scrape)
// but immune to AES-key rotation and IV-formula changes.
type Browser struct {
	allocCtx context.Context
	cancel   context.CancelFunc
	timeout  time.Duration

	// scrapeMu serializes scrapes to avoid Chromium tab stampedes under the
	// same allocator. Mirrors the pattern used by CoinglassScraper.
	scrapeMu sync.Mutex
}

// NewBrowser constructs a Browser lazily — the Chromium process is only
// spawned on the first Scrape call. timeout bounds each scrape.
func NewBrowser(timeout time.Duration) *Browser {
	if timeout == 0 {
		timeout = 60 * time.Second
	}
	return &Browser{timeout: timeout}
}

// Close releases the Chromium process, if any. Safe to call multiple times.
func (b *Browser) Close() {
	if b.cancel != nil {
		b.cancel()
		b.cancel = nil
	}
}

// ensureAllocator spawns the headless Chromium the first time we scrape.
func (b *Browser) ensureAllocator() {
	if b.allocCtx != nil {
		return
	}
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", true),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.Flag("disable-blink-features", "AutomationControlled"),
		chromedp.WindowSize(1366, 900),
	)
	b.allocCtx, b.cancel = chromedp.NewExecAllocator(context.Background(), opts...)
}

// Scrape loads /news/ in a headless browser, captures the decrypted posts
// payload via a JSON.parse override, and returns the posts in the shared
// rawPost shape. asset is an optional currency slug (e.g. "bitcoin") to
// narrow the feed; empty returns the global feed.
func (b *Browser) Scrape(ctx context.Context, asset string) ([]rawPost, error) {
	b.scrapeMu.Lock()
	defer b.scrapeMu.Unlock()
	b.ensureAllocator()

	targetURL := baseURL + "/news/"
	if asset != "" {
		targetURL = baseURL + "/news/" + asset + "/"
	}

	tabCtx, cancel := chromedp.NewContext(b.allocCtx)
	defer cancel()

	scrapeCtx, cancelTimeout := context.WithTimeout(tabCtx, b.timeout)
	defer cancelTimeout()

	// Propagate caller cancellation.
	go func() {
		select {
		case <-ctx.Done():
			cancelTimeout()
		case <-scrapeCtx.Done():
		}
	}()

	hookJS := `
		(function() {
			if (window.__cpHooked) return;
			window.__cpHooked = true;
			window.__cpPayloads = [];
			const _parse = JSON.parse;
			JSON.parse = function(text, reviver) {
				const out = _parse(text, reviver);
				try {
					if (out && typeof out === 'object' && Array.isArray(out.k) && Array.isArray(out.l)) {
						window.__cpPayloads.push(out);
					}
				} catch (e) {}
				return out;
			};
		})();
	`

	var payloadJSON string
	err := chromedp.Run(scrapeCtx,
		chromedp.ActionFunc(func(ctx context.Context) error {
			// Inject the JSON.parse hook on every new document, before any
			// SPA code runs. chromedp.Evaluate after Navigate would be too
			// late because the initial fetch happens synchronously during
			// hydration.
			return chromedp.Evaluate(hookJS, nil).Do(ctx)
		}),
		chromedp.Navigate(targetURL),
		// Re-inject in case Navigate beat the first Evaluate to hydration.
		chromedp.Evaluate(hookJS, nil),
		chromedp.ActionFunc(func(ctx context.Context) error {
			return waitForPayload(ctx, b.timeout)
		}),
		chromedp.Evaluate(`JSON.stringify((window.__cpPayloads||[]).find(p => p && p.k && p.l && p.k.indexOf('title') >= 0) || null)`, &payloadJSON),
	)
	if err != nil {
		return nil, fmt.Errorf("cryptopanic browser: %w", err)
	}
	if payloadJSON == "" || payloadJSON == "null" {
		return nil, errors.New("cryptopanic browser: no posts payload captured")
	}

	posts, err := decodeDictList([]byte(payloadJSON))
	if err != nil {
		return nil, fmt.Errorf("cryptopanic browser: decode captured payload: %w", err)
	}
	slog.Debug("cryptopanic browser: scrape ok", "posts", len(posts), "url", targetURL)
	return posts, nil
}

// waitForPayload polls window.__cpPayloads until it contains a posts-shaped
// entry or the overall scrape timeout elapses.
func waitForPayload(ctx context.Context, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		var present bool
		if err := chromedp.Evaluate(
			`!!((window.__cpPayloads||[]).find(p => p && p.k && p.l && p.k.indexOf('title') >= 0))`,
			&present,
		).Do(ctx); err != nil {
			return err
		}
		if present {
			return nil
		}
		if time.Now().After(deadline) {
			return errors.New("cryptopanic browser: timed out waiting for posts payload")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(300 * time.Millisecond):
		}
	}
}

// BrowserFeed adapts Browser to port.NewsFeed — used when the RE path fails.
type BrowserFeed struct {
	browser *Browser
}

// NewBrowserFeed constructs the browser-backed NewsFeed.
func NewBrowserFeed(browser *Browser) *BrowserFeed {
	return &BrowserFeed{browser: browser}
}

// FetchLatest satisfies port.NewsFeed by delegating to the Chromium
// scraper. The browser path always fetches the global /news/ feed,
// regardless of asset. Per-asset filtering requires CryptoPanic's
// ticker→slug mapping (e.g. "BTC" → "bitcoin") which we do not ship;
// attempting to stitch callers' strings into the URL path produced
// bogus URLs in production (/news/bitcoin reserve us government/).
// Since the browser is a cold-path fallback — only engaged when the RE
// client has structurally broken — returning the global feed is an
// acceptable degradation until the AES key is re-extracted.
func (f *BrowserFeed) FetchLatest(ctx context.Context, _ string, limit int) ([]port.NewsItem, error) {
	posts, err := f.browser.Scrape(ctx, "")
	if err != nil {
		return nil, err
	}
	if limit <= 0 || limit > len(posts) {
		limit = len(posts)
	}
	out := make([]port.NewsItem, 0, limit)
	for i := 0; i < limit; i++ {
		p := posts[i]
		if p.Kind == "sponsored" {
			continue
		}
		out = append(out, toNewsItem(p))
	}
	return out, nil
}

// Ensure BrowserFeed implements port.NewsFeed (compile-time check).
var _ port.NewsFeed = (*BrowserFeed)(nil)

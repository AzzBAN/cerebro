package cryptopanic

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/azhar/cerebro/internal/port"
)

// FallbackFeed composes the pure-Go RE client with a lazily-instantiated
// Chromium fallback. The RE path is tried first; on failure we increment
// a failure counter and transparently fall back to the browser. Once we
// exceed failureThreshold consecutive RE failures we enter a cool-down
// during which the RE path is skipped entirely and all scrapes go through
// the browser — this avoids thrashing while operators rotate the key.
type FallbackFeed struct {
	primary *Feed
	browser *BrowserFeed

	failureThreshold int32
	coolDown         time.Duration
	// browserBudget is the wall-clock budget granted to a browser fallback
	// run independently of the caller's deadline. Chromium cold-start +
	// page load + payload capture can legitimately take 30s+; if the
	// caller's context is shorter (the scrape runner uses 15s by default),
	// the browser would be cancelled before it can produce a result.
	// We bridge caller cancellation via a watcher goroutine so genuine
	// shutdowns still tear down the browser promptly.
	browserBudget time.Duration

	mu            sync.Mutex
	failures      atomic.Int32
	skipPrimaryUntil time.Time
	alertedAt     time.Time

	notifier      port.Notifier
	notifyChannel port.NotifyChannel

	// For telemetry / health checks.
	lastTier     atomic.Value // string: "re" | "browser"
	totalScrapes atomic.Int64
	reFailures   atomic.Int64
	browserRuns  atomic.Int64
}

// Options tunes the fallback behaviour. Zero values use sensible defaults.
type Options struct {
	// FailureThreshold is the number of consecutive RE failures that trips
	// the circuit-breaker. Default: 3.
	FailureThreshold int
	// CoolDown is how long we skip the RE path after a trip. Default: 1h.
	CoolDown time.Duration
	// BrowserBudget bounds a single Chromium fallback run independently of
	// the caller's deadline. Default: 45s. Set to a generous value — too
	// tight a budget under load just produces "context deadline exceeded"
	// noise without saving any work, since Chromium will already have been
	// spawned by the time we cancel.
	BrowserBudget time.Duration
	// Notifier is optional; when set, a single alert is sent per cool-down
	// window explaining that an operator needs to re-extract the AES key.
	Notifier port.Notifier
	// NotifyChannel defaults to ChannelSystemAlerts.
	NotifyChannel port.NotifyChannel
}

// NewFallbackFeed wires the two feeds together. The browser feed is
// optional — pass nil to disable browser fallback entirely, in which case
// RE failures surface to the caller as errors.
func NewFallbackFeed(primary *Feed, browser *BrowserFeed, opts Options) *FallbackFeed {
	if opts.FailureThreshold == 0 {
		opts.FailureThreshold = 3
	}
	if opts.CoolDown == 0 {
		opts.CoolDown = time.Hour
	}
	if opts.BrowserBudget == 0 {
		opts.BrowserBudget = 45 * time.Second
	}
	if opts.NotifyChannel == "" {
		opts.NotifyChannel = port.ChannelSystemAlerts
	}
	f := &FallbackFeed{
		primary:          primary,
		browser:          browser,
		failureThreshold: int32(opts.FailureThreshold),
		coolDown:         opts.CoolDown,
		browserBudget:    opts.BrowserBudget,
		notifier:         opts.Notifier,
		notifyChannel:    opts.NotifyChannel,
	}
	f.lastTier.Store("")
	return f
}

// FetchLatest implements port.NewsFeed. Tier semantics:
//
//   - Happy path: RE primary succeeds → return.
//   - ErrBadPayload (key rotation / IV change): browser fallback runs for
//     this call and the circuit breaker trips after N consecutive failures.
//     The browser is the only recovery path here because the RE client is
//     structurally broken.
//   - Any other RE error (transient 5xx, network timeout, CSRF hiccup):
//     return the error. CryptoPanic is down — the browser path would hit
//     the same origin and fail the same way, but ~30s slower and with a
//     Chromium process to clean up. The runner will just try again next
//     tick; the Redis cache (TTL = interval × 3) keeps serving the last
//     good snapshot to the agent.
//   - Cool-down: when the breaker is tripped, every call goes through the
//     browser until the cool-down elapses.
func (f *FallbackFeed) FetchLatest(ctx context.Context, asset string, limit int) ([]port.NewsItem, error) {
	f.totalScrapes.Add(1)

	// Cool-down: RE is known broken, everything goes via browser.
	if f.inCoolDown() {
		return f.runBrowser(ctx, asset, limit, "cooldown")
	}

	items, err := f.primary.FetchLatest(ctx, asset, limit)
	if err == nil {
		f.onRESuccess()
		return items, nil
	}

	// Rate-limited by upstream: this is expected behaviour under load
	// and the cache (TTL = interval × 3) keeps serving the last good
	// snapshot. Don't trip the circuit breaker, don't spin up Chromium
	// (same origin → same 429), don't log at WARN on every hit.
	if errors.Is(err, ErrRateLimited) {
		slog.Debug("cryptopanic: rate-limit cooldown active; serving cached data",
			"error", err, "asset", asset)
		return nil, err
	}

	// Distinguish structural RE breakage from upstream transient errors.
	// Only the former warrants a browser round-trip.
	if !errors.Is(err, ErrBadPayload) {
		// Log once at WARN — no browser spin-up, no consecutive-failure count.
		slog.Warn("cryptopanic: RE fetch failed (transient); keeping cached data",
			"error", err)
		return nil, err
	}

	f.reFailures.Add(1)
	n := f.failures.Add(1)
	slog.Warn("cryptopanic: RE payload decode failed; falling back to browser",
		"error", err, "consecutive_failures", n)

	if n >= f.failureThreshold {
		f.tripBreaker(ctx, err)
	}

	return f.runBrowser(ctx, asset, limit, "re_bad_payload")
}

// runBrowser drives the Chromium fallback and updates the tier counters.
// reason is emitted on the log line so operators can see why the browser
// is carrying the load.
//
// The browser is given an independent budget (browserBudget) rather than
// inheriting the caller's deadline directly. The scrape runner currently
// gives the cryptopanic job a 15s ctx — too tight for cold-start
// Chromium. We bridge the caller's cancellation so a real shutdown still
// tears down Chromium promptly, but a tight per-tick deadline no longer
// kills the only working tier when the RE path is broken.
func (f *FallbackFeed) runBrowser(ctx context.Context, asset string, limit int, reason string) ([]port.NewsItem, error) {
	if f.browser == nil {
		return nil, errors.New("cryptopanic: browser fallback disabled")
	}

	browserCtx, cancel := context.WithTimeout(context.Background(), f.browserBudget)
	defer cancel()
	go func() {
		select {
		case <-ctx.Done():
			cancel()
		case <-browserCtx.Done():
		}
	}()

	items, err := f.browser.FetchLatest(browserCtx, asset, limit)
	if err != nil {
		slog.Error("cryptopanic: browser fallback failed",
			"error", err, "reason", reason, "budget", f.browserBudget)
		return nil, err
	}
	f.browserRuns.Add(1)
	f.lastTier.Store("browser")
	slog.Debug("cryptopanic: browser fallback ok",
		"items", len(items), "reason", reason)
	return items, nil
}

func (f *FallbackFeed) onRESuccess() {
	if f.failures.Swap(0) > 0 {
		slog.Info("cryptopanic: RE path recovered")
	}
	f.lastTier.Store("re")
}

func (f *FallbackFeed) inCoolDown() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return !f.skipPrimaryUntil.IsZero() && time.Now().Before(f.skipPrimaryUntil)
}

func (f *FallbackFeed) tripBreaker(ctx context.Context, cause error) {
	f.mu.Lock()
	now := time.Now()
	f.skipPrimaryUntil = now.Add(f.coolDown)
	shouldAlert := f.notifier != nil && now.Sub(f.alertedAt) >= f.coolDown
	if shouldAlert {
		f.alertedAt = now
	}
	f.mu.Unlock()

	slog.Error("cryptopanic: RE path circuit-breaker tripped",
		"cool_down", f.coolDown,
		"source_bundle_hash", sourceBundleHash,
		"cause", cause)

	if shouldAlert {
		msg := "CryptoPanic RE path broken — key likely rotated.\n" +
			"Pinned bundle: " + sourceBundleHash + "\n" +
			"Run scripts/extract_cryptopanic_key.sh to refresh the AES key.\n" +
			"Falling back to headless Chromium in the meantime."
		// Send on a detached timeout so a slow/dead notifier doesn't block scrapes.
		go func(parent context.Context) {
			sendCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if err := f.notifier.Send(sendCtx, f.notifyChannel, msg); err != nil {
				slog.Warn("cryptopanic: alert send failed", "error", err)
			}
		}(ctx)
	}
}

// Healthy reports whether the RE path succeeded on the last call (or has
// not yet been called). False means we are currently in the browser fallback.
func (f *FallbackFeed) Healthy() bool {
	v, _ := f.lastTier.Load().(string)
	return v == "" || v == "re"
}

// Stats exposes counters for observability wiring.
func (f *FallbackFeed) Stats() (totalScrapes, reFailures, browserRuns int64, tier string) {
	tier, _ = f.lastTier.Load().(string)
	return f.totalScrapes.Load(), f.reFailures.Load(), f.browserRuns.Load(), tier
}

// Close releases the browser process, if any.
func (f *FallbackFeed) Close() {
	if f.browser != nil && f.browser.browser != nil {
		f.browser.browser.Close()
	}
}

// Ensure FallbackFeed implements port.NewsFeed.
var _ port.NewsFeed = (*FallbackFeed)(nil)

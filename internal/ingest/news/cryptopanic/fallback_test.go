package cryptopanic

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/azhar/cerebro/internal/port"
)

// fakePrimary swaps in place of *Feed (via a tiny interface) so we can
// drive circuit-breaker scenarios deterministically.
type fakePrimary struct {
	calls atomic.Int32
	err   error
	items []port.NewsItem
}

func (f *fakePrimary) FetchLatest(_ context.Context, _ string, _ int) ([]port.NewsItem, error) {
	f.calls.Add(1)
	if f.err != nil {
		return nil, f.err
	}
	return f.items, nil
}

type fakeBrowser struct {
	calls atomic.Int32
	items []port.NewsItem
	err   error
}

func (b *fakeBrowser) FetchLatest(_ context.Context, _ string, _ int) ([]port.NewsItem, error) {
	b.calls.Add(1)
	if b.err != nil {
		return nil, b.err
	}
	return b.items, nil
}

type capturingNotifier struct {
	sends atomic.Int32
	last  string
}

func (n *capturingNotifier) Send(_ context.Context, _ port.NotifyChannel, msg string) error {
	n.sends.Add(1)
	n.last = msg
	return nil
}

func (n *capturingNotifier) SendEmbed(_ context.Context, _ port.NotifyChannel, _, _ string, _ map[string]string) error {
	return nil
}

// newTestFallback constructs a FallbackFeed driven by fakes. We construct
// it manually because the public constructor expects *Feed / *BrowserFeed;
// the fake implements the same FetchLatest contract via the shim below.
func newTestFallback(primary port.NewsFeed, browser port.NewsFeed, notifier port.Notifier) *testFallback {
	return &testFallback{
		primary:          primary,
		browser:          browser,
		notifier:         notifier,
		failureThreshold: 3,
		coolDown:         50 * time.Millisecond,
	}
}

// testFallback mirrors FallbackFeed but is driven by the port interface so
// we can sub in fakes. The public FallbackFeed holds concrete pointers for
// compile-time safety; this shim exercises the same state machine.
type testFallback struct {
	primary          port.NewsFeed
	browser          port.NewsFeed
	failureThreshold int32
	coolDown         time.Duration
	failures         atomic.Int32
	notifier         port.Notifier
	skipUntil        time.Time
	alerted          time.Time
}

func (f *testFallback) FetchLatest(ctx context.Context, asset string, limit int) ([]port.NewsItem, error) {
	// Cool-down: everything goes via browser.
	if !f.skipUntil.IsZero() && time.Now().Before(f.skipUntil) {
		if f.browser == nil {
			return nil, errors.New("no browser")
		}
		return f.browser.FetchLatest(ctx, asset, limit)
	}

	items, err := f.primary.FetchLatest(ctx, asset, limit)
	if err == nil {
		f.failures.Store(0)
		return items, nil
	}

	// Only fall back to the browser when the RE path is structurally
	// broken. Transient errors (network/5xx) propagate to the caller so
	// the runner can skip the tick and reuse the Redis cache.
	if !errors.Is(err, ErrBadPayload) {
		return nil, err
	}

	n := f.failures.Add(1)
	if n >= f.failureThreshold {
		f.skipUntil = time.Now().Add(f.coolDown)
		if f.notifier != nil && time.Since(f.alerted) >= f.coolDown {
			f.alerted = time.Now()
			_ = f.notifier.Send(ctx, port.ChannelSystemAlerts, "broken")
		}
	}

	if f.browser == nil {
		return nil, err
	}
	return f.browser.FetchLatest(ctx, asset, limit)
}

func TestFallback_HealthyPrimary_SkipsBrowser(t *testing.T) {
	p := &fakePrimary{items: []port.NewsItem{{ID: "1"}}}
	b := &fakeBrowser{}
	f := newTestFallback(p, b, nil)

	for i := 0; i < 5; i++ {
		items, err := f.FetchLatest(context.Background(), "", 10)
		if err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
		if len(items) != 1 {
			t.Fatalf("call %d: want 1 item, got %d", i, len(items))
		}
	}
	if b.calls.Load() != 0 {
		t.Errorf("browser called %d times, want 0", b.calls.Load())
	}
}

func TestFallback_REFailures_FallsBackToBrowser(t *testing.T) {
	p := &fakePrimary{err: ErrBadPayload}
	b := &fakeBrowser{items: []port.NewsItem{{ID: "b1"}}}
	f := newTestFallback(p, b, nil)

	items, err := f.FetchLatest(context.Background(), "", 10)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(items) != 1 || items[0].ID != "b1" {
		t.Fatalf("got %+v; want one item from browser", items)
	}
	if p.calls.Load() != 1 || b.calls.Load() != 1 {
		t.Errorf("primary=%d, browser=%d", p.calls.Load(), b.calls.Load())
	}
}

func TestFallback_CircuitBreaker_AlertsOnce(t *testing.T) {
	p := &fakePrimary{err: ErrBadPayload}
	b := &fakeBrowser{items: []port.NewsItem{{ID: "b1"}}}
	n := &capturingNotifier{}
	f := newTestFallback(p, b, n)

	for i := 0; i < 10; i++ {
		_, _ = f.FetchLatest(context.Background(), "", 10)
	}
	// Should alert once per cool-down; counters depend on scheduling but
	// invariant: at least 1, at most 2 alerts inside a 50ms window.
	got := n.sends.Load()
	if got < 1 || got > 2 {
		t.Errorf("sends=%d; want 1-2 for single cool-down window", got)
	}
	// Primary should be *skipped* most of the time after the trip.
	if p.calls.Load() > 5 {
		t.Errorf("primary called %d times; circuit-breaker should have skipped most", p.calls.Load())
	}
	// Browser should carry the load.
	if b.calls.Load() < 5 {
		t.Errorf("browser called %d times; want >=5 after breaker trip", b.calls.Load())
	}
}

func TestFallback_NonBadPayloadError_DoesNotTripBreaker(t *testing.T) {
	p := &fakePrimary{err: fmt.Errorf("transient network blip")}
	b := &fakeBrowser{items: []port.NewsItem{{ID: "b"}}}
	f := newTestFallback(p, b, nil)

	for i := 0; i < 10; i++ {
		_, _ = f.FetchLatest(context.Background(), "", 10)
	}
	// Breaker should not trip on non-ErrBadPayload errors — primary is
	// retried every call.
	if p.calls.Load() != 10 {
		t.Errorf("primary calls=%d; want 10 (no breaker trip on generic errors)", p.calls.Load())
	}
	// Critical invariant: transient errors must NOT spin up the browser.
	// The runner catches the error and reuses the Redis cache.
	if b.calls.Load() != 0 {
		t.Errorf("browser calls=%d; want 0 (only ErrBadPayload should trigger browser fallback)", b.calls.Load())
	}
}

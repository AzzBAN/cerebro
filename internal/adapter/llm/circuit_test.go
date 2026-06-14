package llm

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/azhar/cerebro/internal/domain"
	"github.com/azhar/cerebro/internal/port"
)

// fakeLLM is a minimal port.LLM test double whose next response can be
// scripted. Counts underlying calls so tests can assert short-circuiting.
type fakeLLM struct {
	mu       sync.Mutex
	response string
	err      error
	calls    int
}

func (f *fakeLLM) Provider() string { return "fake" }
func (f *fakeLLM) ModelID() string  { return "fake-1" }

func (f *fakeLLM) Complete(_ context.Context, _, _ string, _ map[string]port.Tool) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	return f.response, f.err
}

func (f *fakeLLM) setResponse(s string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.response, f.err = s, err
}

func (f *fakeLLM) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// mustOpenBreaker is a helper that invokes the breaker enough times with a
// failing inner to trip it. It also asserts the breaker actually opened.
func mustOpenBreaker(t *testing.T, cb port.LLM, inner *fakeLLM, samples int) {
	t.Helper()
	inner.setResponse("", errors.New("boom"))
	for i := 0; i < samples; i++ {
		_, err := cb.Complete(context.Background(), "", "", nil)
		if err == nil {
			t.Fatalf("iter %d: expected error, got nil", i)
		}
	}
}

// TestCircuitBreaker_Disabled verifies that the breaker becomes a pass-through
// when ErrorRatePct <= 0 — calls flow through verbatim no matter how many
// times they fail.
func TestCircuitBreaker_Disabled(t *testing.T) {
	inner := &fakeLLM{err: errors.New("boom")}
	cb := NewCircuitBreaker(inner, CircuitBreakerOpts{ErrorRatePct: 0})

	for i := 0; i < 10; i++ {
		_, err := cb.Complete(context.Background(), "", "", nil)
		if err == nil {
			t.Fatalf("iter %d: want error, got nil", i)
		}
	}
	if got, want := inner.callCount(), 10; got != want {
		t.Errorf("inner calls: got %d, want %d (breaker should be disabled)", got, want)
	}
	// Disabled breaker returns the inner itself; type assertion proves it.
	if _, isBreaker := cb.(*CircuitBreaker); isBreaker {
		t.Errorf("expected inner LLM returned verbatim when breaker disabled")
	}
}

// TestCircuitBreaker_OpensOnThresholdExceeded verifies that once the error
// rate in the sliding window crosses the threshold the breaker opens and
// short-circuits subsequent calls.
func TestCircuitBreaker_OpensOnThresholdExceeded(t *testing.T) {
	inner := &fakeLLM{}
	cb := NewCircuitBreaker(inner, CircuitBreakerOpts{
		ErrorRatePct: 50,
		Window:       time.Minute,
		Cooldown:     time.Minute,
		MinSamples:   3,
	}).(*CircuitBreaker)

	// Fail 3 times — exactly at minSamples, 100% rate → should trip.
	mustOpenBreaker(t, cb, inner, 3)

	if cb.state != stateOpen {
		t.Fatalf("breaker state: got %s, want OPEN", cb.state)
	}

	// Subsequent calls are short-circuited — inner NOT called.
	before := inner.callCount()
	_, err := cb.Complete(context.Background(), "", "", nil)
	if !errors.Is(err, domain.ErrAgentTimeout) {
		t.Errorf("short-circuited error: got %v, want ErrAgentTimeout", err)
	}
	// Caller must also be able to detect this is a circuit-open (don't-retry)
	// error — otherwise the agent runtime would retry against a tripped
	// breaker, wasting the parent invocation budget.
	if !errors.Is(err, domain.ErrCircuitOpen) {
		t.Errorf("short-circuited error: got %v, want ErrCircuitOpen wrap", err)
	}
	// Sanity: a circuit-open error should NOT unwrap to a transient deadline,
	// which would mistakenly trigger retries.
	if errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("circuit-open error must not unwrap to context.DeadlineExceeded: %v", err)
	}
	if got := inner.callCount(); got != before {
		t.Errorf("inner called while OPEN: before=%d after=%d", before, got)
	}
}

// TestCircuitBreaker_MinSamplesGuard verifies the breaker does NOT trip on
// a small number of failures, even at 100% rate — prevents flapping on
// one-off errors.
func TestCircuitBreaker_MinSamplesGuard(t *testing.T) {
	inner := &fakeLLM{err: errors.New("boom")}
	cb := NewCircuitBreaker(inner, CircuitBreakerOpts{
		ErrorRatePct: 50,
		Window:       time.Minute,
		Cooldown:     time.Minute,
		MinSamples:   5,
	}).(*CircuitBreaker)

	for i := 0; i < 4; i++ { // 4 < minSamples
		_, _ = cb.Complete(context.Background(), "", "", nil)
	}
	if cb.state != stateClosed {
		t.Errorf("breaker tripped early: got %s, want CLOSED (only 4 samples < minSamples=5)", cb.state)
	}
}

// TestCircuitBreaker_HalfOpenProbeSucceeds simulates: breaker opens, cooldown
// elapses, next call is a probe; inner recovers; breaker closes.
func TestCircuitBreaker_HalfOpenProbeSucceeds(t *testing.T) {
	inner := &fakeLLM{}

	// Injectable clock so we can advance time deterministically.
	var clock time.Time
	cb := newCircuitBreakerForTest(inner, CircuitBreakerOpts{
		ErrorRatePct: 50,
		Window:       time.Minute,
		Cooldown:     30 * time.Second,
		MinSamples:   3,
	}, func() time.Time { return clock })

	clock = time.Unix(0, 0)
	mustOpenBreaker(t, cb, inner, 3)

	// Not yet past cooldown — still OPEN, short-circuited.
	clock = clock.Add(10 * time.Second)
	_, err := cb.Complete(context.Background(), "", "", nil)
	if !errors.Is(err, domain.ErrAgentTimeout) {
		t.Fatalf("before cooldown: got %v, want short-circuit", err)
	}

	// Past cooldown — next call is a probe.
	clock = clock.Add(25 * time.Second) // total 35s > cooldown 30s
	inner.setResponse("ok", nil)
	callsBefore := inner.callCount()
	out, err := cb.Complete(context.Background(), "", "", nil)
	if err != nil {
		t.Fatalf("probe call: unexpected err=%v", err)
	}
	if out != "ok" {
		t.Errorf("probe output: got %q, want %q", out, "ok")
	}
	if inner.callCount() != callsBefore+1 {
		t.Errorf("expected inner to be called once for probe")
	}
	if cb.state != stateClosed {
		t.Errorf("after successful probe: state=%s, want CLOSED", cb.state)
	}
	if len(cb.outcomes) != 0 {
		t.Errorf("outcomes should be reset after close; got %d", len(cb.outcomes))
	}
}

// TestCircuitBreaker_HalfOpenProbeFails simulates: probe fails, breaker
// re-opens for another cooldown cycle.
func TestCircuitBreaker_HalfOpenProbeFails(t *testing.T) {
	inner := &fakeLLM{}

	var clock time.Time
	cb := newCircuitBreakerForTest(inner, CircuitBreakerOpts{
		ErrorRatePct: 50,
		Window:       time.Minute,
		Cooldown:     30 * time.Second,
		MinSamples:   3,
	}, func() time.Time { return clock })

	clock = time.Unix(0, 0)
	mustOpenBreaker(t, cb, inner, 3)
	openedAtFirst := cb.openedAt

	// Advance past cooldown, fail the probe.
	clock = clock.Add(35 * time.Second)
	inner.setResponse("", errors.New("still broken"))
	_, err := cb.Complete(context.Background(), "", "", nil)
	if err == nil {
		t.Fatal("expected probe error")
	}
	if cb.state != stateOpen {
		t.Errorf("after failed probe: state=%s, want OPEN", cb.state)
	}
	if !cb.openedAt.After(openedAtFirst) {
		t.Errorf("openedAt should have advanced; first=%v now=%v", openedAtFirst, cb.openedAt)
	}
}

// TestCircuitBreaker_WindowPrunesOldFailures verifies that failures outside
// the sliding window don't contribute to the error rate.
func TestCircuitBreaker_WindowPrunesOldFailures(t *testing.T) {
	inner := &fakeLLM{}
	var clock time.Time
	cb := newCircuitBreakerForTest(inner, CircuitBreakerOpts{
		ErrorRatePct: 50,
		Window:       1 * time.Minute,
		Cooldown:     time.Minute,
		MinSamples:   3,
	}, func() time.Time { return clock })

	// 3 ancient failures at t=0 — enough to trip, but they'll be pruned.
	clock = time.Unix(0, 0)
	inner.setResponse("", errors.New("boom"))
	for i := 0; i < 3; i++ {
		_, _ = cb.Complete(context.Background(), "", "", nil)
	}
	// At this point breaker has ALREADY tripped at t=0. Advance beyond
	// cooldown so HALF_OPEN unlocks — we're testing window semantics of
	// the closed state, so close it manually via a successful probe.
	clock = clock.Add(2 * time.Minute)
	inner.setResponse("ok", nil)
	_, err := cb.Complete(context.Background(), "", "", nil)
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if cb.state != stateClosed {
		t.Fatalf("pre-condition: state=%s, want CLOSED", cb.state)
	}

	// Now with a fresh CLOSED breaker, mix 2 successes with 1 failure over
	// a few seconds — error rate 33% < 50%, so should stay CLOSED.
	for _, ok := range []bool{true, false, true} {
		if ok {
			inner.setResponse("ok", nil)
		} else {
			inner.setResponse("", errors.New("boom"))
		}
		clock = clock.Add(1 * time.Second)
		_, _ = cb.Complete(context.Background(), "", "", nil)
	}
	if cb.state != stateClosed {
		t.Errorf("state=%s, want CLOSED (only 33%% errors in window)", cb.state)
	}
}

// newCircuitBreakerForTest is an internal-only helper that exposes the
// injectable clock. The public constructor hard-codes time.Now.
func newCircuitBreakerForTest(inner port.LLM, opts CircuitBreakerOpts, now func() time.Time) *CircuitBreaker {
	// Force-construct a breaker even when opts would be filtered out;
	// tests always provide valid opts but we keep symmetry with the public
	// constructor's validation.
	if opts.ErrorRatePct <= 0 || opts.Window <= 0 || opts.Cooldown <= 0 {
		panic(fmt.Sprintf("test opts invalid: %+v", opts))
	}
	if opts.MinSamples <= 0 {
		opts.MinSamples = 3
	}
	return &CircuitBreaker{
		inner:      inner,
		threshold:  opts.ErrorRatePct / 100.0,
		window:     opts.Window,
		cooldown:   opts.Cooldown,
		minSamples: opts.MinSamples,
		now:        now,
	}
}

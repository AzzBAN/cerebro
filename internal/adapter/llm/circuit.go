package llm

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/azhar/cerebro/internal/domain"
	"github.com/azhar/cerebro/internal/port"
)

// CircuitBreaker is a port.LLM adapter that short-circuits calls to an
// underlying LLM when it has been consistently failing. The breaker follows
// the classic CLOSED → OPEN → HALF_OPEN state machine:
//
//   - CLOSED    : calls flow through; outcomes recorded in a sliding window.
//   - OPEN      : calls return immediately with ErrAgentTimeout (cheap fail).
//   - HALF_OPEN : exactly one probe call is allowed; success closes the
//                 breaker, failure re-opens it for a fresh cooldown.
//
// The breaker is safe for concurrent use.
type CircuitBreaker struct {
	inner       port.LLM
	threshold   float64       // error rate (0..1) that trips the breaker
	window      time.Duration // sliding window for error-rate computation
	cooldown    time.Duration // minimum time spent in OPEN before a probe
	minSamples  int           // don't trip on < this many recorded calls
	now         func() time.Time

	mu           sync.Mutex
	state        breakerState
	outcomes     []outcome // sliding window, pruned on record
	openedAt     time.Time // when state transitioned to OPEN
	probeInFlight bool     // true when a HALF_OPEN probe is executing
}

type breakerState int

const (
	stateClosed breakerState = iota
	stateOpen
	stateHalfOpen
)

func (s breakerState) String() string {
	switch s {
	case stateClosed:
		return "CLOSED"
	case stateOpen:
		return "OPEN"
	case stateHalfOpen:
		return "HALF_OPEN"
	default:
		return "UNKNOWN"
	}
}

type outcome struct {
	ts      time.Time
	success bool
}

// CircuitBreakerOpts configures the breaker. ErrorRatePct <= 0 disables it
// and the constructor returns the underlying LLM unchanged.
type CircuitBreakerOpts struct {
	ErrorRatePct    float64       // e.g. 50 means "trip at 50% failures"
	Window          time.Duration // sliding window for rate calc
	Cooldown        time.Duration // time to stay OPEN before probing
	MinSamples      int           // minimum calls in window before tripping
}

// NewCircuitBreaker wraps inner with a CircuitBreaker. When opts.ErrorRatePct
// is <= 0 the breaker is considered disabled and inner is returned as-is.
func NewCircuitBreaker(inner port.LLM, opts CircuitBreakerOpts) port.LLM {
	if opts.ErrorRatePct <= 0 || opts.Window <= 0 || opts.Cooldown <= 0 {
		return inner
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
		now:        time.Now,
	}
}

// Provider returns the wrapped provider's identifier.
func (c *CircuitBreaker) Provider() string { return c.inner.Provider() }

// ModelID returns the wrapped model's identifier.
func (c *CircuitBreaker) ModelID() string { return c.inner.ModelID() }

// Complete checks the breaker state, then either dispatches to the wrapped
// LLM or short-circuits with ErrAgentTimeout.
func (c *CircuitBreaker) Complete(
	ctx context.Context,
	systemPrompt string,
	userMessage string,
	tools map[string]port.Tool,
) (string, error) {
	// Decide whether this call is allowed to run.
	allowed, isProbe := c.acquire()
	if !allowed {
		slog.Debug("circuit breaker OPEN; short-circuiting LLM call",
			"provider", c.inner.Provider(), "model", c.inner.ModelID())
		// Multi-wrap so callers can distinguish "fail-fast, don't retry"
		// (ErrCircuitOpen) from a real transient timeout, while still
		// satisfying errors.Is(..., ErrAgentTimeout) for the risk gate.
		return "", fmt.Errorf("%w: %w", domain.ErrAgentTimeout, domain.ErrCircuitOpen)
	}

	out, err := c.inner.Complete(ctx, systemPrompt, userMessage, tools)
	c.record(err == nil, isProbe)
	return out, err
}

// acquire checks the breaker's state and returns whether the call may
// proceed. The second return value indicates whether this call is the
// designated HALF_OPEN probe (so record() knows to close on success).
func (c *CircuitBreaker) acquire() (allowed, isProbe bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	switch c.state {
	case stateClosed:
		return true, false

	case stateOpen:
		// Has the cooldown elapsed?
		if c.now().Sub(c.openedAt) < c.cooldown {
			return false, false
		}
		// Transition to HALF_OPEN and dispatch this call as the probe.
		c.state = stateHalfOpen
		c.probeInFlight = true
		slog.Info("circuit breaker → HALF_OPEN; allowing probe call",
			"provider", c.inner.Provider())
		return true, true

	case stateHalfOpen:
		// Only one probe allowed at a time.
		if c.probeInFlight {
			return false, false
		}
		c.probeInFlight = true
		return true, true
	}
	return false, false
}

// record updates the sliding window and transitions state after a call.
func (c *CircuitBreaker) record(success, isProbe bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := c.now()
	c.outcomes = append(c.outcomes, outcome{ts: now, success: success})
	c.pruneLocked(now)

	// HALF_OPEN probe resolves the state deterministically.
	if isProbe {
		c.probeInFlight = false
		if success {
			// One good call → trust restored.
			c.state = stateClosed
			c.outcomes = c.outcomes[:0] // fresh start
			slog.Info("circuit breaker → CLOSED (probe succeeded)",
				"provider", c.inner.Provider())
		} else {
			// Failed probe → another cooldown.
			c.state = stateOpen
			c.openedAt = now
			slog.Warn("circuit breaker → OPEN (probe failed)",
				"provider", c.inner.Provider())
		}
		return
	}

	// CLOSED path: decide whether to trip.
	if c.state == stateClosed {
		rate, total := c.errorRateLocked()
		if total >= c.minSamples && rate >= c.threshold {
			c.state = stateOpen
			c.openedAt = now
			slog.Warn("circuit breaker → OPEN (error rate exceeded)",
				"provider", c.inner.Provider(),
				"error_rate_pct", rate*100,
				"samples", total,
				"threshold_pct", c.threshold*100,
				"cooldown", c.cooldown,
			)
		}
	}
}

// errorRateLocked returns (rate, total) over the pruned window. rate is
// in [0, 1].
func (c *CircuitBreaker) errorRateLocked() (float64, int) {
	total := len(c.outcomes)
	if total == 0 {
		return 0, 0
	}
	failures := 0
	for _, o := range c.outcomes {
		if !o.success {
			failures++
		}
	}
	return float64(failures) / float64(total), total
}

// pruneLocked drops outcomes older than the sliding window.
func (c *CircuitBreaker) pruneLocked(now time.Time) {
	cutoff := now.Add(-c.window)
	i := 0
	for ; i < len(c.outcomes); i++ {
		if !c.outcomes[i].ts.Before(cutoff) {
			break
		}
	}
	if i > 0 {
		c.outcomes = append(c.outcomes[:0], c.outcomes[i:]...)
	}
}

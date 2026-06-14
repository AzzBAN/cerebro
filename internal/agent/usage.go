package agent

import (
	"context"
	"sync"
)

// Usage accumulates token usage and estimated cost for a single agent
// invocation. Adapters write to it via UsageFromCtx after each LLM API
// response; Runtime.Invoke reads it once the call returns and forwards the
// totals to CostTracker.
//
// A pointer to Usage is stashed in the context; callers must treat the
// pointer as write-only from the adapter side. The struct is safe for
// concurrent use (tool calls may be parallel within a single Anthropic
// turn), hence the mutex.
type Usage struct {
	mu                sync.Mutex
	InputTokens       int
	OutputTokens      int
	CachedInputTokens int // cache-read portion (Anthropic prompt caching)
}

// Add increments the running totals atomically. Negative values are ignored
// so a misbehaving provider can never drive the counters below zero.
func (u *Usage) Add(inputTokens, outputTokens, cachedInputTokens int) {
	if u == nil {
		return
	}
	if inputTokens < 0 {
		inputTokens = 0
	}
	if outputTokens < 0 {
		outputTokens = 0
	}
	if cachedInputTokens < 0 {
		cachedInputTokens = 0
	}
	u.mu.Lock()
	u.InputTokens += inputTokens
	u.OutputTokens += outputTokens
	u.CachedInputTokens += cachedInputTokens
	u.mu.Unlock()
}

// Snapshot returns a copy of the current counters. Safe for concurrent use.
func (u *Usage) Snapshot() (in, out, cached int) {
	if u == nil {
		return 0, 0, 0
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.InputTokens, u.OutputTokens, u.CachedInputTokens
}

// usageKey is the context key under which a *Usage is stored.
const usageKey contextKey = "llm_usage"

// WithUsage attaches a Usage accumulator to ctx. Adapters should call
// UsageFromCtx and write observed token counts after each API response.
func WithUsage(ctx context.Context, u *Usage) context.Context {
	if u == nil {
		return ctx
	}
	return context.WithValue(ctx, usageKey, u)
}

// UsageFromCtx returns the Usage accumulator stored in ctx, or nil if none
// is present. Adapters must tolerate a nil return value.
func UsageFromCtx(ctx context.Context) *Usage {
	if v, ok := ctx.Value(usageKey).(*Usage); ok {
		return v
	}
	return nil
}

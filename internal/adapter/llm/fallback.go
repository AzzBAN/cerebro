package llm

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/azhar/cerebro/internal/domain"
	"github.com/azhar/cerebro/internal/observability"
	"github.com/azhar/cerebro/internal/port"
)

// FallbackChain tries providers in order and falls back when a configured
// error condition occurs. All fallbacks are logged; the risk gate fails closed
// if all providers are exhausted.
type FallbackChain struct {
	providers  []port.LLM
	fallbackOn []string // "timeout", "rate_limit", "budget_exceeded"
}

// NewFallbackChain wraps multiple providers with ordered fallback.
func NewFallbackChain(providers []port.LLM, fallbackOn []string) *FallbackChain {
	return &FallbackChain{providers: providers, fallbackOn: fallbackOn}
}

func (f *FallbackChain) Provider() string {
	if len(f.providers) == 0 {
		return "none"
	}
	return f.providers[0].Provider()
}

func (f *FallbackChain) ModelID() string {
	if len(f.providers) == 0 {
		return ""
	}
	return f.providers[0].ModelID()
}

// Complete tries each provider in turn. If a provider fails with a configured
// fallback condition, the next provider is tried. Each provider gets a fair
// share of the remaining deadline so that a slow provider 1 doesn't starve
// provider 2. After all providers fail, returns domain.ErrAgentTimeout so the
// risk gate fails closed.
func (f *FallbackChain) Complete(
	ctx context.Context,
	systemPrompt string,
	userMessage string,
	tools map[string]port.Tool,
) (string, error) {
	var lastErr error
	for i, provider := range f.providers {
		result, err := f.callProvider(ctx, i, provider, systemPrompt, userMessage, tools)
		if err == nil {
			return result, nil
		}
		lastErr = err

		if f.shouldFallback(err) {
			// Redact any URL embedded in the error string. OpenRouter /
			// OpenAI error messages of the form `Post "https://...?...":
			// context canceled` would otherwise carry query strings into
			// the log; we keep the rest of the message intact for triage.
			slog.Warn("LLM provider failed; trying next in chain",
				"provider", provider.Provider(),
				"model", provider.ModelID(),
				"attempt", i+1,
				"total", len(f.providers),
				"error", observability.RedactErr(err),
			)
			continue
		}

		// Non-fallback error: return immediately.
		return "", err
	}

	slog.Error("all LLM providers exhausted; failing closed",
		"providers_tried", len(f.providers))
	// Multi-wrap so callers can:
	//   - errors.Is(err, domain.ErrAgentTimeout) → fail closed at the risk gate
	//   - errors.Is(err, context.DeadlineExceeded) → identify a retryable transient
	//   - errors.Is(err, ErrCircuitOpen) → know NOT to retry
	if lastErr != nil {
		return "", fmt.Errorf("%w: all LLM providers failed: %w", domain.ErrAgentTimeout, lastErr)
	}
	return "", fmt.Errorf("%w: all LLM providers failed", domain.ErrAgentTimeout)
}

// callProvider executes a single provider with a fair share of the remaining
// deadline so a slow provider can't starve fallbacks. The cancel runs as soon
// as this call returns rather than stacking via defer in the outer loop.
func (f *FallbackChain) callProvider(
	ctx context.Context,
	idx int,
	provider port.LLM,
	systemPrompt, userMessage string,
	tools map[string]port.Tool,
) (string, error) {
	providerCtx := ctx
	var cancel context.CancelFunc
	if deadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(deadline)
		providersLeft := len(f.providers) - idx
		perProvider := remaining / time.Duration(providersLeft)
		if perProvider > 0 {
			providerCtx, cancel = context.WithTimeout(ctx, perProvider)
			defer cancel()
		}
	}
	return provider.Complete(providerCtx, systemPrompt, userMessage, tools)
}

func (f *FallbackChain) shouldFallback(err error) bool {
	if err == nil {
		return false
	}
	for _, cond := range f.fallbackOn {
		switch cond {
		case "timeout":
			if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
				return true
			}
		case "budget_exceeded":
			if errors.Is(err, domain.ErrBudgetExceeded) {
				return true
			}
		case "rate_limit":
			if errors.Is(err, domain.ErrRateLimitWeight) {
				return true
			}
		}
	}
	// Also fall back on generic LLM call errors.
	return errors.Is(err, ErrLLMCall)
}

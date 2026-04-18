package llm

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/azhar/cerebro/internal/domain"
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
// fallback condition, the next provider is tried. After all providers fail,
// returns domain.ErrAgentTimeout so the risk gate fails closed.
func (f *FallbackChain) Complete(
	ctx context.Context,
	systemPrompt string,
	userMessage string,
	tools map[string]port.Tool,
) (string, error) {
	for i, provider := range f.providers {
		result, err := provider.Complete(ctx, systemPrompt, userMessage, tools)
		if err == nil {
			return result, nil
		}

		if f.shouldFallback(err) {
			slog.Warn("LLM provider failed; trying next in chain",
				"provider", provider.Provider(),
				"model", provider.ModelID(),
				"attempt", i+1,
				"total", len(f.providers),
				"error", err,
			)
			continue
		}

		// Non-fallback error: return immediately.
		return "", err
	}

	slog.Error("all LLM providers exhausted; failing closed",
		"providers_tried", len(f.providers))
	return "", fmt.Errorf("%w: all LLM providers failed", domain.ErrAgentTimeout)
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

package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/azhar/cerebro/internal/domain"
)

func TestWrapAgentTimeout(t *testing.T) {
	// Error that comes out of the LLM fallback chain — already wrapped.
	alreadyWrapped := fmt.Errorf("%w: all LLM providers failed", domain.ErrAgentTimeout)

	// Plain error that the agent runtime sees (e.g. a validation error from
	// a tool).
	plain := errors.New("tool returned malformed JSON")

	tests := []struct {
		name           string
		in             error
		wantNil        bool
		wantIsTimeout  bool
		// When wantIsTimeout is true, we additionally assert the deadline
		// sentinel appears at most once in the formatted error.
	}{
		{
			name:    "nil in → nil out",
			in:      nil,
			wantNil: true,
		},
		{
			name:          "already wrapped ErrAgentTimeout is passed through",
			in:            alreadyWrapped,
			wantIsTimeout: true,
		},
		{
			name:          "plain error is wrapped exactly once",
			in:            plain,
			wantIsTimeout: true,
		},
	}

	sentinel := domain.ErrAgentTimeout.Error()

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := wrapAgentTimeout(tc.in)
			if tc.wantNil {
				if got != nil {
					t.Fatalf("wrapAgentTimeout(nil): want nil, got %v", got)
				}
				return
			}
			if !errors.Is(got, domain.ErrAgentTimeout) {
				t.Fatalf("errors.Is(..., ErrAgentTimeout) = false, want true (err=%v)", got)
			}
			// Sentinel must appear exactly once — no "deadline: deadline: ..."
			if n := strings.Count(got.Error(), sentinel); n != 1 {
				t.Errorf("ErrAgentTimeout appears %d times in %q; want exactly 1", n, got.Error())
			}
		})
	}
}

// TestWrapAgentTimeout_PreservesOriginalMessage guarantees that when we
// already-wrapped error is passed through, we don't lose the context that
// was attached to it.
func TestWrapAgentTimeout_PreservesOriginalMessage(t *testing.T) {
	in := fmt.Errorf("%w: openrouter returned 502", domain.ErrAgentTimeout)
	got := wrapAgentTimeout(in)
	if got != in {
		t.Errorf("expected pass-through (same pointer); got a new wrap: %q", got)
	}
	if !strings.Contains(got.Error(), "openrouter returned 502") {
		t.Errorf("original detail lost; got %q", got.Error())
	}
}

// TestIsTransient covers the retry-classification logic. The bug we fixed:
// the per-turn deadline error from an LLM adapter was being wrapped through
// FallbackChain → ErrAgentTimeout, so isTransient() never returned true and
// retry_on_transient was effectively dead. This locks the new behaviour in.
func TestIsTransient(t *testing.T) {
	// Mimic the wrap chain produced by adapter → FallbackChain in production:
	//   ErrAgentTimeout: all LLM providers failed: ErrLLMCall: openai: context deadline exceeded
	llmCall := errors.New("LLM API call failed") // mirror of llm.ErrLLMCall (avoid importing the adapter)
	wrapped := fmt.Errorf("%w: all LLM providers failed: %w: openai: %w",
		domain.ErrAgentTimeout, llmCall, context.DeadlineExceeded)

	circuitOpen := fmt.Errorf("%w: %w", domain.ErrAgentTimeout, domain.ErrCircuitOpen)

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil → not transient", nil, false},
		{"plain deadline → transient", context.DeadlineExceeded, true},
		{"plain canceled → transient", context.Canceled, true},
		{"adapter wrap preserving deadline → transient", wrapped, true},
		{"circuit breaker open → NOT transient", circuitOpen, false},
		{"random error → not transient", errors.New("nope"), false},
		{"ErrAgentTimeout without underlying deadline → not transient",
			fmt.Errorf("%w: all LLM providers failed", domain.ErrAgentTimeout), false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := isTransient(tc.err); got != tc.want {
				t.Errorf("isTransient(%v) = %v; want %v", tc.err, got, tc.want)
			}
		})
	}
}

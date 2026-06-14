package llm

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/azhar/cerebro/internal/domain"
	"github.com/azhar/cerebro/internal/port"
)

// stubLLM is a minimal port.LLM that returns a scripted (output, err) pair.
type stubLLM struct {
	provider string
	model    string
	out      string
	err      error
	calls    int
}

func (s *stubLLM) Provider() string { return s.provider }
func (s *stubLLM) ModelID() string  { return s.model }
func (s *stubLLM) Complete(_ context.Context, _, _ string, _ map[string]port.Tool) (string, error) {
	s.calls++
	return s.out, s.err
}

// TestFallbackChain_PreservesUnderlyingErrorChain verifies that when all
// providers fail, the FallbackChain wraps both ErrAgentTimeout AND the last
// underlying error, so the agent runtime can still see context.DeadlineExceeded
// and decide to retry. This is the exact bug that broke retries in production.
func TestFallbackChain_PreservesUnderlyingErrorChain(t *testing.T) {
	// Simulate the openai adapter's wrap: ErrLLMCall: openai: <deadline>.
	innerErr := fmt.Errorf("%w: openai: %w", ErrLLMCall, context.DeadlineExceeded)
	provider := &stubLLM{provider: "stub", model: "stub-1", err: innerErr}

	chain := NewFallbackChain([]port.LLM{provider}, []string{"timeout"})

	_, err := chain.Complete(context.Background(), "", "", nil)
	if err == nil {
		t.Fatal("expected error when single provider fails, got nil")
	}

	// Risk gate must still see ErrAgentTimeout for fail-closed behaviour.
	if !errors.Is(err, domain.ErrAgentTimeout) {
		t.Errorf("errors.Is(err, ErrAgentTimeout) = false, want true (err=%v)", err)
	}
	// Agent runtime must see context.DeadlineExceeded so retries fire.
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("errors.Is(err, context.DeadlineExceeded) = false, want true (err=%v)", err)
	}
	// The underlying ErrLLMCall must also be visible.
	if !errors.Is(err, ErrLLMCall) {
		t.Errorf("errors.Is(err, ErrLLMCall) = false, want true (err=%v)", err)
	}
}

// TestFallbackChain_ReturnsImmediatelyOnNonFallbackError ensures non-transient
// errors are not retried via the chain — they short-circuit immediately.
func TestFallbackChain_ReturnsImmediatelyOnNonFallbackError(t *testing.T) {
	bizErr := errors.New("400 bad request")
	p1 := &stubLLM{provider: "p1", err: bizErr}
	p2 := &stubLLM{provider: "p2", out: "should not be called"}

	chain := NewFallbackChain([]port.LLM{p1, p2}, []string{"timeout"})

	_, err := chain.Complete(context.Background(), "", "", nil)
	if err == nil || !errors.Is(err, bizErr) {
		t.Fatalf("want bizErr, got %v", err)
	}
	if p2.calls != 0 {
		t.Errorf("p2 called %d times; want 0 (non-fallback error must short-circuit)", p2.calls)
	}
}

// TestFallbackChain_FallsThroughOnTimeoutAndPreservesLastError ensures that
// a timeout on p1 falls through to p2, and if p2 also fails, the last error
// (from p2) is preserved in the wrap.
func TestFallbackChain_FallsThroughOnTimeoutAndPreservesLastError(t *testing.T) {
	p1Err := fmt.Errorf("%w: p1: %w", ErrLLMCall, context.DeadlineExceeded)
	p2Err := errors.New("p2 boom")
	p1 := &stubLLM{provider: "p1", err: p1Err}
	p2 := &stubLLM{provider: "p2", err: p2Err}

	chain := NewFallbackChain([]port.LLM{p1, p2}, []string{"timeout"})

	_, err := chain.Complete(context.Background(), "", "", nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if p2.calls != 1 {
		t.Errorf("p2 calls = %d; want 1 (chain should fall through)", p2.calls)
	}
	// p2 returned a non-fallback error → chain returns p2Err immediately
	// (without the ErrAgentTimeout wrap, since the chain only wraps when
	// the fallback path is exhausted via the `continue` branch).
	if !errors.Is(err, p2Err) {
		t.Errorf("expected last underlying p2Err in chain, got %v", err)
	}
}

// TestFallbackChain_AllTimeoutPreservesLastDeadline verifies that when EVERY
// provider in the chain returns a fallback-eligible timeout, the final wrap
// still preserves context.DeadlineExceeded so retries are possible.
func TestFallbackChain_AllTimeoutPreservesLastDeadline(t *testing.T) {
	p1Err := fmt.Errorf("%w: p1: %w", ErrLLMCall, context.DeadlineExceeded)
	p2Err := fmt.Errorf("%w: p2: %w", ErrLLMCall, context.DeadlineExceeded)
	p1 := &stubLLM{provider: "p1", err: p1Err}
	p2 := &stubLLM{provider: "p2", err: p2Err}

	chain := NewFallbackChain([]port.LLM{p1, p2}, []string{"timeout"})

	_, err := chain.Complete(context.Background(), "", "", nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, domain.ErrAgentTimeout) {
		t.Errorf("missing ErrAgentTimeout wrap: %v", err)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("underlying deadline lost; chain wrap broken: %v", err)
	}
}

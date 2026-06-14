package positionproposal

import (
	"context"
	"errors"
	"testing"

	"github.com/azhar/cerebro/internal/domain"
	"github.com/shopspring/decimal"
)

func mkProposal(sym domain.Symbol) Proposal {
	return Proposal{Symbol: sym, Venue: domain.VenueBinanceFutures, Side: domain.SideBuy,
		ProposedStop: decimal.NewFromInt(60000), ProposedTP: decimal.NewFromInt(70000)}
}

func TestProposeSupersedesSameSymbol(t *testing.T) {
	s := NewStore(func(context.Context, Proposal) error { return nil }, nil)
	s.Propose(mkProposal("BTC/USDT-PERP"))
	s.Propose(mkProposal("BTC/USDT-PERP"))
	if got := len(s.Pending()); got != 1 {
		t.Fatalf("pending = %d, want 1", got)
	}
}

func TestConfirmExecutesOnce(t *testing.T) {
	calls := 0
	s := NewStore(func(context.Context, Proposal) error { calls++; return nil }, nil)
	id := s.Propose(mkProposal("BTC/USDT-PERP"))
	if err := s.Confirm(context.Background(), id); err != nil {
		t.Fatalf("confirm: %v", err)
	}
	if calls != 1 {
		t.Fatalf("apply calls = %d, want 1", calls)
	}
	if err := s.Confirm(context.Background(), id); err == nil {
		t.Fatal("second confirm should error (unknown id)")
	}
}

func TestRejectDiscards(t *testing.T) {
	calls := 0
	s := NewStore(func(context.Context, Proposal) error { calls++; return nil }, nil)
	id := s.Propose(mkProposal("BTC/USDT-PERP"))
	if err := s.Reject(id); err != nil {
		t.Fatalf("reject: %v", err)
	}
	if calls != 0 || len(s.Pending()) != 0 {
		t.Fatalf("calls=%d pending=%d, want 0/0", calls, len(s.Pending()))
	}
}

func TestConfirmDropsWhenPositionGone(t *testing.T) {
	calls := 0
	s := NewStore(func(context.Context, Proposal) error { calls++; return nil }, nil)
	s.SetPositionExists(func(domain.Symbol) bool { return false })
	id := s.Propose(mkProposal("BTC/USDT-PERP"))
	if err := s.Confirm(context.Background(), id); err != nil {
		t.Fatalf("confirm: %v", err)
	}
	if calls != 0 {
		t.Fatalf("apply should not run when position gone, calls=%d", calls)
	}
	if len(s.Pending()) != 0 {
		t.Fatal("proposal should be dropped")
	}
}

func TestPrunePositionGone(t *testing.T) {
	s := NewStore(func(context.Context, Proposal) error { return nil }, nil)
	s.SetPositionExists(func(domain.Symbol) bool { return false })
	s.Propose(mkProposal("BTC/USDT-PERP"))
	s.Prune()
	if len(s.Pending()) != 0 {
		t.Fatal("prune should drop proposals whose position is gone")
	}
}

// TestConfirmApplyErrorKeepsPending verifies that when the injected ApplyFunc
// fails, the proposal stays pending (so the operator can retry) and the error
// propagates to the caller.
func TestConfirmApplyErrorKeepsPending(t *testing.T) {
	applyErr := errors.New("broker down")
	s := NewStore(func(context.Context, Proposal) error { return applyErr }, nil)
	id := s.Propose(mkProposal("BTC/USDT-PERP"))
	err := s.Confirm(context.Background(), id)
	if !errors.Is(err, applyErr) {
		t.Fatalf("want applyErr, got %v", err)
	}
	if len(s.Pending()) != 1 {
		t.Fatal("proposal must remain pending after apply error")
	}
}

// TestConfirmUnknownIDIsSentinel verifies Confirm and Reject return
// ErrUnknownProposal (matchable via errors.Is) for an unknown id, so the web
// handler can map it to a 404 without string-matching.
func TestConfirmUnknownIDIsSentinel(t *testing.T) {
	s := NewStore(func(context.Context, Proposal) error { return nil }, nil)
	if err := s.Confirm(context.Background(), "nope"); !errors.Is(err, ErrUnknownProposal) {
		t.Errorf("Confirm unknown id: want ErrUnknownProposal, got %v", err)
	}
	if err := s.Reject("nope"); !errors.Is(err, ErrUnknownProposal) {
		t.Errorf("Reject unknown id: want ErrUnknownProposal, got %v", err)
	}
}

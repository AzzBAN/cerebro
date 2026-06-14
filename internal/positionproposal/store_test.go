package positionproposal

import (
	"context"
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

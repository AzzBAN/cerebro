// Package positionproposal holds agent-originated SL/TP adjustment proposals
// that require explicit operator confirmation from the web dashboard. Unlike
// execution.ActionQueue, proposals never expire on a clock — they live until
// the operator confirms or rejects, or until the underlying position closes.
package positionproposal

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/azhar/cerebro/internal/domain"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// Proposal is a pending SL/TP adjustment awaiting operator confirmation.
type Proposal struct {
	ID           string
	Symbol       domain.Symbol
	Venue        domain.Venue
	Side         domain.Side
	CurrentStop  decimal.Decimal
	CurrentTP    decimal.Decimal
	ProposedStop decimal.Decimal
	ProposedTP   decimal.Decimal
	Reasoning    string
	CreatedAt    time.Time
}

// ApplyFunc executes a confirmed proposal (cancel exchange protection, place a
// new bracket at the proposed levels, record it). Implemented elsewhere and
// injected so the store has no broker dependency.
type ApplyFunc func(ctx context.Context, p Proposal) error

// Store holds pending proposals. Safe for concurrent use.
type Store struct {
	mu             sync.Mutex
	bySymbol       map[domain.Symbol]*Proposal // one live proposal per symbol
	byID           map[string]*Proposal
	apply          ApplyFunc
	positionExists func(domain.Symbol) bool
	onChange       func() // notified after any mutation so the UI can refresh
}

// NewStore builds a Store. apply executes confirmed proposals; onChange (may be
// nil) fires after every mutation so the caller can push a fresh snapshot.
func NewStore(apply ApplyFunc, onChange func()) *Store {
	return &Store{
		bySymbol: make(map[domain.Symbol]*Proposal),
		byID:     make(map[string]*Proposal),
		apply:    apply,
		onChange: onChange,
	}
}

// SetPositionExists installs the guard consulted before execution and during
// Prune. When it returns false for a symbol, that proposal is dropped.
func (s *Store) SetPositionExists(fn func(domain.Symbol) bool) {
	s.mu.Lock()
	s.positionExists = fn
	s.mu.Unlock()
}

// Propose adds or replaces the proposal for a symbol and returns its ID.
func (s *Store) Propose(p Proposal) string {
	if p.ID == "" {
		p.ID = uuid.New().String()
	}
	if p.CreatedAt.IsZero() {
		p.CreatedAt = time.Now().UTC()
	}
	s.mu.Lock()
	if old, ok := s.bySymbol[p.Symbol]; ok {
		delete(s.byID, old.ID) // supersede the previous proposal for this symbol
	}
	cp := p
	s.bySymbol[p.Symbol] = &cp
	s.byID[p.ID] = &cp
	s.mu.Unlock()
	slog.Info("proposal: enqueued", "id", p.ID, "symbol", p.Symbol,
		"proposed_stop", p.ProposedStop, "proposed_tp", p.ProposedTP)
	s.notify()
	return p.ID
}

// Pending returns a snapshot copy of all live proposals.
func (s *Store) Pending() []Proposal {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Proposal, 0, len(s.byID))
	for _, p := range s.byID {
		out = append(out, *p)
	}
	return out
}

// Confirm executes the proposal then removes it. Returns an error for an
// unknown ID. If the position is already gone the proposal is dropped and a
// nil error is returned (nothing to do).
func (s *Store) Confirm(ctx context.Context, id string) error {
	s.mu.Lock()
	p, ok := s.byID[id]
	if !ok {
		s.mu.Unlock()
		return fmt.Errorf("positionproposal: unknown id %q", id)
	}
	snapshot := *p
	guard := s.positionExists
	s.mu.Unlock()

	if guard != nil && !guard(snapshot.Symbol) {
		s.remove(snapshot.ID, snapshot.Symbol)
		slog.Info("proposal: dropped on confirm; position gone",
			"id", id, "symbol", snapshot.Symbol)
		return nil
	}
	if err := s.apply(ctx, snapshot); err != nil {
		// Keep the proposal pending so the operator can retry.
		return fmt.Errorf("positionproposal: apply %q: %w", id, err)
	}
	s.remove(snapshot.ID, snapshot.Symbol)
	slog.Info("proposal: confirmed and applied", "id", id, "symbol", snapshot.Symbol)
	return nil
}

// Reject removes a proposal without executing. Errors on an unknown ID.
func (s *Store) Reject(id string) error {
	s.mu.Lock()
	p, ok := s.byID[id]
	if !ok {
		s.mu.Unlock()
		return fmt.Errorf("positionproposal: unknown id %q", id)
	}
	sym := p.Symbol
	delete(s.byID, id)
	delete(s.bySymbol, sym)
	s.mu.Unlock()
	slog.Info("proposal: rejected", "id", id, "symbol", sym)
	s.notify()
	return nil
}

// Prune drops proposals whose position no longer exists. Call periodically.
func (s *Store) Prune() {
	s.mu.Lock()
	guard := s.positionExists
	var gone []*Proposal
	if guard != nil {
		for _, p := range s.byID {
			if !guard(p.Symbol) {
				gone = append(gone, p)
			}
		}
		for _, p := range gone {
			delete(s.byID, p.ID)
			delete(s.bySymbol, p.Symbol)
		}
	}
	s.mu.Unlock()
	for _, p := range gone {
		slog.Info("proposal: pruned; position gone", "id", p.ID, "symbol", p.Symbol)
	}
	if len(gone) > 0 {
		s.notify()
	}
}

func (s *Store) remove(id string, sym domain.Symbol) {
	s.mu.Lock()
	delete(s.byID, id)
	if cur, ok := s.bySymbol[sym]; ok && cur.ID == id {
		delete(s.bySymbol, sym)
	}
	s.mu.Unlock()
	s.notify()
}

func (s *Store) notify() {
	if s.onChange != nil {
		s.onChange()
	}
}

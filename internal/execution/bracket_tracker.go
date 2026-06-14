package execution

import (
	"sync"

	"github.com/azhar/cerebro/internal/domain"
)

// BracketTracker records which symbols currently have a protective bracket
// attached. It is the reconciler's source of truth for the "is this position
// protected?" question, updated by the Worker on bracket placement and by the
// reconciler/matcher when a bracket is cancelled or fires.
//
// Safe for concurrent use.
type BracketTracker struct {
	mu       sync.RWMutex
	brackets map[domain.Symbol]domain.BracketResponse
}

// NewBracketTracker returns an empty tracker.
func NewBracketTracker() *BracketTracker {
	return &BracketTracker{brackets: make(map[domain.Symbol]domain.BracketResponse)}
}

// Record marks a symbol as having a live bracket.
func (t *BracketTracker) Record(sym domain.Symbol, resp domain.BracketResponse) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.brackets[sym] = resp
}

// Has reports whether the symbol currently has a tracked bracket.
func (t *BracketTracker) Has(sym domain.Symbol) bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	_, ok := t.brackets[sym]
	return ok
}

// Get returns the tracked bracket for a symbol.
func (t *BracketTracker) Get(sym domain.Symbol) (domain.BracketResponse, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	resp, ok := t.brackets[sym]
	return resp, ok
}

// Remove clears the tracked bracket for a symbol.
func (t *BracketTracker) Remove(sym domain.Symbol) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.brackets, sym)
}

// Symbols returns the set of symbols with tracked brackets.
func (t *BracketTracker) Symbols() []domain.Symbol {
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := make([]domain.Symbol, 0, len(t.brackets))
	for s := range t.brackets {
		out = append(out, s)
	}
	return out
}

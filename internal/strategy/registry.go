package strategy

import (
	"fmt"
	"log/slog"

	"github.com/azhar/cerebro/internal/domain"
	"github.com/azhar/cerebro/internal/port"
)

// Registry maps strategy names to their implementations.
// Only enabled strategies from config are registered.
type Registry struct {
	strategies map[domain.StrategyName]port.Strategy
}

// NewRegistry creates an empty registry.
func NewRegistry() *Registry {
	return &Registry{strategies: make(map[domain.StrategyName]port.Strategy)}
}

// Register adds a strategy. Panics on duplicate names to catch wiring errors early.
func (r *Registry) Register(s port.Strategy) {
	if _, exists := r.strategies[s.Name()]; exists {
		panic(fmt.Sprintf("strategy registry: duplicate name %q", s.Name()))
	}
	r.strategies[s.Name()] = s
	slog.Info("strategy registered", "name", s.Name(), "symbols", s.Symbols())
}

// All returns all registered strategies.
func (r *Registry) All() []port.Strategy {
	out := make([]port.Strategy, 0, len(r.strategies))
	for _, s := range r.strategies {
		out = append(out, s)
	}
	return out
}

// Get returns a strategy by name.
func (r *Registry) Get(name domain.StrategyName) (port.Strategy, bool) {
	s, ok := r.strategies[name]
	return s, ok
}

// Len returns the number of registered strategies.
func (r *Registry) Len() int { return len(r.strategies) }

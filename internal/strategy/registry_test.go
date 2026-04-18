package strategy

import (
	"context"
	"testing"

	"github.com/azhar/cerebro/internal/domain"
	"github.com/azhar/cerebro/internal/port"
)

type mockStrategy struct {
	name    domain.StrategyName
	symbols []domain.Symbol
}

func (m *mockStrategy) Name() domain.StrategyName        { return m.name }
func (m *mockStrategy) Symbols() []domain.Symbol          { return m.symbols }
func (m *mockStrategy) Timeframes() []domain.Timeframe    { return nil }
func (m *mockStrategy) OnCandle(_ context.Context, _ domain.Candle) (domain.Signal, bool) {
	return domain.Signal{}, false
}
func (m *mockStrategy) Warmup(_ context.Context, _ []domain.Candle) {}

func TestRegistry_RegisterAndGet(t *testing.T) {
	r := NewRegistry()
	s := &mockStrategy{name: "test-strategy", symbols: []domain.Symbol{"BTC/USDT"}}

	r.Register(s)

	got, ok := r.Get("test-strategy")
	if !ok {
		t.Fatal("expected strategy to be found")
	}
	if got.Name() != "test-strategy" {
		t.Errorf("got name %q", got.Name())
	}
}

func TestRegistry_GetNotFound(t *testing.T) {
	r := NewRegistry()
	_, ok := r.Get("nonexistent")
	if ok {
		t.Error("expected not found")
	}
}

func TestRegistry_All(t *testing.T) {
	r := NewRegistry()
	r.Register(&mockStrategy{name: "s1"})
	r.Register(&mockStrategy{name: "s2"})

	all := r.All()
	if len(all) != 2 {
		t.Errorf("expected 2 strategies, got %d", len(all))
	}
}

func TestRegistry_Len(t *testing.T) {
	r := NewRegistry()
	if r.Len() != 0 {
		t.Error("expected 0 length")
	}
	r.Register(&mockStrategy{name: "s1"})
	if r.Len() != 1 {
		t.Errorf("expected 1, got %d", r.Len())
	}
}

func TestRegistry_DuplicatePanics(t *testing.T) {
	r := NewRegistry()
	r.Register(&mockStrategy{name: "dupe"})

	defer func() {
		if rec := recover(); rec == nil {
			t.Error("expected panic on duplicate")
		}
	}()
	r.Register(&mockStrategy{name: "dupe"})
}

// Ensure mockStrategy satisfies port.Strategy.
var _ port.Strategy = (*mockStrategy)(nil)

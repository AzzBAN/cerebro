package risk

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/azhar/cerebro/internal/config"
	"github.com/azhar/cerebro/internal/domain"
	"github.com/shopspring/decimal"
)

// stubCache implements port.Cache for testing.
type stubCache struct {
	mu   sync.RWMutex
	data map[string][]byte
}

func newStubCache() *stubCache {
	return &stubCache{data: make(map[string][]byte)}
}

func (s *stubCache) Set(_ context.Context, key string, value []byte, _ time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[key] = value
	return nil
}

func (s *stubCache) Get(_ context.Context, key string) ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.data[key]
	if !ok {
		return nil, nil
	}
	return v, nil
}

func (s *stubCache) Delete(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.data, key)
	return nil
}

func (s *stubCache) IncrBy(_ context.Context, key string, _ int64, _ time.Duration) (int64, error) {
	return 0, nil
}

func (s *stubCache) Keys(_ context.Context, _ string) ([]string, error) {
	return nil, nil
}

func (s *stubCache) Exists(_ context.Context, key string) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.data[key]
	return ok, nil
}

func makeSignal(symbol domain.Symbol) domain.Signal {
	return domain.Signal{
		ID:       "sig-1",
		Symbol:   symbol,
		Side:     domain.SideBuy,
		Strategy: "test",
	}
}

func TestGate_Check_AllowsValidSignal(t *testing.T) {
	g := NewGate(config.RiskConfig{}, newStubCache(), NewCalendarBlackout())
	err := g.Check(context.Background(), makeSignal("BTC/USDT"), nil)
	if err != nil {
		t.Fatalf("expected signal to pass, got %v", err)
	}
}

func TestGate_Check_HaltActive(t *testing.T) {
	g := NewGate(config.RiskConfig{}, newStubCache(), NewCalendarBlackout())
	g.SetHalt(domain.HaltModePause)

	err := g.Check(context.Background(), makeSignal("BTC/USDT"), nil)
	if err == nil {
		t.Fatal("expected rejection when halted")
	}
	if err.Error() != "trading halted; no new orders accepted: mode=pause" {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestGate_SetHalt_ClearHalt(t *testing.T) {
	g := NewGate(config.RiskConfig{}, newStubCache(), NewCalendarBlackout())

	if g.IsHalted() {
		t.Error("should not be halted initially")
	}

	g.SetHalt(domain.HaltModeFlatten)
	if !g.IsHalted() {
		t.Error("should be halted after SetHalt")
	}
	if mode := g.CurrentHaltMode(); mode == nil || *mode != domain.HaltModeFlatten {
		t.Errorf("expected flatten mode, got %v", mode)
	}

	g.ClearHalt()
	if g.IsHalted() {
		t.Error("should not be halted after ClearHalt")
	}
	if mode := g.CurrentHaltMode(); mode != nil {
		t.Errorf("expected nil mode, got %v", mode)
	}
}

func TestGate_TradingState(t *testing.T) {
	g := NewGate(config.RiskConfig{}, newStubCache(), NewCalendarBlackout())

	tests := []struct {
		name  string
		setup func()
		want  string
	}{
		{"running by default", func() {}, "running"},
		{"paused", func() { g.SetHalt(domain.HaltModePause) }, "paused"},
		{"flatten", func() { g.SetHalt(domain.HaltModeFlatten) }, "flatten"},
		{"pause_and_notify", func() { g.SetHalt(domain.HaltModePauseAndNotify) }, "pause_and_notify"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g.ClearHalt()
			tt.setup()
			if got := g.TradingState(); got != tt.want {
				t.Errorf("TradingState() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestGate_Check_MaxOpenPositions(t *testing.T) {
	cfg := config.RiskConfig{MaxOpenPositions: 2}
	g := NewGate(cfg, newStubCache(), NewCalendarBlackout())

	positions := []domain.Position{
		{Symbol: "BTC/USDT"},
		{Symbol: "ETH/USDT"},
	}

	err := g.Check(context.Background(), makeSignal("SOL/USDT"), positions)
	if err == nil {
		t.Fatal("expected rejection for max open positions")
	}
}

func TestGate_Check_MaxOpenPositionsPerSymbol(t *testing.T) {
	cfg := config.RiskConfig{MaxOpenPositionsPerSymbol: 1}
	g := NewGate(cfg, newStubCache(), NewCalendarBlackout())

	positions := []domain.Position{
		{Symbol: "BTC/USDT"},
	}

	err := g.Check(context.Background(), makeSignal("BTC/USDT"), positions)
	if err == nil {
		t.Fatal("expected rejection for max positions per symbol")
	}

	// Different symbol should pass.
	err = g.Check(context.Background(), makeSignal("ETH/USDT"), positions)
	if err != nil {
		t.Fatalf("different symbol should pass, got %v", err)
	}
}

func TestGate_Check_CalendarBlackout(t *testing.T) {
	cal := NewCalendarBlackout()
	cal.Update([]domain.EconomicEvent{
		{
			Title:       "NFP",
			Impact:      "high",
			Currency:    "USD",
			ScheduledAt: time.Now().UTC().Add(-10 * time.Minute), // 10 min ago, within 15 min after
		},
	})

	g := NewGate(config.RiskConfig{}, newStubCache(), cal)
	err := g.Check(context.Background(), makeSignal("BTC/USDT"), nil)
	if err == nil {
		t.Fatal("expected rejection during calendar blackout")
	}
}

func TestGate_UpdatePnL(t *testing.T) {
	g := NewGate(config.RiskConfig{}, newStubCache(), NewCalendarBlackout())

	g.UpdatePnL(decimal.NewFromInt(100))
	g.UpdatePnL(decimal.NewFromInt(-50))

	// Internal state is not exported, but we can verify no panic occurred.
	// If daily loss checks were enforced, they would read these values.
}

func TestGate_Check_ContextCancellation(t *testing.T) {
	g := NewGate(config.RiskConfig{}, newStubCache(), NewCalendarBlackout())

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// Gate should still work (it checks cache which respects ctx),
	// but cache stub ignores context anyway. Main point: no panic.
	err := g.Check(ctx, makeSignal("BTC/USDT"), nil)
	if err != nil {
		t.Fatalf("cancelled context should not cause error in gate, got %v", err)
	}
}

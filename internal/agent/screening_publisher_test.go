package agent

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/azhar/cerebro/internal/domain"
)

// noopCache is a minimal in-memory port.Cache stand-in used by the publisher
// test. It satisfies the interface methods that cacheBias actually exercises.
type noopCache struct{}

func (noopCache) Set(_ context.Context, _ string, _ []byte, _ time.Duration) error { return nil }
func (noopCache) Get(_ context.Context, _ string) ([]byte, error)                  { return nil, nil }
func (noopCache) Delete(_ context.Context, _ string) error                         { return nil }
func (noopCache) IncrBy(_ context.Context, _ string, _ int64, _ time.Duration) (int64, error) {
	return 0, nil
}
func (noopCache) Keys(_ context.Context, _ string) ([]string, error) { return nil, nil }
func (noopCache) Exists(_ context.Context, _ string) (bool, error)   { return false, nil }

// captureBias records every BiasResult sent to it so the test can assert.
type captureBias struct {
	mu     sync.Mutex
	calls  []domain.BiasResult
}

func (c *captureBias) SendBias(b domain.BiasResult) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls = append(c.calls, b)
}

func TestScreeningAgent_CacheBias_PublishesWhenSinkSet(t *testing.T) {
	cap := &captureBias{}
	s := &ScreeningAgent{
		cache:   noopCache{},
		biasTTL: 15 * time.Minute,
	}
	s.SetBiasPublisher(cap)

	if err := s.cacheBias(context.Background(), domain.Symbol("BTC/USDT"), "Bullish", "trend up"); err != nil {
		t.Fatalf("cacheBias: %v", err)
	}

	cap.mu.Lock()
	defer cap.mu.Unlock()
	if len(cap.calls) != 1 {
		t.Fatalf("expected 1 SendBias call, got %d", len(cap.calls))
	}
	got := cap.calls[0]
	if got.Symbol != domain.Symbol("BTC/USDT") {
		t.Errorf("Symbol = %q", got.Symbol)
	}
	if got.Score != domain.BiasBullish {
		t.Errorf("Score = %v, want Bullish", got.Score)
	}
	if got.Reasoning != "trend up" {
		t.Errorf("Reasoning = %q", got.Reasoning)
	}
	if got.CachedAt.IsZero() {
		t.Error("CachedAt should be set")
	}
}

func TestScreeningAgent_CacheBias_NoPublisherIsSilent(t *testing.T) {
	s := &ScreeningAgent{
		cache:   noopCache{},
		biasTTL: 15 * time.Minute,
	}
	// biasPub is nil; cacheBias must not panic and must succeed.
	if err := s.cacheBias(context.Background(), domain.Symbol("ETH/USDT"), "Bearish", ""); err != nil {
		t.Fatalf("cacheBias: %v", err)
	}
}

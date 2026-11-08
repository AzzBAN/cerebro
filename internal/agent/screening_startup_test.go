package agent

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/azhar/cerebro/internal/domain"
)

// TestStalePartition covers the restart freshness logic: a symbol needs a
// screen when its bias is missing or its CachedAt is older than one interval;
// otherwise it is fresh and contributes to the oldest-fresh alignment time.
func TestStalePartition(t *testing.T) {
	now := time.Date(2026, 6, 15, 2, 0, 0, 0, time.UTC)
	interval := time.Hour

	btc := domain.Symbol("BTC/USDT")
	eth := domain.Symbol("ETH/USDT")
	sol := domain.Symbol("SOL/USDT")

	// biasMap builds a lookup func from a symbol->CachedAt map. A symbol absent
	// from the map is reported as missing (ok=false), i.e. needs screening.
	biasMap := func(m map[domain.Symbol]time.Time) func(domain.Symbol) (domain.BiasResult, bool) {
		return func(sym domain.Symbol) (domain.BiasResult, bool) {
			ts, ok := m[sym]
			if !ok {
				return domain.BiasResult{}, false
			}
			return domain.BiasResult{Symbol: sym, CachedAt: ts}, true
		}
	}

	tests := []struct {
		name          string
		syms          []domain.Symbol
		cached        map[domain.Symbol]time.Time
		wantStale     []domain.Symbol
		wantHaveFresh bool
		wantOldest    time.Time
	}{
		{
			name:          "all fresh — none stale, oldest tracked",
			syms:          []domain.Symbol{btc, eth},
			cached:        map[domain.Symbol]time.Time{btc: now.Add(-30 * time.Minute), eth: now.Add(-10 * time.Minute)},
			wantStale:     nil,
			wantHaveFresh: true,
			wantOldest:    now.Add(-30 * time.Minute),
		},
		{
			name:          "all missing — all stale, no fresh",
			syms:          []domain.Symbol{btc, eth},
			cached:        map[domain.Symbol]time.Time{},
			wantStale:     []domain.Symbol{btc, eth},
			wantHaveFresh: false,
		},
		{
			name:          "mixed — only stale/missing returned, oldest fresh tracked",
			syms:          []domain.Symbol{btc, eth, sol},
			cached:        map[domain.Symbol]time.Time{btc: now.Add(-90 * time.Minute), eth: now.Add(-20 * time.Minute)},
			wantStale:     []domain.Symbol{btc, sol},
			wantHaveFresh: true,
			wantOldest:    now.Add(-20 * time.Minute),
		},
		{
			name:          "exactly one interval old is stale (boundary)",
			syms:          []domain.Symbol{btc},
			cached:        map[domain.Symbol]time.Time{btc: now.Add(-interval)},
			wantStale:     []domain.Symbol{btc},
			wantHaveFresh: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stale, oldest, haveFresh := stalePartition(tt.syms, biasMap(tt.cached), now, interval)

			if !equalSymbols(stale, tt.wantStale) {
				t.Errorf("stale = %v, want %v", stale, tt.wantStale)
			}
			if haveFresh != tt.wantHaveFresh {
				t.Errorf("haveFresh = %v, want %v", haveFresh, tt.wantHaveFresh)
			}
			if tt.wantHaveFresh && !oldest.Equal(tt.wantOldest) {
				t.Errorf("oldestFresh = %v, want %v", oldest, tt.wantOldest)
			}
		})
	}
}

func equalSymbols(a, b []domain.Symbol) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// storeCache is a minimal in-memory port.Cache that actually persists values,
// so hydration tests can pre-seed bias keys and read them back.
type storeCache struct {
	mu sync.Mutex
	m  map[string][]byte
}

func newStoreCache() *storeCache { return &storeCache{m: map[string][]byte{}} }

func (c *storeCache) Set(_ context.Context, k string, v []byte, _ time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.m[k] = v
	return nil
}

func (c *storeCache) Get(_ context.Context, k string) ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.m[k], nil
}

func (c *storeCache) Delete(_ context.Context, k string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.m, k)
	return nil
}

func (c *storeCache) IncrBy(_ context.Context, _ string, _ int64, _ time.Duration) (int64, error) {
	return 0, nil
}

func (c *storeCache) Keys(_ context.Context, pattern string) ([]string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	prefix := strings.TrimSuffix(pattern, "*")
	var out []string
	for k := range c.m {
		if strings.HasPrefix(k, prefix) {
			out = append(out, k)
		}
	}
	return out, nil
}

func (c *storeCache) Exists(_ context.Context, k string) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, ok := c.m[k]
	return ok, nil
}

func (c *storeCache) SetNX(_ context.Context, k string, v []byte, _ time.Duration) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.m[k]; ok {
		return false, nil
	}
	c.m[k] = append([]byte(nil), v...)
	return true, nil
}

// TestHydrateBiasUI verifies that cached biases are replayed into the UI sink
// on startup, and that hydration is a safe no-op without a publisher.
func TestHydrateBiasUI(t *testing.T) {
	ctx := context.Background()
	cache := newStoreCache()

	seed := func(sym domain.Symbol, score domain.BiasScore) {
		b, _ := json.Marshal(domain.BiasResult{Symbol: sym, Score: score, CachedAt: time.Now().UTC()})
		_ = cache.Set(ctx, "bias:"+string(sym), b, time.Hour)
	}
	seed("BTC/USDT", domain.BiasBullish)
	seed("ETH/USDT", domain.BiasBearish)

	t.Run("replays cached biases to sink", func(t *testing.T) {
		cap := &captureBias{}
		s := &ScreeningAgent{cache: cache, biasTTL: time.Hour}
		s.SetBiasPublisher(cap)

		s.hydrateUI(ctx)

		cap.mu.Lock()
		defer cap.mu.Unlock()
		if len(cap.calls) != 2 {
			t.Fatalf("expected 2 SendBias calls, got %d", len(cap.calls))
		}
	})

	t.Run("no publisher is a silent no-op", func(t *testing.T) {
		s := &ScreeningAgent{cache: cache, biasTTL: time.Hour}
		s.hydrateUI(ctx) // must not panic
	})

	t.Run("empty cache sends nothing", func(t *testing.T) {
		cap := &captureBias{}
		s := &ScreeningAgent{cache: newStoreCache(), biasTTL: time.Hour}
		s.SetBiasPublisher(cap)

		s.hydrateUI(ctx)

		cap.mu.Lock()
		defer cap.mu.Unlock()
		if len(cap.calls) != 0 {
			t.Fatalf("expected 0 SendBias calls, got %d", len(cap.calls))
		}
	})
}

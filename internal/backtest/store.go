package backtest

import (
	"context"
	"sync"
	"time"

	"github.com/azhar/cerebro/internal/domain"
	"github.com/azhar/cerebro/internal/port"
)

var _ port.TradeStore = (*MemTradeStore)(nil)
var _ port.Cache = (*MemCache)(nil)

// MemCache is an in-memory port.Cache implementation for backtests.
type MemCache struct {
	mu   sync.RWMutex
	data map[string][]byte
}

// NewMemCache creates a new in-memory cache.
func NewMemCache() *MemCache {
	return &MemCache{data: make(map[string][]byte)}
}

func (c *MemCache) Set(_ context.Context, key string, val []byte, _ time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.data[key] = append([]byte(nil), val...)
	return nil
}

func (c *MemCache) Get(_ context.Context, key string) ([]byte, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	v, ok := c.data[key]
	if !ok {
		return nil, nil
	}
	return append([]byte(nil), v...), nil
}

func (c *MemCache) Delete(_ context.Context, key string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.data, key)
	return nil
}

func (c *MemCache) IncrBy(_ context.Context, _ string, _ int64, _ time.Duration) (int64, error) {
	return 0, nil
}

func (c *MemCache) Keys(_ context.Context, _ string) ([]string, error) {
	return nil, nil
}

func (c *MemCache) Exists(_ context.Context, key string) (bool, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	_, ok := c.data[key]
	return ok, nil
}

func (c *MemCache) SetNX(_ context.Context, key string, val []byte, _ time.Duration) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.data[key]; ok {
		return false, nil
	}
	c.data[key] = append([]byte(nil), val...)
	return true, nil
}

// MemTradeStore is an in-memory port.TradeStore for backtests and simulation.
type MemTradeStore struct {
	mu      sync.Mutex
	intents map[string]domain.OrderIntent
	trades  []domain.Trade
}

// NewMemTradeStore creates an initialized MemTradeStore.
func NewMemTradeStore() *MemTradeStore {
	return &MemTradeStore{
		intents: make(map[string]domain.OrderIntent),
	}
}

func (s *MemTradeStore) SaveIntent(_ context.Context, i domain.OrderIntent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.intents[i.ID] = i
	return nil
}

func (s *MemTradeStore) UpdateIntentStatus(_ context.Context, id string, status domain.OrderStatus, _ string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if i, ok := s.intents[id]; ok {
		i.Status = status
		s.intents[id] = i
	}
	return nil
}

func (s *MemTradeStore) SaveTrade(_ context.Context, t domain.Trade) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.trades = append(s.trades, t)
	return nil
}

func (s *MemTradeStore) TradesByWindow(_ context.Context, from, to time.Time) ([]domain.Trade, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if from.IsZero() && to.IsZero() {
		out := make([]domain.Trade, len(s.trades))
		copy(out, s.trades)
		return out, nil
	}
	var out []domain.Trade
	for _, t := range s.trades {
		if !t.CreatedAt.Before(from) && !t.CreatedAt.After(to) {
			out = append(out, t)
		}
	}
	return out, nil
}

func (s *MemTradeStore) IntentsByWindow(_ context.Context, from, to time.Time) ([]domain.OrderIntent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []domain.OrderIntent
	for _, i := range s.intents {
		if !i.CreatedAt.Before(from) && !i.CreatedAt.After(to) {
			out = append(out, i)
		}
	}
	return out, nil
}

func (s *MemTradeStore) AllTrades() []domain.Trade {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]domain.Trade, len(s.trades))
	copy(out, s.trades)
	return out
}

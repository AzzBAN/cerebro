package execution

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

func TestDeduplicateOrder_Fresh(t *testing.T) {
	cache := newStubCache()
	fresh, err := DeduplicateOrder(context.Background(), cache, "order-123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !fresh {
		t.Error("first submission should be fresh")
	}
}

func TestDeduplicateOrder_Duplicate(t *testing.T) {
	cache := newStubCache()
	_, _ = DeduplicateOrder(context.Background(), cache, "order-123")

	fresh, err := DeduplicateOrder(context.Background(), cache, "order-123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fresh {
		t.Error("duplicate should not be fresh")
	}
}

func TestDeduplicateOrder_DifferentIDs(t *testing.T) {
	cache := newStubCache()
	_, _ = DeduplicateOrder(context.Background(), cache, "order-1")

	fresh, err := DeduplicateOrder(context.Background(), cache, "order-2")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !fresh {
		t.Error("different ID should be fresh")
	}
}

// Minimal stub cache for execution tests.
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

// keep import happy
var _ = decimal.NewFromInt(0)

// keep time import happy
var _ = time.Second

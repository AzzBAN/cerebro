package app

import (
	"context"
	"encoding/json"
	"log/slog"
	"path"
	"sync"
	"time"

	"github.com/azhar/cerebro/internal/domain"
)

// ── In-memory store implementations ──────────────────────────────────────────

type memoryTradeStore struct {
	mu      sync.Mutex
	intents map[string]domain.OrderIntent
	status  map[string]domain.OrderStatus
	trades  []domain.Trade
}

func newMemoryTradeStore() *memoryTradeStore {
	return &memoryTradeStore{
		intents: make(map[string]domain.OrderIntent),
		status:  make(map[string]domain.OrderStatus),
	}
}

func (m *memoryTradeStore) SaveIntent(_ context.Context, i domain.OrderIntent) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.intents[i.ID] = i
	m.status[i.ID] = domain.OrderStatusPending
	slog.Debug("trade store: intent saved", "id", i.ID, "symbol", i.Symbol, "side", i.Side)
	return nil
}

func (m *memoryTradeStore) UpdateIntentStatus(_ context.Context, id string, status domain.OrderStatus, brokerID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.status[id] = status
	slog.Debug("trade store: status updated", "id", id, "status", status, "broker_id", brokerID)
	return nil
}

func (m *memoryTradeStore) SaveTrade(_ context.Context, t domain.Trade) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.trades = append(m.trades, t)
	slog.Info("trade store: trade recorded",
		"id", t.ID, "symbol", t.Symbol, "side", t.Side,
		"qty", t.Quantity.StringFixed(6), "fill_price", t.FillPrice.StringFixed(4))
	return nil
}

func (m *memoryTradeStore) TradesByWindow(_ context.Context, from, to time.Time) ([]domain.Trade, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]domain.Trade, 0, len(m.trades))
	for _, t := range m.trades {
		if !t.CreatedAt.Before(from) && !t.CreatedAt.After(to) {
			out = append(out, t)
		}
	}
	return out, nil
}

type memoryAuditStore struct{}

func (m *memoryAuditStore) SaveEvent(_ context.Context, e domain.AuditEvent) error {
	slog.Debug("audit", "type", e.EventType, "actor", e.Actor)
	return nil
}

type memoryAgentLogStore struct {
	mu   sync.Mutex
	runs []domain.AgentRun
	msgs []domain.AgentMessage
}

func (m *memoryAgentLogStore) SaveRun(_ context.Context, r domain.AgentRun) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.runs = append(m.runs, r)
	slog.Debug("agent run saved",
		"agent", r.Agent, "model", r.Model, "latency_ms", r.LatencyMS, "outcome", r.Outcome)
	return nil
}

func (m *memoryAgentLogStore) RunsByWindow(_ context.Context, agentName string, from, to time.Time) ([]domain.AgentRun, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []domain.AgentRun
	for _, r := range m.runs {
		if string(r.Agent) == agentName && !r.CreatedAt.Before(from) && !r.CreatedAt.After(to) {
			out = append(out, r)
		}
	}
	return out, nil
}

func (m *memoryAgentLogStore) SaveMessage(_ context.Context, msg domain.AgentMessage) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.msgs = append(m.msgs, msg)
	slog.Debug("agent message saved", "role", msg.Role, "tool", msg.ToolName)
	return nil
}

type memoryCache struct {
	mu   sync.RWMutex
	data map[string]memoryValue
}

type memoryValue struct {
	payload   []byte
	expiresAt time.Time
}

func newMemoryCache() *memoryCache {
	return &memoryCache{data: make(map[string]memoryValue)}
}

func (m *memoryCache) Set(_ context.Context, key string, value []byte, ttl time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	v := memoryValue{payload: append([]byte(nil), value...)}
	if ttl > 0 {
		v.expiresAt = time.Now().Add(ttl)
	}
	m.data[key] = v
	slog.Debug("cache: set", "key", key, "ttl", ttl.String())
	return nil
}

func (m *memoryCache) Get(_ context.Context, key string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.data[key]
	if !ok {
		return nil, nil
	}
	if !v.expiresAt.IsZero() && time.Now().After(v.expiresAt) {
		delete(m.data, key)
		return nil, nil
	}
	return append([]byte(nil), v.payload...), nil
}

func (m *memoryCache) Delete(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.data, key)
	return nil
}

func (m *memoryCache) IncrBy(ctx context.Context, key string, delta int64, ttl time.Duration) (int64, error) {
	curr, err := m.Get(ctx, key)
	if err != nil {
		return 0, err
	}
	var n int64
	if len(curr) > 0 {
		_ = json.Unmarshal(curr, &n)
	}
	n += delta
	b, _ := json.Marshal(n)
	return n, m.Set(ctx, key, b, ttl)
}

func (m *memoryCache) Keys(_ context.Context, patternExpr string) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var keys []string
	for k, v := range m.data {
		if !v.expiresAt.IsZero() && time.Now().After(v.expiresAt) {
			delete(m.data, k)
			continue
		}
		if matched, err := path.Match(patternExpr, k); err == nil && matched {
			keys = append(keys, k)
		}
	}
	return keys, nil
}

func (m *memoryCache) Exists(_ context.Context, key string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.data[key]
	if !ok {
		return false, nil
	}
	if !v.expiresAt.IsZero() && time.Now().After(v.expiresAt) {
		delete(m.data, key)
		return false, nil
	}
	return true, nil
}

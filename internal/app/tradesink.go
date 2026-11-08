package app

import (
	"context"
	"sync"
	"time"

	"github.com/azhar/cerebro/internal/domain"
	"github.com/azhar/cerebro/internal/port"
)

// broadcastingTradeStore wraps an inner port.TradeStore and notifies every
// registered port.TradeSink on every successful SaveTrade. The push is
// best-effort: a panic in any sink is recovered so the engine hot path
// never blocks on a misbehaving UI adapter. The store is concurrency-safe
// (the inner store is too), and the slice of sinks is replaced atomically
// via AddSink so runtime wiring can register the web dashboard after the
// store is built.
type broadcastingTradeStore struct {
	inner port.TradeStore
	mu    sync.RWMutex
	sinks []port.TradeSink
}

func newBroadcastingTradeStore(inner port.TradeStore) *broadcastingTradeStore {
	return &broadcastingTradeStore{inner: inner}
}

// AddSink registers a notifier. Safe to call from any goroutine; the
// registration is applied under a write lock so a concurrent SaveTrade
// never sees a torn slice.
func (b *broadcastingTradeStore) AddSink(s port.TradeSink) {
	if s == nil {
		return
	}
	b.mu.Lock()
	b.sinks = append(b.sinks, s)
	b.mu.Unlock()
}

func (b *broadcastingTradeStore) SaveIntent(ctx context.Context, i domain.OrderIntent) error {
	return b.inner.SaveIntent(ctx, i)
}

func (b *broadcastingTradeStore) UpdateIntentStatus(ctx context.Context, id string, status domain.OrderStatus, brokerID string) error {
	return b.inner.UpdateIntentStatus(ctx, id, status, brokerID)
}

// SaveTrade persists the trade and, on success, fans out to every sink.
// Sinks run synchronously inside SaveTrade; they must not block — a slow
// WebSocket client will eventually backpressure the broadcast queue and
// be dropped by the dashboard's own logic.
func (b *broadcastingTradeStore) SaveTrade(ctx context.Context, t domain.Trade) error {
	if err := b.inner.SaveTrade(ctx, t); err != nil {
		return err
	}
	b.mu.RLock()
	sinks := b.sinks
	b.mu.RUnlock()
	for _, s := range sinks {
		safeSendTrade(s, t)
	}
	return nil
}

// safeSendTrade isolates a sink call from panics so a misbehaving UI can
// never bring down the engine.
func safeSendTrade(s port.TradeSink, t domain.Trade) {
	defer func() {
		_ = recover()
	}()
	s.SendTrade(t)
}

func (b *broadcastingTradeStore) TradesByWindow(ctx context.Context, from, to time.Time) ([]domain.Trade, error) {
	return b.inner.TradesByWindow(ctx, from, to)
}

func (b *broadcastingTradeStore) IntentsByWindow(ctx context.Context, from, to time.Time) ([]domain.OrderIntent, error) {
	return b.inner.IntentsByWindow(ctx, from, to)
}

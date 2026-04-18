package paper

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/azhar/cerebro/internal/domain"
	"github.com/shopspring/decimal"
)

type stubTradeStore struct {
	mu     sync.Mutex
	trades []domain.Trade
}

func (s *stubTradeStore) SaveIntent(_ context.Context, _ domain.OrderIntent) error { return nil }
func (s *stubTradeStore) UpdateIntentStatus(_ context.Context, _ string, _ domain.OrderStatus, _ string) error {
	return nil
}
func (s *stubTradeStore) SaveTrade(_ context.Context, t domain.Trade) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.trades = append(s.trades, t)
	return nil
}
func (s *stubTradeStore) TradesByWindow(_ context.Context, _, _ time.Time) ([]domain.Trade, error) {
	return nil, nil
}

func TestMatcher_PlaceOrder(t *testing.T) {
	book := NewBook()
	store := &stubTradeStore{}
	m := NewMatcher(book, store, 0.04) // 0.04% commission

	intent := domain.OrderIntent{
		ID:       "test-1",
		Symbol:   "BTC/USDT",
		Side:     domain.SideBuy,
		Quantity: decimal.NewFromInt(1),
	}

	brokerID, err := m.PlaceOrder(context.Background(), intent)
	if err != nil {
		t.Fatalf("PlaceOrder error: %v", err)
	}
	if brokerID != "paper-test-1" {
		t.Errorf("broker ID = %q, want %q", brokerID, "paper-test-1")
	}

	orders := book.OpenOrders()
	if len(orders) != 1 {
		t.Fatalf("expected 1 open order, got %d", len(orders))
	}
}

func TestMatcher_Venue(t *testing.T) {
	m := NewMatcher(NewBook(), &stubTradeStore{}, 0)
	if m.Venue() != domain.VenueBinanceSpot {
		t.Errorf("Venue() = %q", m.Venue())
	}
}

func TestMatcher_Connect(t *testing.T) {
	m := NewMatcher(NewBook(), &stubTradeStore{}, 0)
	if err := m.Connect(context.Background()); err != nil {
		t.Errorf("Connect() error: %v", err)
	}
}

func TestMatcher_OnCandle(t *testing.T) {
	book := NewBook()
	store := &stubTradeStore{}
	m := NewMatcher(book, store, 0.04)

	intent := domain.OrderIntent{
		ID:       "candle-1",
		Symbol:   "BTC/USDT",
		Side:     domain.SideBuy,
		Quantity: decimal.NewFromFloat(0.5),
	}
	m.PlaceOrder(context.Background(), intent)

	candle := domain.Candle{
		Symbol:    "BTC/USDT",
		Open:      decimal.NewFromInt(50000),
		Close:     decimal.NewFromInt(50500),
		OpenTime:  time.Now().UTC(),
		CloseTime: time.Now().UTC(),
	}

	m.OnCandle(context.Background(), candle)

	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.trades) != 1 {
		t.Fatalf("expected 1 trade saved, got %d", len(store.trades))
	}
	trade := store.trades[0]
	if !trade.FillPrice.Equal(decimal.NewFromInt(50000)) {
		t.Errorf("fill price = %s, want 50000", trade.FillPrice)
	}

	// Verify fees: qty * price * commission = 0.5 * 50000 * 0.0004 = 10
	expectedFee := decimal.NewFromFloat(0.5).Mul(decimal.NewFromInt(50000)).Mul(decimal.NewFromFloat(0.0004))
	if !trade.Fees.Equal(expectedFee) {
		t.Errorf("fees = %s, want %s", trade.Fees, expectedFee)
	}
}

func TestMatcher_OnCandle_SymbolMismatch(t *testing.T) {
	book := NewBook()
	store := &stubTradeStore{}
	m := NewMatcher(book, store, 0.04)

	intent := domain.OrderIntent{
		ID:       "sym-1",
		Symbol:   "BTC/USDT",
		Side:     domain.SideBuy,
		Quantity: decimal.NewFromInt(1),
	}
	m.PlaceOrder(context.Background(), intent)

	candle := domain.Candle{
		Symbol:    "ETH/USDT",
		Open:      decimal.NewFromInt(3000),
		OpenTime:  time.Now().UTC(),
		CloseTime: time.Now().UTC(),
	}

	m.OnCandle(context.Background(), candle)

	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.trades) != 0 {
		t.Error("should not fill order for different symbol")
	}
}

func TestMatcher_Positions(t *testing.T) {
	book := NewBook()
	m := NewMatcher(book, &stubTradeStore{}, 0)

	positions, err := m.Positions(context.Background())
	if err != nil {
		t.Fatalf("Positions error: %v", err)
	}
	if len(positions) != 0 {
		t.Errorf("expected 0 positions, got %d", len(positions))
	}
}

func TestMatcher_AutoFillAll(t *testing.T) {
	book := NewBook()
	store := &stubTradeStore{}
	m := NewMatcher(book, store, 0)

	book.AddOrder(domain.OrderIntent{ID: "af1", Symbol: "BTC/USDT", Quantity: decimal.NewFromInt(1)})
	book.AddOrder(domain.OrderIntent{ID: "af2", Symbol: "ETH/USDT", Quantity: decimal.NewFromInt(2)})

	prices := map[domain.Symbol]decimal.Decimal{
		"BTC/USDT": decimal.NewFromInt(50000),
		"ETH/USDT": decimal.NewFromInt(3000),
	}

	m.AutoFillAll(context.Background(), prices)

	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.trades) != 2 {
		t.Fatalf("expected 2 trades, got %d", len(store.trades))
	}
}

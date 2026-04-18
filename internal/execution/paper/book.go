package paper

import (
	"sync"
	"time"

	"github.com/azhar/cerebro/internal/domain"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// PaperOrder tracks an open simulated order waiting for fill.
type PaperOrder struct {
	Intent    domain.OrderIntent
	Status    domain.OrderStatus
	CreatedAt time.Time
}

// Book is an in-memory paper order book.
// It holds open orders and simulates fills when new candle data arrives.
type Book struct {
	mu        sync.Mutex
	orders    map[string]*PaperOrder // keyed by Intent.ID
	positions map[domain.Symbol]*domain.Position
}

// NewBook creates an empty paper order book.
func NewBook() *Book {
	return &Book{
		orders:    make(map[string]*PaperOrder),
		positions: make(map[domain.Symbol]*domain.Position),
	}
}

// AddOrder places a new simulated order.
func (b *Book) AddOrder(intent domain.OrderIntent) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.orders[intent.ID] = &PaperOrder{
		Intent:    intent,
		Status:    domain.OrderStatusPending,
		CreatedAt: time.Now().UTC(),
	}
}

// OpenOrders returns all pending paper orders.
func (b *Book) OpenOrders() []PaperOrder {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]PaperOrder, 0, len(b.orders))
	for _, o := range b.orders {
		out = append(out, *o)
	}
	return out
}

// Positions returns all currently open paper positions.
func (b *Book) Positions() []domain.Position {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]domain.Position, 0, len(b.positions))
	for _, p := range b.positions {
		out = append(out, *p)
	}
	return out
}

// Fill marks an order as filled and creates/updates a position.
func (b *Book) Fill(orderID string, fillPrice decimal.Decimal, fillTime time.Time) domain.Trade {
	b.mu.Lock()
	defer b.mu.Unlock()

	order, ok := b.orders[orderID]
	if !ok {
		return domain.Trade{}
	}
	order.Status = domain.OrderStatusFilled
	delete(b.orders, orderID)

	intent := order.Intent
	pos := &domain.Position{
		Symbol:        intent.Symbol,
		Venue:         intent.Venue,
		Side:          intent.Side,
		Quantity:      intent.Quantity,
		EntryPrice:    fillPrice,
		CurrentPrice:  fillPrice,
		StopLoss:      intent.StopLoss,
		TakeProfit1:   intent.TakeProfit1,
		Strategy:      intent.Strategy,
		CorrelationID: intent.CorrelationID,
		OpenedAt:      fillTime,
	}
	b.positions[intent.Symbol] = pos

	return domain.Trade{
		ID:            uuid.New().String(),
		IntentID:      intent.ID,
		CorrelationID: intent.CorrelationID,
		Symbol:        intent.Symbol,
		Side:          intent.Side,
		Quantity:      intent.Quantity,
		FillPrice:     fillPrice,
		Strategy:      intent.Strategy,
		Venue:         intent.Venue,
		CreatedAt:     fillTime,
	}
}

// UpdatePrice updates the current price of a position (for PnL display).
func (b *Book) UpdatePrice(symbol domain.Symbol, price decimal.Decimal) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if pos, ok := b.positions[symbol]; ok {
		pos.CurrentPrice = price
	}
}

// CancelExpired cancels orders older than maxAge.
func (b *Book) CancelExpired(maxAge time.Duration) []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	var cancelled []string
	for id, o := range b.orders {
		if time.Since(o.CreatedAt) > maxAge {
			o.Status = domain.OrderStatusCancelled
			cancelled = append(cancelled, id)
			delete(b.orders, id)
		}
	}
	return cancelled
}

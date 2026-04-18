package paper

import (
	"testing"
	"time"

	"github.com/azhar/cerebro/internal/domain"
	"github.com/shopspring/decimal"
)

func makeIntent(id string, symbol domain.Symbol) domain.OrderIntent {
	return domain.OrderIntent{
		ID:       id,
		Symbol:   symbol,
		Side:     domain.SideBuy,
		Quantity: decimal.NewFromInt(1),
	}
}

func TestBook_AddOrder(t *testing.T) {
	book := NewBook()
	book.AddOrder(makeIntent("o1", "BTC/USDT"))

	orders := book.OpenOrders()
	if len(orders) != 1 {
		t.Fatalf("expected 1 open order, got %d", len(orders))
	}
	if orders[0].Intent.ID != "o1" {
		t.Errorf("got order ID %q, want %q", orders[0].Intent.ID, "o1")
	}
	if orders[0].Status != domain.OrderStatusPending {
		t.Errorf("expected pending status, got %s", orders[0].Status)
	}
}

func TestBook_Fill(t *testing.T) {
	book := NewBook()
	book.AddOrder(makeIntent("o1", "BTC/USDT"))

	fillPrice := decimal.NewFromInt(50000)
	fillTime := time.Now().UTC()
	trade := book.Fill("o1", fillPrice, fillTime)

	if trade.ID == "" {
		t.Fatal("expected trade ID")
	}
	if trade.IntentID != "o1" {
		t.Errorf("trade intent ID = %q, want %q", trade.IntentID, "o1")
	}
	if !trade.FillPrice.Equal(fillPrice) {
		t.Errorf("fill price = %s, want %s", trade.FillPrice, fillPrice)
	}
	if !trade.Quantity.Equal(decimal.NewFromInt(1)) {
		t.Errorf("quantity = %s, want 1", trade.Quantity)
	}

	// Order should be removed.
	orders := book.OpenOrders()
	if len(orders) != 0 {
		t.Errorf("expected 0 open orders after fill, got %d", len(orders))
	}

	// Position should exist.
	positions := book.Positions()
	if len(positions) != 1 {
		t.Fatalf("expected 1 position, got %d", len(positions))
	}
	if positions[0].Symbol != "BTC/USDT" {
		t.Errorf("position symbol = %q", positions[0].Symbol)
	}
	if !positions[0].EntryPrice.Equal(fillPrice) {
		t.Errorf("entry price = %s, want %s", positions[0].EntryPrice, fillPrice)
	}
}

func TestBook_FillUnknownOrder(t *testing.T) {
	book := NewBook()
	trade := book.Fill("nonexistent", decimal.NewFromInt(100), time.Now().UTC())
	if trade.ID != "" {
		t.Error("expected empty trade for unknown order")
	}
}

func TestBook_UpdatePrice(t *testing.T) {
	book := NewBook()
	book.AddOrder(makeIntent("o1", "BTC/USDT"))
	book.Fill("o1", decimal.NewFromInt(50000), time.Now().UTC())

	newPrice := decimal.NewFromInt(55000)
	book.UpdatePrice("BTC/USDT", newPrice)

	positions := book.Positions()
	if len(positions) != 1 {
		t.Fatalf("expected 1 position, got %d", len(positions))
	}
	if !positions[0].CurrentPrice.Equal(newPrice) {
		t.Errorf("current price = %s, want %s", positions[0].CurrentPrice, newPrice)
	}
}

func TestBook_UpdatePriceUnknownSymbol(t *testing.T) {
	book := NewBook()
	book.UpdatePrice("ETH/USDT", decimal.NewFromInt(3000))
	// No panic, no position created.
	positions := book.Positions()
	if len(positions) != 0 {
		t.Errorf("expected 0 positions, got %d", len(positions))
	}
}

func TestBook_CancelExpired(t *testing.T) {
	book := NewBook()
	book.AddOrder(makeIntent("o1", "BTC/USDT"))

	// Use a negative duration to guarantee time.Since(createdAt) > maxAge.
	// This tests the cancellation path without relying on real time delays.
	cancelled := book.CancelExpired(-1 * time.Second)
	if len(cancelled) != 1 {
		t.Fatalf("expected 1 cancelled, got %d", len(cancelled))
	}
	if cancelled[0] != "o1" {
		t.Errorf("cancelled ID = %q, want %q", cancelled[0], "o1")
	}

	orders := book.OpenOrders()
	if len(orders) != 0 {
		t.Errorf("expected 0 open orders after cancel, got %d", len(orders))
	}
}

func TestBook_CancelExpiredNoneExpired(t *testing.T) {
	book := NewBook()
	book.AddOrder(makeIntent("o1", "BTC/USDT"))

	// Large max age — nothing should be cancelled.
	cancelled := book.CancelExpired(24 * time.Hour)
	if len(cancelled) != 0 {
		t.Errorf("expected 0 cancelled, got %d", len(cancelled))
	}
}

func TestBook_MultipleOrdersAndFills(t *testing.T) {
	book := NewBook()
	book.AddOrder(makeIntent("o1", "BTC/USDT"))
	book.AddOrder(makeIntent("o2", "ETH/USDT"))

	if len(book.OpenOrders()) != 2 {
		t.Fatal("expected 2 open orders")
	}

	book.Fill("o1", decimal.NewFromInt(50000), time.Now().UTC())
	book.Fill("o2", decimal.NewFromInt(3000), time.Now().UTC())

	if len(book.OpenOrders()) != 0 {
		t.Error("all orders should be filled")
	}
	positions := book.Positions()
	if len(positions) != 2 {
		t.Errorf("expected 2 positions, got %d", len(positions))
	}
}

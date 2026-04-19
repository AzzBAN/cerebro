package spot

import (
	"context"
	"testing"

	"github.com/azhar/cerebro/internal/domain"
	"github.com/shopspring/decimal"
)

func TestHandleUserDataMessage_BalanceUpdateRemovesPosition(t *testing.T) {
	b := NewSpotBroker(nil, "mainnet", []domain.Symbol{"BTC/USDT"})

	// Pre-populate a BTC position.
	b.mu.Lock()
	b.balances["BTC"] = spotBalance{
		free:   decimal.NewFromFloat(0.5),
		locked: decimal.Zero,
	}
	b.positions = map[domain.Symbol]domain.Position{
		"BTC/USDT": {
			Symbol:   "BTC/USDT",
			Venue:    domain.VenueBinanceSpot,
			Side:     domain.SideBuy,
			Quantity: decimal.NewFromFloat(0.5),
		},
	}
	b.mu.Unlock()

	// Simulate the Binance WS API wrapping a balance update in an "event" field,
	// where BTC balance is now zero (sold everything).
	msg := `{"event":{"e":"outboundAccountPosition","E":1745000000000,"B":[{"a":"BTC","f":"0","l":"0"},{"a":"USDT","f":"50000","l":"0"}]}}`

	if err := b.handleUserDataMessage([]byte(msg)); err != nil {
		t.Fatalf("handleUserDataMessage: %v", err)
	}

	positions, _ := b.Positions(context.Background())
	for _, p := range positions {
		if p.Symbol == "BTC/USDT" {
			t.Errorf("BTC/USDT position should have been removed after balance went to zero, got: %+v", p)
		}
	}
}

func TestHandleUserDataMessage_BalanceUpdateKeepsPosition(t *testing.T) {
	b := NewSpotBroker(nil, "mainnet", []domain.Symbol{"BTC/USDT"})

	// Pre-populate.
	b.mu.Lock()
	b.balances["BTC"] = spotBalance{
		free:   decimal.NewFromFloat(1.0),
		locked: decimal.Zero,
	}
	b.positions = map[domain.Symbol]domain.Position{
		"BTC/USDT": {
			Symbol:   "BTC/USDT",
			Venue:    domain.VenueBinanceSpot,
			Side:     domain.SideBuy,
			Quantity: decimal.NewFromFloat(1.0),
		},
	}
	b.mu.Unlock()

	// Partial sell: BTC balance drops from 1.0 to 0.5.
	msg := `{"event":{"e":"outboundAccountPosition","E":1745000000000,"B":[{"a":"BTC","f":"0.5","l":"0"},{"a":"USDT","f":"50000","l":"0"}]}}`

	if err := b.handleUserDataMessage([]byte(msg)); err != nil {
		t.Fatalf("handleUserDataMessage: %v", err)
	}

	positions, _ := b.Positions(context.Background())
	found := false
	for _, p := range positions {
		if p.Symbol == "BTC/USDT" {
			found = true
			if !p.Quantity.Equal(decimal.NewFromFloat(0.5)) {
				t.Errorf("expected quantity 0.5, got %s", p.Quantity)
			}
		}
	}
	if !found {
		t.Error("BTC/USDT position should still exist with 0.5 quantity")
	}
}

func TestHandleUserDataMessage_AckMessage(t *testing.T) {
	b := NewSpotBroker(nil, "mainnet", nil)
	// Subscription ack should be silently ignored.
	msg := `{"id":"sub-1","status":200,"result":null}`
	if err := b.handleUserDataMessage([]byte(msg)); err != nil {
		t.Fatalf("ack should not error: %v", err)
	}
}

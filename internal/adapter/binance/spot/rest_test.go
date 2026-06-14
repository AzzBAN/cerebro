package spot

import (
	"context"
	"testing"

	"github.com/azhar/cerebro/internal/domain"
	"github.com/shopspring/decimal"
)

func TestHandleUserDataMessage_BalanceUpdateRemovesPosition(t *testing.T) {
	b := NewSpotBroker(nil, "mainnet", []domain.Symbol{"BTC/USDT"}, nil)

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
	b := NewSpotBroker(nil, "mainnet", []domain.Symbol{"BTC/USDT"}, nil)

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

func TestRebuildPositions_DustFiltered(t *testing.T) {
	minLots := map[domain.Symbol]decimal.Decimal{
		"BTC/USDT": decimal.NewFromFloat(0.00001),
	}
	b := NewSpotBroker(nil, "mainnet", []domain.Symbol{"BTC/USDT"}, minLots)

	// Simulate a dust balance below the lot size.
	b.mu.Lock()
	b.balances["BTC"] = spotBalance{
		free:   decimal.NewFromFloat(0.00000814), // below lot_size 0.00001
		locked: decimal.Zero,
	}
	b.rebuildPositionsLocked()
	b.mu.Unlock()

	positions, _ := b.Positions(context.Background())
	for _, p := range positions {
		if p.Symbol == "BTC/USDT" {
			t.Errorf("BTC/USDT dust balance should have been filtered out, got qty=%s", p.Quantity)
		}
	}
}

func TestRebuildPositions_AboveLotSizeKept(t *testing.T) {
	minLots := map[domain.Symbol]decimal.Decimal{
		"BTC/USDT": decimal.NewFromFloat(0.00001),
	}
	b := NewSpotBroker(nil, "mainnet", []domain.Symbol{"BTC/USDT"}, minLots)

	// Balance above the lot size should be kept.
	b.mu.Lock()
	b.balances["BTC"] = spotBalance{
		free:   decimal.NewFromFloat(0.001),
		locked: decimal.Zero,
	}
	b.rebuildPositionsLocked()
	b.mu.Unlock()

	positions, _ := b.Positions(context.Background())
	found := false
	for _, p := range positions {
		if p.Symbol == "BTC/USDT" {
			found = true
		}
	}
	if !found {
		t.Error("BTC/USDT position above lot size should be kept")
	}
}

func TestHandleUserDataMessage_AckMessage(t *testing.T) {
	b := NewSpotBroker(nil, "mainnet", nil, nil)
	// Subscription ack should be silently ignored.
	msg := `{"id":"sub-1","status":200,"result":null}`
	if err := b.handleUserDataMessage([]byte(msg)); err != nil {
		t.Fatalf("ack should not error: %v", err)
	}
}

// TestApplyBalanceSnapshot_Resync verifies the periodic REST resync path:
// balances are REPLACED wholesale (not merged) so an asset sold to zero — which
// may be absent from the fresh snapshot entirely — has its position dropped,
// while a newly-funded asset becomes a position. Cerebro-internal metadata on
// survivors is preserved via rebuildPositionsLocked. This recovers state the
// user-data WS may have missed.
func TestApplyBalanceSnapshot_Resync(t *testing.T) {
	b := NewSpotBroker(nil, "mainnet", []domain.Symbol{"BTC/USDT", "ETH/USDT"}, nil)

	// Cache: ETH position (with internal metadata) that has since been closed,
	// plus a stale balance entry for it.
	b.mu.Lock()
	b.balances["ETH"] = spotBalance{free: decimal.NewFromFloat(2.0), locked: decimal.Zero}
	b.positions = map[domain.Symbol]domain.Position{
		"ETH/USDT": {
			Symbol:        "ETH/USDT",
			Venue:         domain.VenueBinanceSpot,
			Side:          domain.SideBuy,
			Quantity:      decimal.NewFromFloat(2.0),
			StopLoss:      decimal.NewFromFloat(2800),
			TakeProfit1:   decimal.NewFromFloat(3500),
			Strategy:      domain.StrategyName("rsi_bb"),
			CorrelationID: "corr-eth",
		},
	}
	b.mu.Unlock()

	// Fresh REST snapshot: ETH gone (sold), BTC newly held.
	snapshot := map[string]spotBalance{
		"BTC":  {free: decimal.NewFromFloat(0.5), locked: decimal.Zero},
		"USDT": {free: decimal.NewFromFloat(10000), locked: decimal.Zero},
	}

	b.applyBalanceSnapshot(snapshot)

	positions, _ := b.Positions(context.Background())
	gotSyms := map[domain.Symbol]bool{}
	for _, p := range positions {
		gotSyms[p.Symbol] = true
	}
	if gotSyms["ETH/USDT"] {
		t.Error("ETH/USDT should be dropped after it disappeared from the balance snapshot")
	}
	if !gotSyms["BTC/USDT"] {
		t.Error("BTC/USDT should be added from the balance snapshot")
	}
}

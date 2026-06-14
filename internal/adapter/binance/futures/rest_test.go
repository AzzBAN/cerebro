package futures

import (
	"testing"

	"github.com/azhar/cerebro/internal/domain"
	"github.com/shopspring/decimal"
)

// TestHandleAccountUpdate_MarginAndIsolated verifies that ACCOUNT_UPDATE
// payloads from the user-data stream populate the Margin and Isolated
// fields on cached positions, and that a missing isolated wallet field
// preserves whatever bootstrap had already recorded.
func TestHandleAccountUpdate_MarginAndIsolated(t *testing.T) {
	t.Run("isolated wallet from WS overrides bootstrap value", func(t *testing.T) {
		b := NewFuturesBroker(nil, "mainnet")
		// Simulate a prior bootstrap for BTCUSDT with a 20.41 USDT margin.
		sym, err := domain.NormalizeExchangeSymbol("BTCUSDT", domain.ContractFuturesPerp)
		if err != nil {
			t.Fatalf("normalize symbol: %v", err)
		}
		b.positions[sym] = domain.Position{
			Symbol:       sym,
			Venue:        domain.VenueBinanceFutures,
			Side:         domain.SideBuy,
			Quantity:     decimal.RequireFromString("0.033"),
			EntryPrice:   decimal.RequireFromString("77325.80"),
			CurrentPrice: decimal.RequireFromString("77325.80"),
			Leverage:     125,
			Margin:       decimal.RequireFromString("20.41"),
			Isolated:     true,
		}

		// User tops up isolated wallet to 50 USDT — Binance pushes ACCOUNT_UPDATE.
		msg := []byte(`{"e":"ACCOUNT_UPDATE","a":{"P":[{"s":"BTCUSDT","pa":"0.033","ep":"77325.80","mp":"77474.50","iw":"50","mt":"isolated"}]}}`)
		if err := b.handleAccountUpdate(msg); err != nil {
			t.Fatalf("handleAccountUpdate: %v", err)
		}

		got := b.positions[sym]
		if !got.Isolated {
			t.Error("Isolated should remain true after isolated update")
		}
		if !got.Margin.Equal(decimal.RequireFromString("50")) {
			t.Errorf("Margin = %s, want 50", got.Margin.String())
		}
		// Leverage was not part of the WS payload — must be preserved.
		if got.Leverage != 125 {
			t.Errorf("Leverage = %d, want 125 preserved from bootstrap", got.Leverage)
		}
		if !got.CurrentPrice.Equal(decimal.RequireFromString("77474.50")) {
			t.Errorf("CurrentPrice should reflect markPrice, got %s", got.CurrentPrice.String())
		}
	})

	t.Run("switching to cross resets isolated margin", func(t *testing.T) {
		b := NewFuturesBroker(nil, "mainnet")
		sym, _ := domain.NormalizeExchangeSymbol("BTCUSDT", domain.ContractFuturesPerp)
		b.positions[sym] = domain.Position{
			Symbol:     sym,
			Venue:      domain.VenueBinanceFutures,
			Side:       domain.SideBuy,
			Quantity:   decimal.RequireFromString("0.033"),
			EntryPrice: decimal.RequireFromString("77325.80"),
			Leverage:   125,
			Margin:     decimal.RequireFromString("50"),
			Isolated:   true,
		}

		msg := []byte(`{"e":"ACCOUNT_UPDATE","a":{"P":[{"s":"BTCUSDT","pa":"0.033","ep":"77325.80","mp":"77325.80","mt":"cross"}]}}`)
		if err := b.handleAccountUpdate(msg); err != nil {
			t.Fatalf("handleAccountUpdate: %v", err)
		}

		got := b.positions[sym]
		if got.Isolated {
			t.Error("Isolated should be false after switching to cross")
		}
		if !got.Margin.IsZero() {
			t.Errorf("Margin should be zeroed after switching to cross, got %s", got.Margin.String())
		}
	})

	t.Run("no margin fields preserves prior values", func(t *testing.T) {
		b := NewFuturesBroker(nil, "mainnet")
		sym, _ := domain.NormalizeExchangeSymbol("BTCUSDT", domain.ContractFuturesPerp)
		b.positions[sym] = domain.Position{
			Symbol:     sym,
			Venue:      domain.VenueBinanceFutures,
			Side:       domain.SideBuy,
			Quantity:   decimal.RequireFromString("0.033"),
			EntryPrice: decimal.RequireFromString("77325.80"),
			Leverage:   125,
			Margin:     decimal.RequireFromString("20.41"),
			Isolated:   true,
		}

		// A regular price-only ACCOUNT_UPDATE (no `iw`/`mt`).
		msg := []byte(`{"e":"ACCOUNT_UPDATE","a":{"P":[{"s":"BTCUSDT","pa":"0.033","ep":"77325.80","mp":"77400"}]}}`)
		if err := b.handleAccountUpdate(msg); err != nil {
			t.Fatalf("handleAccountUpdate: %v", err)
		}

		got := b.positions[sym]
		if !got.Isolated {
			t.Error("Isolated should be preserved when WS omits mt")
		}
		if !got.Margin.Equal(decimal.RequireFromString("20.41")) {
			t.Errorf("Margin should be preserved when WS omits iw, got %s", got.Margin.String())
		}
	})
}

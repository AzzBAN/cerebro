package futures

import (
	"testing"

	"github.com/azhar/cerebro/internal/domain"
	"github.com/shopspring/decimal"
)

// TestApplyPositionSnapshot_Resync verifies that merging a fresh REST snapshot
// into the cache (the periodic resync path) (a) drops positions that no longer
// exist on the exchange, (b) adds newly-opened positions, and (c) preserves
// Cerebro-internal metadata (SL/TP/Strategy/CorrelationID/OpenedAt) on
// survivors while taking live qty/price from the snapshot. This is the
// recovery path for when the user-data WS misses a close or open event.
func TestApplyPositionSnapshot_Resync(t *testing.T) {
	b := NewFuturesBroker(nil, "mainnet")
	btc, _ := domain.NormalizeExchangeSymbol("BTCUSDT", domain.ContractFuturesPerp)
	eth, _ := domain.NormalizeExchangeSymbol("ETHUSDT", domain.ContractFuturesPerp)
	doge, _ := domain.NormalizeExchangeSymbol("DOGEUSDT", domain.ContractFuturesPerp)

	// Cache: BTC (with internal metadata) and ETH (will be closed on exchange).
	b.positions[btc] = domain.Position{
		Symbol:        btc,
		Venue:         domain.VenueBinanceFutures,
		Side:          domain.SideBuy,
		Quantity:      decimal.RequireFromString("0.033"),
		EntryPrice:    decimal.RequireFromString("77325.80"),
		StopLoss:      decimal.RequireFromString("76000"),
		TakeProfit1:   decimal.RequireFromString("80000"),
		Strategy:      domain.StrategyName("rsi_bb"),
		CorrelationID: "corr-123",
	}
	b.positions[eth] = domain.Position{
		Symbol:   eth,
		Venue:    domain.VenueBinanceFutures,
		Side:     domain.SideBuy,
		Quantity: decimal.RequireFromString("1.5"),
	}

	// Fresh REST snapshot: BTC still open (new qty/price, no metadata), DOGE
	// newly opened, ETH absent (closed).
	snapshot := map[domain.Symbol]domain.Position{
		btc: {
			Symbol:     btc,
			Venue:      domain.VenueBinanceFutures,
			Side:       domain.SideBuy,
			Quantity:   decimal.RequireFromString("0.050"),
			EntryPrice: decimal.RequireFromString("77400.00"),
			Leverage:   125,
		},
		doge: {
			Symbol:     doge,
			Venue:      domain.VenueBinanceFutures,
			Side:       domain.SideSell,
			Quantity:   decimal.RequireFromString("1000"),
			EntryPrice: decimal.RequireFromString("0.16"),
		},
	}

	b.applyPositionSnapshot(snapshot)

	// ETH must be dropped (closed on the exchange).
	if _, ok := b.positions[eth]; ok {
		t.Error("ETH position should be removed after it disappeared from the snapshot")
	}
	// DOGE must be added (newly opened on the exchange).
	if _, ok := b.positions[doge]; !ok {
		t.Error("DOGE position should be added from the snapshot")
	}
	// BTC must survive with live qty/price from snapshot but preserved metadata.
	got, ok := b.positions[btc]
	if !ok {
		t.Fatal("BTC position should still exist")
	}
	if !got.Quantity.Equal(decimal.RequireFromString("0.050")) {
		t.Errorf("BTC Quantity = %s, want 0.050 from snapshot", got.Quantity)
	}
	if !got.StopLoss.Equal(decimal.RequireFromString("76000")) {
		t.Errorf("BTC StopLoss = %s, want 76000 preserved", got.StopLoss)
	}
	if !got.TakeProfit1.Equal(decimal.RequireFromString("80000")) {
		t.Errorf("BTC TakeProfit1 = %s, want 80000 preserved", got.TakeProfit1)
	}
	if got.Strategy != domain.StrategyName("rsi_bb") {
		t.Errorf("BTC Strategy = %q, want rsi_bb preserved", got.Strategy)
	}
	if got.CorrelationID != "corr-123" {
		t.Errorf("BTC CorrelationID = %q, want corr-123 preserved", got.CorrelationID)
	}
}

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

// TestDeriveFuturesMarkPrice verifies the mark-price derivation from a
// position's signed notional and signed position amount. The futures account
// endpoint exposes neither markPrice nor a clean current price, so it is
// derived as |notional| / |positionAmt|. Regression guard for the bug where
// UnrealizedProfit (PnL, negative for a loser) was stored into CurrentPrice.
func TestDeriveFuturesMarkPrice(t *testing.T) {
	tests := []struct {
		name     string
		notional string
		amount   string
		want     string // "" means caller's decimal parse degrades to zero
	}{
		{"long position", "7740.0", "0.1", "77400"},
		{"short position signed notional", "-7740.0", "-0.1", "77400"},
		{"short with positive notional", "7740.0", "-0.1", "77400"},
		{"zero amount yields empty", "7740.0", "0", ""},
		{"empty amount yields empty", "7740.0", "", ""},
		{"empty notional yields empty", "", "0.1", ""},
		{"garbage notional yields empty", "abc", "0.1", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := deriveFuturesMarkPrice(tt.notional, tt.amount)
			if tt.want == "" {
				if got != "" {
					t.Errorf("deriveFuturesMarkPrice(%q, %q) = %q, want empty", tt.notional, tt.amount, got)
				}
				return
			}
			if !decimal.RequireFromString(got).Equal(decimal.RequireFromString(tt.want)) {
				t.Errorf("deriveFuturesMarkPrice(%q, %q) = %q, want %q", tt.notional, tt.amount, got, tt.want)
			}
		})
	}
}

// TestFuturesAccountPositionToDomain_CurrentPriceIsMark confirms the mapper
// stores the derived mark price (never PnL) in CurrentPrice. A losing position
// has negative unrealized PnL; CurrentPrice must remain a positive price.
func TestFuturesAccountPositionToDomain_CurrentPriceIsMark(t *testing.T) {
	// Long 0.1 BTC entered at 78000, mark derived from notional 7740 → 77400.
	mark := deriveFuturesMarkPrice("7740.0", "0.1")
	pos, ok, err := futuresAccountPositionToDomain("BTCUSDT", "0.1", "78000", mark)
	if err != nil {
		t.Fatalf("futuresAccountPositionToDomain: %v", err)
	}
	if !ok {
		t.Fatal("expected a live position")
	}
	if pos.Side != domain.SideBuy {
		t.Errorf("Side = %s, want BUY", pos.Side)
	}
	if !pos.CurrentPrice.Equal(decimal.RequireFromString("77400")) {
		t.Errorf("CurrentPrice = %s, want 77400", pos.CurrentPrice.String())
	}
	if pos.CurrentPrice.IsNegative() {
		t.Error("CurrentPrice must never be negative (regression: PnL stored as price)")
	}
}

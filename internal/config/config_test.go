package config

import (
	"testing"

	"github.com/azhar/cerebro/internal/domain"
)

func TestResolveStrategySymbol(t *testing.T) {
	known := map[domain.Symbol]bool{
		"BTC/USDT":      true,
		"BTC/USDT-PERP": true,
		"XAU/USDT-PERP": true,
	}

	if _, err := resolveStrategySymbol("BTCUSDT", known); err == nil {
		t.Fatalf("expected ambiguity error for BTCUSDT when spot and futures both exist")
	}

	got, err := resolveStrategySymbol("BTC/USDT-PERP", known)
	if err != nil {
		t.Fatalf("resolveStrategySymbol returned error: %v", err)
	}
	if got != "BTC/USDT-PERP" {
		t.Fatalf("got %q", got)
	}

	got, err = resolveStrategySymbol("XAUUSDT", known)
	if err != nil {
		t.Fatalf("resolveStrategySymbol returned error: %v", err)
	}
	if got != "XAU/USDT-PERP" {
		t.Fatalf("got %q", got)
	}
}

func TestNormalizeSymbols(t *testing.T) {
	cfg := &Config{
		Markets: []VenueConfig{
			{
				Venue: domain.VenueBinanceSpot,
				Symbols: []SymbolConfig{
					{Symbol: "BTCUSDT", ContractType: domain.ContractSpot, Enabled: true},
				},
			},
			{
				Venue: domain.VenueBinanceFutures,
				Symbols: []SymbolConfig{
					{Symbol: "XAUUSDT", ContractType: domain.ContractFuturesPerp, Enabled: true},
				},
			},
		},
		Strategies: StrategiesConfig{
			Strategies: []StrategyConfig{
				{Name: "trend_following", Enabled: true, Markets: []string{"BTCUSDT", "XAUUSDT"}},
			},
		},
	}

	if err := normalizeSymbols(cfg); err != nil {
		t.Fatalf("normalizeSymbols error: %v", err)
	}

	if got := cfg.Markets[0].Symbols[0].Symbol; got != "BTC/USDT" {
		t.Fatalf("spot symbol got %q", got)
	}
	if got := cfg.Markets[1].Symbols[0].Symbol; got != "XAU/USDT-PERP" {
		t.Fatalf("futures symbol got %q", got)
	}
	if got := cfg.Strategies.Strategies[0].Markets[1]; got != "XAU/USDT-PERP" {
		t.Fatalf("strategy symbol got %q", got)
	}
}

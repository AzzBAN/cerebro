package config

import (
	"os"
	"path/filepath"
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

// TestLoadMarkets_VenueDefaults verifies that fields declared in a venue's
// `defaults:` block are inherited by every symbol unless explicitly overridden.
// This is the ergonomic improvement that lets users add a new watch with just
// `- symbol: BTC/USDT`.
func TestLoadMarkets_VenueDefaults(t *testing.T) {
	yamlData := `
venues:
  - venue: binance_spot
    defaults:
      contract_type: spot
      leverage: 1
      margin_type: isolated
      tick_size: 0.01
      lot_size: 0.0001
      min_notional: 5.0
      max_order_notional: 50000.0
      max_position_size_pct: 5.0
      timeframes: [1m, 5m, 15m, 1h]
      primary_timeframe: 5m
      trend_timeframe: 1h
      max_spread_pct: 0.05
      enabled: true
    symbols:
      - symbol: BTC/USDT
      - symbol: SOL/USDT
        lot_size: 0.01
        max_order_notional: 20000.0
`
	dir := t.TempDir()
	path := filepath.Join(dir, "markets.yaml")
	if err := os.WriteFile(path, []byte(yamlData), 0o644); err != nil {
		t.Fatalf("write tmp: %v", err)
	}

	venues, err := loadMarkets(path)
	if err != nil {
		t.Fatalf("loadMarkets: %v", err)
	}
	if len(venues) != 1 || len(venues[0].Symbols) != 2 {
		t.Fatalf("expected 1 venue with 2 symbols, got %+v", venues)
	}

	// BTC/USDT should inherit all venue defaults.
	btc := venues[0].Symbols[0]
	if btc.ContractType != domain.ContractSpot {
		t.Errorf("BTC contract_type: want spot, got %q", btc.ContractType)
	}
	if btc.Leverage != 1 {
		t.Errorf("BTC leverage: want 1, got %d", btc.Leverage)
	}
	if !btc.Enabled {
		t.Errorf("BTC enabled: want true, got false")
	}
	if btc.PrimaryTimeframe != domain.TF5m {
		t.Errorf("BTC primary_timeframe: want 5m, got %q", btc.PrimaryTimeframe)
	}
	if btc.LotSize.String() != "0.0001" {
		t.Errorf("BTC lot_size: want 0.0001, got %s", btc.LotSize.String())
	}

	// SOL/USDT should override lot_size and max_order_notional but
	// inherit everything else from defaults.
	sol := venues[0].Symbols[1]
	if sol.LotSize.String() != "0.01" {
		t.Errorf("SOL lot_size override: want 0.01, got %s", sol.LotSize.String())
	}
	if sol.MaxOrderNotional.String() != "20000" {
		t.Errorf("SOL max_order_notional override: want 20000, got %s", sol.MaxOrderNotional.String())
	}
	if sol.ContractType != domain.ContractSpot {
		t.Errorf("SOL inherited contract_type: want spot, got %q", sol.ContractType)
	}
	if !sol.Enabled {
		t.Errorf("SOL inherited enabled: want true, got false")
	}
	if len(sol.Timeframes) != 4 {
		t.Errorf("SOL inherited timeframes: want 4, got %d", len(sol.Timeframes))
	}
}

// TestResolveStrategyAssignments_DefaultsAndPerSymbolOverride verifies the
// fallback rules:
//   - Symbols without `strategies:` inherit `default_strategies`.
//   - Symbols with `strategies:` override the defaults completely.
//   - A strategy's own `markets:` list is preserved (additive).
func TestResolveStrategyAssignments_DefaultsAndPerSymbolOverride(t *testing.T) {
	cfg := &Config{
		Markets: []VenueConfig{
			{
				Venue: domain.VenueBinanceSpot,
				Symbols: []SymbolConfig{
					// inherits default_strategies
					{Symbol: "BTC/USDT", ContractType: domain.ContractSpot, Enabled: true},
					{Symbol: "ETH/USDT", ContractType: domain.ContractSpot, Enabled: true},
					// per-symbol override: only mean_reversion
					{
						Symbol:       "SOL/USDT",
						ContractType: domain.ContractSpot,
						Enabled:      true,
						Strategies:   []domain.StrategyName{"mean_reversion"},
					},
					// disabled symbol — must be skipped
					{Symbol: "DOGE/USDT", ContractType: domain.ContractSpot, Enabled: false},
				},
			},
		},
		Strategies: StrategiesConfig{
			DefaultStrategies: []domain.StrategyName{"mean_reversion", "trend_following"},
			Strategies: []StrategyConfig{
				{Name: "mean_reversion", Enabled: true},
				{Name: "trend_following", Enabled: true},
				// volatility_breakout has an explicit pin — kept as-is.
				{
					Name:    "volatility_breakout",
					Enabled: true,
					Markets: []string{"XAU/USDT-PERP"},
				},
			},
		},
	}

	resolveStrategyAssignments(cfg)

	got := make(map[string][]string, len(cfg.Strategies.Strategies))
	for _, s := range cfg.Strategies.Strategies {
		got[string(s.Name)] = s.Markets
	}

	// mean_reversion: BTC + ETH (defaults) + SOL (per-symbol)
	if want := []string{"BTC/USDT", "ETH/USDT", "SOL/USDT"}; !equalStringSlices(got["mean_reversion"], want) {
		t.Errorf("mean_reversion markets: want %v, got %v", want, got["mean_reversion"])
	}

	// trend_following: BTC + ETH only (SOL opted out via override)
	if want := []string{"BTC/USDT", "ETH/USDT"}; !equalStringSlices(got["trend_following"], want) {
		t.Errorf("trend_following markets: want %v, got %v", want, got["trend_following"])
	}

	// volatility_breakout: explicit pin survives (no symbol opted into it
	// via per-symbol or defaults).
	if want := []string{"XAU/USDT-PERP"}; !equalStringSlices(got["volatility_breakout"], want) {
		t.Errorf("volatility_breakout markets: want %v, got %v", want, got["volatility_breakout"])
	}
}

// TestResolveStrategyAssignments_EmptyOptOut verifies that a symbol with
// `strategies: []` opts out of *all* strategies, even default ones.
func TestResolveStrategyAssignments_EmptyOptOut(t *testing.T) {
	cfg := &Config{
		Markets: []VenueConfig{
			{
				Venue: domain.VenueBinanceSpot,
				Symbols: []SymbolConfig{
					{
						Symbol:       "BTC/USDT",
						ContractType: domain.ContractSpot,
						Enabled:      true,
						Strategies:   []domain.StrategyName{}, // explicit opt-out
					},
				},
			},
		},
		Strategies: StrategiesConfig{
			DefaultStrategies: []domain.StrategyName{"mean_reversion"},
			Strategies: []StrategyConfig{
				{Name: "mean_reversion", Enabled: true},
			},
		},
	}

	resolveStrategyAssignments(cfg)

	if got := cfg.Strategies.Strategies[0].Markets; len(got) != 0 {
		t.Errorf("expected empty markets list (opt-out), got %v", got)
	}
}

// TestLoadStrategies_DefaultStrategiesField parses `default_strategies:` from
// the preset-map style strategies.yaml.
func TestLoadStrategies_DefaultStrategiesField(t *testing.T) {
	yamlData := `
default_strategies:
  - mean_reversion
  - trend_following

defaults:
  enabled: false
  risk_pct_per_trade: 0.5

mean_reversion:
  enabled: true
  primary_timeframe: 15m

trend_following:
  enabled: true
  primary_timeframe: 1h
`
	dir := t.TempDir()
	path := filepath.Join(dir, "strategies.yaml")
	if err := os.WriteFile(path, []byte(yamlData), 0o644); err != nil {
		t.Fatalf("write tmp: %v", err)
	}

	sc, err := loadStrategies(path)
	if err != nil {
		t.Fatalf("loadStrategies: %v", err)
	}

	if want := 2; len(sc.DefaultStrategies) != want {
		t.Fatalf("default_strategies count: want %d, got %d (%v)",
			want, len(sc.DefaultStrategies), sc.DefaultStrategies)
	}
	if sc.DefaultStrategies[0] != "mean_reversion" {
		t.Errorf("default_strategies[0]: got %q", sc.DefaultStrategies[0])
	}
	if len(sc.Strategies) != 2 {
		t.Errorf("expected 2 strategy presets, got %d", len(sc.Strategies))
	}
}

func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestPositionManagerConfig_Validate(t *testing.T) {
	base := func() PositionManagerConfig {
		return PositionManagerConfig{
			Enabled:             true,
			ReconcileIntervalMS: 5000,
			ConfirmTimeoutSec:   60,
			AutonomousOnTimeout: true,
			TriggerDebounceSec:  300,
			LLMFailureAction:    "tighten_breakeven",
			ProfitThresholdPct:  1.0,
			NearTPSLPct:         0.2,
		}
	}
	tests := []struct {
		name    string
		mutate  func(*PositionManagerConfig)
		wantErr bool
	}{
		{"valid", func(*PositionManagerConfig) {}, false},
		{"disabled skips checks", func(p *PositionManagerConfig) { p.Enabled = false; p.ReconcileIntervalMS = 0 }, false},
		{"zero interval", func(p *PositionManagerConfig) { p.ReconcileIntervalMS = 0 }, true},
		{"zero timeout", func(p *PositionManagerConfig) { p.ConfirmTimeoutSec = 0 }, true},
		{"bad llm action", func(p *PositionManagerConfig) { p.LLMFailureAction = "panic" }, true},
		{"negative profit pct", func(p *PositionManagerConfig) { p.ProfitThresholdPct = -1 }, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := base()
			tt.mutate(&cfg)
			err := cfg.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() err = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

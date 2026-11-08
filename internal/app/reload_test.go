package app

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	webadapter "github.com/azhar/cerebro/internal/adapter/web"
	"github.com/azhar/cerebro/internal/config"
	"github.com/azhar/cerebro/internal/domain"
)

const reloadAppYAML = `environment: paper
aggressiveness: balanced
risk:
  max_drawdown_pct: 2.0
  max_daily_loss_pct: 3.0
agent:
  screening_interval_minutes: 120
  bias_ttl_minutes: 180
`

const reloadMarketsYAML = `venues:
  - venue: binance_futures
    defaults:
      contract_type: futures_perpetual
      leverage: 5
      margin_type: isolated
      tick_size: 0.10
      lot_size: 0.001
      min_notional: 5.0
      timeframes: [1m, 5m, 15m, 1h]
      primary_timeframe: 15m
      trend_timeframe: 1h
      enabled: true
    symbols:
      - symbol: BTC/USDT-PERP
`

const reloadStrategiesYAML = `default_strategies:
  - mean_reversion

defaults:
  enabled: false

mean_reversion:
  enabled: true
  primary_timeframe: 15m
  trend_timeframe: 1h
  risk_pct_per_trade: 0.5
  max_position_size_pct: 4.0
  signal_dedup_window_seconds: 900
  confirmation_candles: 1
  require_bias_alignment: false
  stop_loss:
    type: fixed_pct
    fixed_pct: 0.5
  take_profit_levels:
    - rr_ratio: 1.5
      scale_out_pct: 100
  warmup_candles: 50
`

// writeReloadFixture lays down a minimal-but-valid 4-file config set in a temp
// dir and returns the dir. paper environment needs no API keys.
func writeReloadFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"secrets.env":     "ENVIRONMENT=paper\n",
		"app.yaml":        reloadAppYAML,
		"markets.yaml":    reloadMarketsYAML,
		"strategies.yaml": reloadStrategiesYAML,
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return dir
}

// newTestReloader builds a reloader over the fixture with a seeded atomic
// pointer, no kline client (warmup is skipped), and no web server.
func newTestReloader(t *testing.T, dir string) (*strategyReloader, *atomic.Pointer[[]strategyCandidate]) {
	t.Helper()
	secrets, appPath, markets, strategies := BuildConfigPaths(dir)
	cfg, err := config.Load(secrets, appPath, markets, strategies)
	if err != nil {
		t.Fatalf("load fixture config: %v", err)
	}
	if err := cfg.Validate(domain.EnvironmentPaper); err != nil {
		t.Fatalf("fixture config invalid: %v", err)
	}

	symbolMeta, err := buildSymbolMetaMap(cfg.Markets)
	if err != nil {
		t.Fatalf("build symbol meta: %v", err)
	}

	reg := registerStrategies(cfg)
	var ptr atomic.Pointer[[]strategyCandidate]
	ptr.Store(&reg)

	r := newStrategyReloader(&ptr, dir, cfg, domain.EnvironmentPaper, nil, symbolMeta, nil)
	return r, &ptr
}

func TestReloader_RawView(t *testing.T) {
	dir := writeReloadFixture(t)
	r, _ := newTestReloader(t, dir)

	raw, err := r.RawView()
	if err != nil {
		t.Fatalf("RawView: %v", err)
	}
	if raw.Aggressiveness != "balanced" {
		t.Errorf("aggressiveness: got %q want balanced", raw.Aggressiveness)
	}
	var mr *webadapter.RawStrategyDTO
	for i := range raw.Strategies {
		if raw.Strategies[i].Name == "mean_reversion" {
			mr = &raw.Strategies[i]
		}
	}
	if mr == nil {
		t.Fatal("mean_reversion missing from raw view")
	}
	// RawView must show pre-scaling values (0.5), not anything scaled.
	if mr.RiskPctPerTrade != 0.5 {
		t.Errorf("raw risk_pct: got %v want 0.5", mr.RiskPctPerTrade)
	}
	if mr.StopLossFixedPct != 0.5 || mr.TP1RRRatio != 1.5 {
		t.Errorf("raw sl/tp: got sl=%v tp=%v want 0.5/1.5", mr.StopLossFixedPct, mr.TP1RRRatio)
	}
}

func TestReloader_ApplyEdits_SwapsPointer(t *testing.T) {
	dir := writeReloadFixture(t)
	r, ptr := newTestReloader(t, dir)
	before := ptr.Load()

	newRisk := 0.25
	_, err := r.ApplyEdits(context.Background(), webadapter.EditRequestDTO{
		Strategies: []webadapter.StrategyEditDTO{
			{Name: "mean_reversion", RiskPctPerTrade: &newRisk},
		},
	})
	if err != nil {
		t.Fatalf("ApplyEdits: %v", err)
	}

	// The atomic pointer must now reference a freshly built registry.
	if ptr.Load() == before {
		t.Error("atomic pointer was not swapped after a valid edit")
	}
	// Disk must hold the new RAW value.
	raw, _ := config.LoadStrategiesRaw(filepath.Join(dir, "strategies.yaml"))
	for _, s := range raw.Strategies {
		if string(s.Name) == "mean_reversion" && s.RiskPctPerTrade != 0.25 {
			t.Errorf("disk risk_pct: got %v want 0.25", s.RiskPctPerTrade)
		}
	}
}

func TestReloader_ApplyEdits_AggressivenessToAppYAML(t *testing.T) {
	dir := writeReloadFixture(t)
	r, _ := newTestReloader(t, dir)

	dto, err := r.ApplyEdits(context.Background(), webadapter.EditRequestDTO{
		Aggressiveness: "aggressive",
	})
	if err != nil {
		t.Fatalf("ApplyEdits: %v", err)
	}
	if dto.Aggressiveness != "aggressive" {
		t.Errorf("returned dto aggressiveness: got %q want aggressive", dto.Aggressiveness)
	}
	// app.yaml on disk must be updated.
	b, _ := os.ReadFile(filepath.Join(dir, "app.yaml"))
	if want := "aggressiveness: aggressive"; !containsStr(string(b), want) {
		t.Errorf("app.yaml not updated:\n%s", b)
	}
	// Effective risk should now be scaled up (0.5 * 1.75 = 0.875).
	for _, s := range dto.Strategies {
		if s.Name == "mean_reversion" && s.RiskPctPerTrade != 0.875 {
			t.Errorf("effective risk after aggressive: got %v want 0.875", s.RiskPctPerTrade)
		}
	}
}

func TestReloader_ApplyEdits_InvalidRollsBack(t *testing.T) {
	dir := writeReloadFixture(t)
	r, ptr := newTestReloader(t, dir)
	before := ptr.Load()
	stratPath := filepath.Join(dir, "strategies.yaml")
	originalFile, _ := os.ReadFile(stratPath)

	// risk_pct_per_trade (5.0) > max_position_size_pct (4.0) → Validate fails.
	badRisk := 5.0
	_, err := r.ApplyEdits(context.Background(), webadapter.EditRequestDTO{
		Strategies: []webadapter.StrategyEditDTO{
			{Name: "mean_reversion", RiskPctPerTrade: &badRisk},
		},
	})
	if err == nil {
		t.Fatal("expected ApplyEdits to reject an invalid edit")
	}

	// The live engine must be untouched: pointer unchanged.
	if ptr.Load() != before {
		t.Error("atomic pointer changed despite a rejected edit")
	}
	// The file must be restored byte-for-byte.
	afterFile, _ := os.ReadFile(stratPath)
	if string(afterFile) != string(originalFile) {
		t.Errorf("strategies.yaml not rolled back:\nwant:\n%s\ngot:\n%s", originalFile, afterFile)
	}
}

func containsStr(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (func() bool {
		for i := 0; i+len(needle) <= len(haystack); i++ {
			if haystack[i:i+len(needle)] == needle {
				return true
			}
		}
		return false
	})()
}

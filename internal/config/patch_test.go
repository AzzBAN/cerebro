package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A minimal preset-map strategies.yaml mirroring the real file's shape:
// a shared defaults block, two presets, nested stop_loss + TP sequence.
const sampleStrategiesYAML = `# top comment
default_strategies:
  - mean_reversion

defaults:
  enabled: false
  risk_pct_per_trade: 0.5
  stop_loss:
    type: fixed_pct
    fixed_pct: 0.5

mean_reversion:
  enabled: true
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

trend_following:
  enabled: true
  risk_pct_per_trade: 1.0
`

func writeTemp(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "strategies.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write temp: %v", err)
	}
	return path
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	return string(b)
}

func TestPatchStrategyField_TopLevel(t *testing.T) {
	path := writeTemp(t, sampleStrategiesYAML)

	if err := PatchStrategyField(path, "mean_reversion", "risk_pct_per_trade", 0.25); err != nil {
		t.Fatalf("patch: %v", err)
	}

	// Re-load and assert the value changed.
	cfg, err := LoadStrategiesRaw(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	var mr *StrategyConfig
	for i := range cfg.Strategies {
		if string(cfg.Strategies[i].Name) == "mean_reversion" {
			mr = &cfg.Strategies[i]
		}
	}
	if mr == nil {
		t.Fatal("mean_reversion missing after patch")
	}
	if mr.RiskPctPerTrade != 0.25 {
		t.Errorf("risk_pct: got %v want 0.25", mr.RiskPctPerTrade)
	}

	// Schema preservation: defaults block, other preset, and comment survive.
	out := readFile(t, path)
	for _, want := range []string{"# top comment", "defaults:", "trend_following:", "take_profit_levels:"} {
		if !strings.Contains(out, want) {
			t.Errorf("patched file lost %q:\n%s", want, out)
		}
	}
	// Did not flatten to list-style.
	if strings.Contains(out, "strategies:\n  - name:") {
		t.Errorf("file was flattened to list-style:\n%s", out)
	}
}

func TestPatchStrategyField_Nested(t *testing.T) {
	path := writeTemp(t, sampleStrategiesYAML)

	if err := PatchStrategyField(path, "mean_reversion", "stop_loss.fixed_pct", 0.3); err != nil {
		t.Fatalf("patch nested: %v", err)
	}
	cfg, _ := LoadStrategiesRaw(path)
	for _, s := range cfg.Strategies {
		if string(s.Name) == "mean_reversion" && s.StopLoss.FixedPct != 0.3 {
			t.Errorf("stop_loss.fixed_pct: got %v want 0.3", s.StopLoss.FixedPct)
		}
	}
}

func TestPatchStrategyField_SequenceIndex(t *testing.T) {
	path := writeTemp(t, sampleStrategiesYAML)

	if err := PatchStrategyField(path, "mean_reversion", "take_profit_levels.0.rr_ratio", 2.0); err != nil {
		t.Fatalf("patch seq: %v", err)
	}
	cfg, _ := LoadStrategiesRaw(path)
	for _, s := range cfg.Strategies {
		if string(s.Name) == "mean_reversion" {
			if len(s.TakeProfitLevels) == 0 || s.TakeProfitLevels[0].RRRatio != 2.0 {
				t.Errorf("tp[0].rr_ratio: got %+v want 2.0", s.TakeProfitLevels)
			}
		}
	}
}

func TestPatchStrategyField_Bool(t *testing.T) {
	path := writeTemp(t, sampleStrategiesYAML)

	if err := PatchStrategyField(path, "mean_reversion", "require_bias_alignment", true); err != nil {
		t.Fatalf("patch bool: %v", err)
	}
	cfg, _ := LoadStrategiesRaw(path)
	for _, s := range cfg.Strategies {
		if string(s.Name) == "mean_reversion" && !s.RequireBiasAlignment {
			t.Error("require_bias_alignment not set to true")
		}
	}
}

func TestPatchStrategyField_Errors(t *testing.T) {
	path := writeTemp(t, sampleStrategiesYAML)

	if err := PatchStrategyField(path, "does_not_exist", "risk_pct_per_trade", 0.1); err == nil {
		t.Error("expected error for unknown preset")
	}
	if err := PatchStrategyField(path, "mean_reversion", "no_such_field", 1); err == nil {
		t.Error("expected error for unknown field")
	}
}

func TestPatchAppField(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.yaml")
	if err := os.WriteFile(path, []byte("environment: demo\naggressiveness: balanced\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := PatchAppField(path, "aggressiveness", "aggressive"); err != nil {
		t.Fatalf("patch app: %v", err)
	}
	out := readFile(t, path)
	if !strings.Contains(out, "aggressiveness: aggressive") {
		t.Errorf("aggressiveness not updated:\n%s", out)
	}
	if !strings.Contains(out, "environment: demo") {
		t.Errorf("environment lost:\n%s", out)
	}
}

// TestPatchNoExplicitTags verifies that patching never emits explicit YAML
// tags (!!float, !!int, !!bool, !!str) in the output. This was a bug where
// yaml.v3 would render "!!float 200" instead of "200" when an explicit tag
// was set on a scalar node.
func TestPatchNoExplicitTags(t *testing.T) {
	path := writeTemp(t, sampleStrategiesYAML)

	// Patch various types
	if err := PatchStrategyField(path, "mean_reversion", "risk_pct_per_trade", 0.25); err != nil {
		t.Fatalf("patch float: %v", err)
	}
	if err := PatchStrategyField(path, "mean_reversion", "signal_dedup_window_seconds", 1200); err != nil {
		t.Fatalf("patch int: %v", err)
	}
	if err := PatchStrategyField(path, "mean_reversion", "require_bias_alignment", true); err != nil {
		t.Fatalf("patch bool: %v", err)
	}

	out := readFile(t, path)
	for _, badTag := range []string{"!!float", "!!int", "!!bool", "!!str"} {
		if strings.Contains(out, badTag) {
			t.Errorf("output contains explicit tag %q:\n%s", badTag, out)
		}
	}
}

// TestPatchSanitizesExistingTags verifies that patching cleans up any
// explicit tags left by a previous buggy write.
func TestPatchSanitizesExistingTags(t *testing.T) {
	// Simulate a file corrupted by the old bug
	corrupted := `mean_reversion:
  risk_pct_per_trade: !!float 0.5
  signal_dedup_window_seconds: !!int 900
  require_bias_alignment: !!bool false
`
	dir := t.TempDir()
	path := filepath.Join(dir, "strategies.yaml")
	if err := os.WriteFile(path, []byte(corrupted), 0o644); err != nil {
		t.Fatal(err)
	}

	// Patch any field — the sanitization pass should clean all explicit tags
	if err := PatchStrategyField(path, "mean_reversion", "risk_pct_per_trade", 0.25); err != nil {
		t.Fatalf("patch: %v", err)
	}

	out := readFile(t, path)
	for _, badTag := range []string{"!!float", "!!int", "!!bool"} {
		if strings.Contains(out, badTag) {
			t.Errorf("sanitization failed — output still contains %q:\n%s", badTag, out)
		}
	}
}

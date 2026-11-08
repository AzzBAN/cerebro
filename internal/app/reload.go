package app

import (
	"context"
	"fmt"
	"os"
	"sync"
	"sync/atomic"

	webadapter "github.com/azhar/cerebro/internal/adapter/web"
	"github.com/azhar/cerebro/internal/config"
	"github.com/azhar/cerebro/internal/domain"
)

// strategyReloader implements webadapter.StrategyEditor. It owns the live,
// hot-swappable strategy slice (via an atomic.Pointer the engine reads each
// candle) and the config file paths, so the web dashboard can edit
// strategies.yaml / app.yaml and have the change take effect without a restart.
//
// Safety: edits are validated by a full config.Load + Validate before they are
// accepted. A fresh strategy set is warmed (blocking REST) BEFORE the atomic
// swap, so the running engine never sees a cold strategy. Any failure restores
// the pre-edit files and leaves the live engine untouched.
type strategyReloader struct {
	ptr        *atomic.Pointer[[]strategyCandidate]
	secrets    string
	appPath    string
	markets    string
	strategies string
	env        domain.Environment
	kc         *klineClients
	symbolMeta map[domain.Symbol]symbolMeta
	web        *webadapter.Server

	mu  sync.Mutex     // serialises concurrent edits + guards cfg
	cfg *config.Config // latest applied config (for aggressiveness readout)
}

func newStrategyReloader(
	ptr *atomic.Pointer[[]strategyCandidate],
	cfgDir string,
	cfg *config.Config,
	env domain.Environment,
	kc *klineClients,
	symbolMeta map[domain.Symbol]symbolMeta,
	web *webadapter.Server,
) *strategyReloader {
	secrets, appPath, markets, strategies := BuildConfigPaths(cfgDir)
	return &strategyReloader{
		ptr:        ptr,
		secrets:    secrets,
		appPath:    appPath,
		markets:    markets,
		strategies: strategies,
		env:        env,
		kc:         kc,
		symbolMeta: symbolMeta,
		web:        web,
		cfg:        cfg,
	}
}

// RawView returns the current pre-scaling editable config for the edit form.
func (r *strategyReloader) RawView() (webadapter.RawStrategiesDTO, error) {
	raw, err := config.LoadStrategiesRaw(r.strategies)
	if err != nil {
		return webadapter.RawStrategiesDTO{}, fmt.Errorf("load raw strategies: %w", err)
	}

	r.mu.Lock()
	aggr := string(r.cfg.Aggressiveness)
	r.mu.Unlock()
	if aggr == "" {
		aggr = string(domain.AggressivenessBalanced)
	}

	out := webadapter.RawStrategiesDTO{Aggressiveness: aggr}
	for _, s := range raw.Strategies {
		out.Strategies = append(out.Strategies, webadapter.RawStrategyDTO{
			Name:                 string(s.Name),
			Enabled:              s.Enabled,
			RiskPctPerTrade:      s.RiskPctPerTrade,
			MaxPositionSizePct:   s.MaxPositionSizePct,
			DedupWindowSeconds:   s.SignalDedupWindowSeconds,
			ConfirmationCandles:  s.ConfirmationCandles,
			RequireBiasAlignment: s.RequireBiasAlignment,
			StopLossFixedPct:     s.StopLoss.FixedPct,
			TP1RRRatio:           firstTPRR(s),
		})
	}
	return out, nil
}

// ApplyEdits validates and persists edits, then hot-reloads the engine. On any
// failure it restores the pre-edit files and returns the error; the live engine
// is left untouched because the atomic swap is the very last step.
func (r *strategyReloader) ApplyEdits(ctx context.Context, req webadapter.EditRequestDTO) (webadapter.ConfigDTO, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Back up both files so a validation failure can be fully rolled back.
	appBak, err := os.ReadFile(r.appPath)
	if err != nil {
		return webadapter.ConfigDTO{}, fmt.Errorf("backup app.yaml: %w", err)
	}
	stratBak, err := os.ReadFile(r.strategies)
	if err != nil {
		return webadapter.ConfigDTO{}, fmt.Errorf("backup strategies.yaml: %w", err)
	}
	restore := func() {
		_ = os.WriteFile(r.appPath, appBak, 0o644)
		_ = os.WriteFile(r.strategies, stratBak, 0o644)
	}

	// Apply patches to disk (raw, pre-scaling values).
	if req.Aggressiveness != "" {
		if err := config.PatchAppField(r.appPath, "aggressiveness", req.Aggressiveness); err != nil {
			restore()
			return webadapter.ConfigDTO{}, fmt.Errorf("patch aggressiveness: %w", err)
		}
	}
	for _, e := range req.Strategies {
		if err := applyStrategyEdit(r.strategies, e); err != nil {
			restore()
			return webadapter.ConfigDTO{}, fmt.Errorf("patch %s: %w", e.Name, err)
		}
	}

	// Re-load + validate the whole config before accepting the edit.
	newCfg, err := config.Load(r.secrets, r.appPath, r.markets, r.strategies)
	if err != nil {
		restore()
		return webadapter.ConfigDTO{}, fmt.Errorf("reload config: %w", err)
	}
	if err := newCfg.Validate(r.env); err != nil {
		restore()
		return webadapter.ConfigDTO{}, fmt.Errorf("invalid config after edit: %w", err)
	}

	// Build + WARM the new strategy set before swapping it in. The old slice
	// keeps serving signals during this (blocking) warmup, so there is no gap.
	newReg := registerStrategies(newCfg)
	warmupStrategies(ctx, newReg, r.kc, newCfg.Markets, r.symbolMeta)

	// Atomic swap: the engine picks up newReg on its next candle event.
	r.ptr.Store(&newReg)
	r.cfg = newCfg

	dto := webadapter.ConfigToDTO(newCfg)
	if r.web != nil {
		r.web.SetConfig(dto)
	}
	return dto, nil
}

// applyStrategyEdit patches every non-nil field of one strategy edit onto
// strategies.yaml. Field → YAML key mapping mirrors config.StrategyConfig tags.
func applyStrategyEdit(path string, e webadapter.StrategyEditDTO) error {
	if e.Name == "" {
		return fmt.Errorf("strategy edit missing name")
	}
	set := func(key string, v any) error {
		return config.PatchStrategyField(path, e.Name, key, v)
	}
	if e.Enabled != nil {
		if err := set("enabled", *e.Enabled); err != nil {
			return err
		}
	}
	if e.RiskPctPerTrade != nil {
		if err := set("risk_pct_per_trade", *e.RiskPctPerTrade); err != nil {
			return err
		}
	}
	if e.MaxPositionSizePct != nil {
		if err := set("max_position_size_pct", *e.MaxPositionSizePct); err != nil {
			return err
		}
	}
	if e.DedupWindowSeconds != nil {
		if err := set("signal_dedup_window_seconds", *e.DedupWindowSeconds); err != nil {
			return err
		}
	}
	if e.ConfirmationCandles != nil {
		if err := set("confirmation_candles", *e.ConfirmationCandles); err != nil {
			return err
		}
	}
	if e.RequireBiasAlignment != nil {
		if err := set("require_bias_alignment", *e.RequireBiasAlignment); err != nil {
			return err
		}
	}
	if e.StopLossFixedPct != nil {
		if err := set("stop_loss.fixed_pct", *e.StopLossFixedPct); err != nil {
			return err
		}
	}
	if e.TP1RRRatio != nil {
		if err := set("take_profit_levels.0.rr_ratio", *e.TP1RRRatio); err != nil {
			return err
		}
	}
	return nil
}

// firstTPRR returns the first take-profit R:R ratio, or 0 when none configured.
func firstTPRR(s config.StrategyConfig) float64 {
	if len(s.TakeProfitLevels) == 0 {
		return 0
	}
	return s.TakeProfitLevels[0].RRRatio
}

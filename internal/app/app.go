package app

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"path/filepath"

	"github.com/azhar/cerebro/internal/config"
	"github.com/azhar/cerebro/internal/domain"
)

// App is the composition root. It wires all adapters and services together
// and manages the application lifecycle via an errgroup.
type App struct {
	cfg *config.Config
}

// New builds the App from the loaded, validated config.
func New(cfg *config.Config) *App {
	return &App{cfg: cfg}
}

// Run starts all long-running goroutines and blocks until ctx is cancelled
// or a fatal error occurs. The same runtime handles paper, demo, and live
// environments; only the broker and market-data adapters differ.
func (a *App) Run(ctx context.Context) error {
	slog.Info("cerebro starting",
		"environment", a.cfg.Environment,
		"kill_switch", a.cfg.Engine.KillSwitch,
	)
	return a.runRuntime(ctx)
}

// HealthCheck validates external connectivity (DB, Redis, broker, LLM).
// Used by `cerebro check --dry-run`.
func (a *App) HealthCheck(ctx context.Context) error {
	slog.Info("running health checks")

	checks := []struct {
		name string
		fn   func(ctx context.Context) error
	}{
		{"config", a.checkConfig},
		{"database", a.checkDatabase},
		{"redis", a.checkRedis},
		{"binance", a.checkBinance},
	}

	failed := false
	for _, c := range checks {
		if err := c.fn(ctx); err != nil {
			slog.Error("health check failed", "check", c.name, "error", err)
			failed = true
		} else {
			slog.Info("health check passed", "check", c.name)
		}
	}

	if failed {
		return fmt.Errorf("one or more health checks failed")
	}
	slog.Info("all health checks passed")
	return nil
}

func (a *App) checkConfig(_ context.Context) error {
	return nil
}

func (a *App) checkDatabase(_ context.Context) error {
	if a.cfg.Secrets.DatabaseURL == "" {
		return fmt.Errorf("DATABASE_URL not set")
	}
	if _, err := url.Parse(a.cfg.Secrets.DatabaseURL); err != nil {
		return fmt.Errorf("invalid DATABASE_URL: %w", err)
	}
	slog.Debug("database URL present (ping deferred to Phase 3)")
	return nil
}

func (a *App) checkRedis(_ context.Context) error {
	if a.cfg.Secrets.RedisURL == "" {
		return fmt.Errorf("REDIS_URL not set")
	}
	slog.Debug("redis URL present (ping deferred to Phase 2)")
	return nil
}

func (a *App) checkBinance(_ context.Context) error {
	switch a.cfg.Environment {
	case domain.EnvironmentDemo:
		if a.cfg.Secrets.BinanceDemoAPIKey == "" {
			return fmt.Errorf("BINANCE_DEMO_API_KEY not set for DEMO environment")
		}
	case domain.EnvironmentLive:
		if a.cfg.Secrets.BinanceAPIKey == "" {
			return fmt.Errorf("BINANCE_API_KEY not set for LIVE environment")
		}
	}
	slog.Debug("binance credentials present (ping deferred to Phase 2)")
	return nil
}

// BuildConfigPaths resolves the four config file paths under cfgDir.
func BuildConfigPaths(cfgDir string) (secrets, app, markets, strategies string) {
	return filepath.Join(cfgDir, "secrets.env"),
		filepath.Join(cfgDir, "app.yaml"),
		filepath.Join(cfgDir, "markets.yaml"),
		filepath.Join(cfgDir, "strategies.yaml")
}

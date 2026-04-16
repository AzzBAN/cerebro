package cli

import (
	"fmt"
	"log/slog"

	"github.com/azhar/cerebro/internal/app"
	"github.com/azhar/cerebro/internal/config"
	"github.com/azhar/cerebro/internal/domain"
	"github.com/azhar/cerebro/internal/observability"
	"github.com/spf13/cobra"
)

func newRunCommand() *cobra.Command {
	var (
		paper bool
		demo  bool
		live  bool
	)

	cmd := &cobra.Command{
		Use:   "run",
		Short: "Start the trading engine",
		Long: `Launches the Cerebro trading engine.

--paper  Fully offline. Synthetic random-walk prices, no API keys required.
--demo   Binance Demo Trading (demo.binance.com). Real live prices via mainnet
         WebSocket, virtual execution — zero financial risk. Requires
         BINANCE_DEMO_API_KEY / BINANCE_DEMO_API_SECRET in secrets.env.
--live   Real-money trading on Binance mainnet. Requires BINANCE_API_KEY.

The CLI flag, ENVIRONMENT in secrets.env, and environment in app.yaml must
all agree — any mismatch causes an immediate fatal error at startup.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			selected := 0
			if paper {
				selected++
			}
			if demo {
				selected++
			}
			if live {
				selected++
			}
			if selected != 1 {
				return fmt.Errorf("exactly one of --paper, --demo or --live must be specified")
			}

			env := domain.EnvironmentPaper
			switch {
			case demo:
				env = domain.EnvironmentDemo
			case live:
				env = domain.EnvironmentLive
			}

			secrets, appPath, markets, strategies := app.BuildConfigPaths(cfgDir)
			cfg, err := config.Load(secrets, appPath, markets, strategies)
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			observability.Setup(cfg.Log.Level, cfg.Log.Format)

			if err := cfg.Validate(env); err != nil {
				return err
			}

			if cfg.Engine.KillSwitch {
				slog.Warn("kill_switch is enabled in config; no orders will be placed")
			}

			ctx, cancel := app.WaitForShutdown()
			defer cancel()

			return app.New(cfg).Run(ctx)
		},
	}

	cmd.Flags().BoolVar(&paper, "paper", false, "offline paper trading with synthetic market data")
	cmd.Flags().BoolVar(&demo, "demo", false, "demo trading: real Binance prices, virtual execution (demo.binance.com)")
	cmd.Flags().BoolVar(&live, "live", false, "live trading with real funds on Binance mainnet")

	return cmd
}

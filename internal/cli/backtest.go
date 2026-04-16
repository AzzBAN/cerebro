package cli

import (
	"fmt"
	"time"

	"github.com/azhar/cerebro/internal/app"
	"github.com/azhar/cerebro/internal/config"
	"github.com/azhar/cerebro/internal/domain"
	"github.com/azhar/cerebro/internal/observability"
	"github.com/spf13/cobra"
)

func newBacktestCommand() *cobra.Command {
	var (
		strategy string
		dataFile string
		from     string
		to       string
		output   string
	)

	cmd := &cobra.Command{
		Use:   "backtest",
		Short: "Run a strategy against historical CSV data",
		Long: `Loads historical candle data from a CSV file and drives it through the
strategy engine and paper execution pipeline using a simulated clock.
LLM agents are mocked using fixture files for deterministic results.

Example:
  cerebro backtest --strategy=trend_following --data=testdata/fixtures/btc_1m.csv \
    --from=2024-01-01 --to=2024-12-31 --output=report.json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if strategy == "" {
				return fmt.Errorf("--strategy is required")
			}
			if dataFile == "" {
				return fmt.Errorf("--data is required")
			}

			fromTime, err := time.Parse("2006-01-02", from)
			if err != nil {
				return fmt.Errorf("invalid --from date: %w", err)
			}
			toTime, err := time.Parse("2006-01-02", to)
			if err != nil {
				return fmt.Errorf("invalid --to date: %w", err)
			}
			if !toTime.After(fromTime) {
				return fmt.Errorf("--to must be after --from")
			}

			secrets, appPath, markets, strategies := app.BuildConfigPaths(cfgDir)
			cfg, err := config.Load(secrets, appPath, markets, strategies)
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			observability.Setup(cfg.Log.Level, cfg.Log.Format)
			// Backtests always run in paper mode.
			if err := cfg.Validate(domain.EnvironmentPaper); err != nil {
				return err
			}

			// Phase 8 will wire the real backtest engine here.
			fmt.Printf("backtest: strategy=%s data=%s from=%s to=%s output=%s\n",
				strategy, dataFile, fromTime.Format("2006-01-02"), toTime.Format("2006-01-02"), output)
			fmt.Println("backtest engine not yet implemented (Phase 8)")
			return nil
		},
	}

	cmd.Flags().StringVar(&strategy, "strategy", "", "strategy name to backtest")
	cmd.Flags().StringVar(&dataFile, "data", "", "path to OHLCV CSV file")
	cmd.Flags().StringVar(&from, "from", "", "start date (YYYY-MM-DD)")
	cmd.Flags().StringVar(&to, "to", "", "end date (YYYY-MM-DD)")
	cmd.Flags().StringVar(&output, "output", "", "optional path to write JSON report")

	return cmd
}

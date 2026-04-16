package cli

import (
	"context"
	"fmt"

	"github.com/azhar/cerebro/internal/app"
	"github.com/azhar/cerebro/internal/config"
	"github.com/azhar/cerebro/internal/domain"
	"github.com/azhar/cerebro/internal/observability"
	"github.com/spf13/cobra"
)

func newCheckCommand() *cobra.Command {
	var dryRun bool

	cmd := &cobra.Command{
		Use:   "check",
		Short: "Validate configuration and connectivity",
		Long: `Validates config syntax, cross-file rules, and external connectivity
(database, Redis, Binance, LLM endpoints). Exits 0 on success, 1 on failure.
No trading actions are performed.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if !dryRun {
				return fmt.Errorf("--dry-run flag is required for safety")
			}

			secrets, appPath, markets, strategies := app.BuildConfigPaths(cfgDir)
			cfg, err := config.Load(secrets, appPath, markets, strategies)
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			observability.Setup(cfg.Log.Level, cfg.Log.Format)

			// For check, we read ENVIRONMENT from config without requiring CLI flag agreement.
			// The triple-agreement check only applies to `run`.
			if err := cfg.Validate(domain.Environment("")); err != nil {
				return err
			}

			return app.New(cfg).HealthCheck(context.Background())
		},
	}

	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "required safety flag; performs checks without trading")

	return cmd
}

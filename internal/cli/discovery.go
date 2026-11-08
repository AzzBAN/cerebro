package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"text/tabwriter"

	"github.com/azhar/cerebro/internal/adapter/redis"
	"github.com/azhar/cerebro/internal/app"
	"github.com/azhar/cerebro/internal/config"
	"github.com/spf13/cobra"
)

func newDiscoveryCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "discovery",
		Short: "Manage discovery candidates",
		Long:  `Commands for manually triggering discovery and viewing candidates.`,
	}

	cmd.AddCommand(newDiscoveryRunCommand())
	return cmd
}

func newDiscoveryRunCommand() *cobra.Command {
	var jsonOutput bool

	cmd := &cobra.Command{
		Use:   "run",
		Short: "Manually trigger one discovery cycle",
		Long: `Manually trigger one discovery cycle and output the candidates.
Candidates are scored based on volume, volatility, and other factors configured
in app.yaml. Results are cached in Redis under 'discovery:candidates'.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			secrets, appPath, markets, strategies := app.BuildConfigPaths(cfgDir)
			cfg, err := config.Load(secrets, appPath, markets, strategies)
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			if !cfg.Agent.Discovery.Enabled {
				return fmt.Errorf("discovery is disabled in config (agent.discovery.enabled = false)")
			}

			// Build cache
			cache, err := redis.New(cfg.Secrets.RedisURL)
			if err != nil {
				return fmt.Errorf("create redis cache: %w", err)
			}
			defer cache.Close()

			// Build universe feeds
			feeds := app.BuildUniverseFeeds(cfg.Agent.Discovery)
			if len(feeds) == 0 {
				return fmt.Errorf("no universe feeds configured for discovery")
			}

			// Build discovery service
			discovery, err := app.NewDiscovery(cfg.Agent.Discovery, cfg.BiasTTL(), cache, feeds)
			if err != nil {
				return fmt.Errorf("create discovery service: %w", err)
			}

			// Run one cycle
			ctx := context.Background()
			candidates, err := discovery.Candidates(ctx)
			if err != nil {
				return fmt.Errorf("discovery cycle failed: %w", err)
			}

			if len(candidates) == 0 {
				fmt.Println("No candidates found matching discovery filters")
				return nil
			}

			// Output results
			if jsonOutput {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				if err := enc.Encode(candidates); err != nil {
					return fmt.Errorf("encode JSON: %w", err)
				}
			} else {
				fmt.Printf("Found %d discovery candidates:\n\n", len(candidates))
				w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
				fmt.Fprintln(w, "SYMBOL\tVENUE\tSCORE\tPRICE\t24H CHG\tVOLUME (USD)")
				for _, c := range candidates {
					fmt.Fprintf(w, "%s\t%s\t%.2f\t%s\t%.2f%%\t%s\n",
						c.Symbol,
						c.Venue,
						c.Score,
						c.LastPrice.StringFixed(4),
						c.PriceChangePct24,
						c.QuoteVolume24h.StringFixed(0),
					)
				}
				w.Flush()
			}

			slog.Info("discovery cycle complete", "candidates", len(candidates))
			return nil
		},
	}

	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output results as JSON")
	return cmd
}

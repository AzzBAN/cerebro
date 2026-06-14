package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"time"

	"github.com/azhar/cerebro/internal/adapter/redis"
	"github.com/azhar/cerebro/internal/agent"
	"github.com/azhar/cerebro/internal/app"
	"github.com/azhar/cerebro/internal/config"
	"github.com/azhar/cerebro/internal/observability"
	"github.com/spf13/cobra"
)

// newLLMCommand returns the `cerebro llm` command group. It's a thin
// operator toolbelt for the LLM budget counters — not a trading surface.
func newLLMCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "llm",
		Short: "Inspect and manage LLM budget counters",
		Long: `Operator toolbelt for the multi-agent LLM layer.

Subcommands let you inspect and manage the per-provider daily budget
counters in Redis. The circuit-breaker that trips on daily token / cost
budget exhaustion reads these counters, so "llm budget reset" effectively
re-opens the breaker for the remainder of the UTC day.`,
	}
	cmd.AddCommand(newLLMBudgetCommand())
	return cmd
}

func newLLMBudgetCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "budget",
		Short: "Daily LLM token / cost budget commands",
		Long: `Manage the per-provider, per-day token and USD cost counters that
back the LLM cost tracker (see internal/agent/cost.go).

The counters live in Redis with a 48h TTL. Use the subcommands below to
inspect or clear them; this never changes config or engine state.`,
	}
	cmd.AddCommand(
		newLLMBudgetShowCommand(),
		newLLMBudgetResetCommand(),
	)
	return cmd
}

func newLLMBudgetShowCommand() *cobra.Command {
	var (
		provider string
		asJSON   bool
	)

	cmd := &cobra.Command{
		Use:   "show",
		Short: "Show today's LLM token / cost usage per provider",
		Long: `Prints a point-in-time snapshot of the per-provider daily token and
USD counters maintained by the CostTracker.

Use --provider to restrict the output to a single provider (e.g.
gemini, anthropic, openai); omit it to list every provider with any
counters for today. Configured daily_token_budget and
daily_cost_budget_usd from app.yaml (agent.llm.*) are included for
reference; a value of 0 means "unlimited" in config.

Use --json for a machine-readable snapshot suitable for piping into jq.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			secrets, appPath, markets, strategies := app.BuildConfigPaths(cfgDir)
			cfg, err := config.Load(secrets, appPath, markets, strategies)
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}
			observability.Setup(cfg.Log)

			if cfg.Secrets.RedisURL == "" {
				return fmt.Errorf("REDIS_URL not set in secrets.env; nothing to show")
			}
			cache, err := redis.New(cfg.Secrets.RedisURL)
			if err != nil {
				return fmt.Errorf("redis: %w", err)
			}

			tracker := agent.NewCostTracker(
				cache, nil,
				cfg.Agent.LLM.DailyTokenBudget,
				cfg.Agent.LLM.DailyCostBudgetUSD,
				cfg.Agent.LLM.AlertAtBudgetPct/100.0,
			)

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			snap := tracker.Snapshot(ctx)

			// Filter to a single provider if requested.
			if provider != "" {
				pu, ok := snap.PerProvider[provider]
				snap.PerProvider = map[string]agent.ProviderUsage{}
				if ok {
					snap.PerProvider[provider] = pu
					snap.TokensUsed = pu.Tokens
					snap.CostUSD = pu.CostUSD
				} else {
					snap.TokensUsed = 0
					snap.CostUSD = 0
				}
			}

			if asJSON {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(snap)
			}
			printBudgetSnapshot(cmd.OutOrStdout(), snap)
			return nil
		},
	}

	cmd.Flags().StringVar(&provider, "provider", "", "restrict output to a single provider (e.g. anthropic, gemini, openai)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit the snapshot as JSON instead of a table")

	return cmd
}

// printBudgetSnapshot renders a simple fixed-width table of per-provider
// token / cost usage, with totals and configured budgets summarised at
// the top. "—" indicates the budget is disabled (0 = unlimited).
func printBudgetSnapshot(w io.Writer, snap agent.BudgetSnapshot) {
	fmt.Fprintf(w, "LLM budget snapshot  (UTC date: %s, taken at %s)\n",
		snap.Date, snap.At.UTC().Format(time.RFC3339))

	tokenBudget := "—"
	if snap.TokenBudget > 0 {
		tokenBudget = strconv.Itoa(snap.TokenBudget)
	}
	costBudget := "—"
	if snap.CostBudgetUSD > 0 {
		costBudget = fmt.Sprintf("$%.2f", snap.CostBudgetUSD)
	}
	fmt.Fprintf(w, "  totals:  tokens=%d / %s   cost=$%.4f / %s\n\n",
		snap.TokensUsed, tokenBudget, snap.CostUSD, costBudget)

	if len(snap.PerProvider) == 0 {
		fmt.Fprintln(w, "  (no per-provider counters found for this date)")
		return
	}

	providers := make([]string, 0, len(snap.PerProvider))
	for p := range snap.PerProvider {
		providers = append(providers, p)
	}
	sort.Strings(providers)

	fmt.Fprintf(w, "  %-16s  %12s  %12s\n", "PROVIDER", "TOKENS", "COST (USD)")
	fmt.Fprintf(w, "  %-16s  %12s  %12s\n", "--------", "------", "----------")
	for _, p := range providers {
		pu := snap.PerProvider[p]
		fmt.Fprintf(w, "  %-16s  %12d  %12.4f\n", p, pu.Tokens, pu.CostUSD)
	}
}

func newLLMBudgetResetCommand() *cobra.Command {
	var (
		provider     string
		date         string
		resetTokens  bool
		resetCost    bool
		resetAll     bool
	)

	cmd := &cobra.Command{
		Use:   "reset",
		Short: "Clear today's LLM token / cost counters in Redis",
		Long: `Deletes the per-provider daily counters written by the CostTracker.

By default today's UTC date is used; pass --date to target a different
day still within the 48h TTL. At least one of --tokens, --cost or --all
must be specified.

Use --provider to target a single provider (e.g. gemini, anthropic,
openai); omit it to reset every provider.

This is a destructive Redis operation but has no effect on live engine
state — the next LLM call will simply start counting from zero again.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if resetAll {
				resetTokens = true
				resetCost = true
			}
			if !resetTokens && !resetCost {
				return fmt.Errorf("must specify at least one of --tokens, --cost, or --all")
			}
			if date == "" {
				date = time.Now().UTC().Format("2006-01-02")
			}

			secrets, appPath, markets, strategies := app.BuildConfigPaths(cfgDir)
			cfg, err := config.Load(secrets, appPath, markets, strategies)
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}
			observability.Setup(cfg.Log)

			if cfg.Secrets.RedisURL == "" {
				return fmt.Errorf("REDIS_URL not set in secrets.env; nothing to reset")
			}
			cache, err := redis.New(cfg.Secrets.RedisURL)
			if err != nil {
				return fmt.Errorf("redis: %w", err)
			}

			tracker := agent.NewCostTracker(cache, nil, 0, 0, 0)

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			deleted, err := tracker.Reset(ctx, provider, date, resetTokens, resetCost)
			if err != nil {
				return err
			}

			scope := "all providers"
			if provider != "" {
				scope = provider
			}
			what := describeReset(resetTokens, resetCost)
			fmt.Printf("reset %s for %s on %s: %d keys deleted\n", what, scope, date, deleted)
			return nil
		},
	}

	cmd.Flags().StringVar(&provider, "provider", "", "target a specific provider (e.g. anthropic, gemini, openai); default = all")
	cmd.Flags().StringVar(&date, "date", "", "UTC date in YYYY-MM-DD (default: today)")
	cmd.Flags().BoolVar(&resetTokens, "tokens", false, "reset the daily token counter")
	cmd.Flags().BoolVar(&resetCost, "cost", false, "reset the daily cost counter")
	cmd.Flags().BoolVar(&resetAll, "all", false, "reset both token and cost counters (shorthand for --tokens --cost)")

	return cmd
}

// describeReset renders a short "tokens", "cost", or "tokens + cost" label
// for the reset summary line.
func describeReset(tokens, cost bool) string {
	switch {
	case tokens && cost:
		return "tokens + cost"
	case tokens:
		return "tokens"
	case cost:
		return "cost"
	default:
		return "nothing"
	}
}

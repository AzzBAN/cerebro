package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var (
	cfgDir string
)

// Root returns the root cobra command for the cerebro CLI.
func Root() *cobra.Command {
	root := &cobra.Command{
		Use:   "cerebro",
		Short: "Cerebro — AI-powered automated trading system",
		Long: `Cerebro is a high-performance, Binance-native automated trading bot
powered by Go with a multi-agent LLM architecture for decision-making and
risk management.

Common commands:
  cerebro run --paper            start the engine with synthetic market data
  cerebro run --demo             start the engine against Binance Demo
  cerebro run --live             start the engine against Binance mainnet
  cerebro check --dry-run        validate config and credential presence
  cerebro backtest ...           replay a strategy over historical CSV data
  cerebro llm budget reset ...   clear the daily LLM budget counters

Safety invariants:
  * Exactly one of --paper, --demo or --live must be passed to "run", and
    the flag must agree with "environment" in app.yaml or startup fails.
  * "engine.kill_switch: true" in app.yaml halts all order submission.

Use "cerebro <command> --help" for details on any command.`,
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	root.PersistentFlags().StringVar(&cfgDir, "config-dir", "configs", "directory containing config files")

	root.AddCommand(
		newRunCommand(),
		newCheckCommand(),
		newBacktestCommand(),
		newLLMCommand(),
	)

	return root
}

// Execute runs the root command. Call from main.go only.
func Execute() {
	if err := Root().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

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
powered by Go with a multi-agent LLM architecture for decision-making and risk management.`,
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	root.PersistentFlags().StringVar(&cfgDir, "config-dir", "configs", "directory containing config files")

	root.AddCommand(
		newRunCommand(),
		newCheckCommand(),
		newBacktestCommand(),
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

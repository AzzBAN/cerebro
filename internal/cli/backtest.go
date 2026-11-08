package cli

import (
	"context"
	"fmt"
	"strings"
	"time"
	"github.com/azhar/cerebro/internal/adapter/llm"
	"github.com/azhar/cerebro/internal/app"
	"github.com/azhar/cerebro/internal/backtest"
	"github.com/azhar/cerebro/internal/config"
	"github.com/azhar/cerebro/internal/domain"
	"github.com/azhar/cerebro/internal/execution/paper"
	"github.com/azhar/cerebro/internal/marketdata"
	"github.com/azhar/cerebro/internal/observability"
	"github.com/azhar/cerebro/internal/port"
	"github.com/azhar/cerebro/internal/risk"
	agenttools "github.com/azhar/cerebro/internal/agent/tools"
	stratpkg "github.com/azhar/cerebro/internal/strategy"
	"github.com/shopspring/decimal"
	"github.com/spf13/cobra"
)

func newBacktestCommand() *cobra.Command {
	var (
		strategy  string
		dataFile  string
		symbol    string
		timeframe string
		from      string
		to        string
		output           string
		qty              float64
		agentMode        string
		limit            int
		maxPositionsFlag int
	)

	cmd := &cobra.Command{
		Use:   "backtest",
		Short: "Run a strategy against historical CSV data",
		Long: `Run a strategy against historical OHLCV data in CSV form.

The command drives historical candles through the strategy engine and
paper execution pipeline under a simulated clock, recording trades and
computing performance metrics (win rate, Sharpe ratio, drawdown, profit factor).

Example:
  cerebro backtest --strategy=trend_following --data=testdata/fixtures/btc_1m.csv \
    --from=2024-01-01 --to=2024-12-31 --output=report.json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if strategy == "" {
				return fmt.Errorf("--strategy is required")
			}
		var fromTime, toTime time.Time
		if from != "" {
			var err error
			fromTime, err = time.Parse("2006-01-02", from)
			if err != nil {
				return fmt.Errorf("invalid --from date: %w", err)
			}
		}
		if to != "" {
			var err error
			toTime, err = time.Parse("2006-01-02", to)
			if err != nil {
				return fmt.Errorf("invalid --to date: %w", err)
			}
		}
		if dataFile == "" {
			if fromTime.IsZero() {
				fromTime = time.Now().UTC().AddDate(0, -1, 0)
			}
			if toTime.IsZero() {
				toTime = time.Now().UTC()
			}
		}
		if !fromTime.IsZero() && !toTime.IsZero() && !toTime.After(fromTime) {
			return fmt.Errorf("--to must be after --from")
		}

			secrets, appPath, markets, strategies := app.BuildConfigPaths(cfgDir)
			cfg, err := config.Load(secrets, appPath, markets, strategies)
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			observability.Setup(cfg.Log)
			// Backtests never run in live mode.
			if cfg.Environment == domain.EnvironmentLive {
				return fmt.Errorf("backtests cannot run in live environment")
			}
			if err := cfg.Validate(cfg.Environment); err != nil {
				return err
			}

			symStr := strings.TrimSpace(symbol)
			if symStr == "" {
				symStr = "BTC/USDT"
			}
			sym := domain.Symbol(symStr)

			tfStr := strings.ToLower(strings.TrimSpace(timeframe))
			if tfStr == "" {
				tfStr = "1m"
			}
			tf := domain.Timeframe(tfStr)

		var candles []domain.Candle
		if dataFile != "" {
			candles, err = backtest.LoadCSV(dataFile, sym, tf)
			if err != nil {
				return fmt.Errorf("load candles: %w", err)
			}
		} else {
			fmt.Printf("Fetching historical candles from Binance (%s %s %s -> %s)...\n",
				sym, tf, fromTime.Format("2006-01-02"), toTime.Format("2006-01-02"))
			candles, err = backtest.FetchBinanceKlines(cmd.Context(), sym, tf, fromTime, toTime)
			if err != nil {
				return fmt.Errorf("fetch binance candles: %w", err)
			}
		}
		if len(candles) == 0 {
			return fmt.Errorf("no candles available for backtest")
		}
			var filtered []domain.Candle
			for _, c := range candles {
				if !fromTime.IsZero() && c.OpenTime.Before(fromTime) {
					continue
				}
				if !toTime.IsZero() && c.CloseTime.After(toTime) {
					continue
				}
				filtered = append(filtered, c)
			}
			if len(filtered) == 0 {
				filtered = candles
			}
			if limit > 0 && len(filtered) > limit {
				filtered = filtered[:limit]
			}

			// Instantiate strategy from config.
			var strat port.Strategy
			stratNameLower := strings.ToLower(strategy)
			for _, sc := range cfg.Strategies.Strategies {
				if strings.EqualFold(string(sc.Name), stratNameLower) {
					sc.PrimaryTimeframe = tf
					sc.Markets = []string{string(sym)}
					switch strings.ToLower(string(sc.Name)) {
					case "mean_reversion":
						strat = stratpkg.NewMeanReversion(sc)
					case "trend_following":
						strat = stratpkg.NewTrendFollowing(sc)
					case "volatility_breakout":
						strat = stratpkg.NewVolatilityBreakout(sc)
					case "scalping":
						strat = stratpkg.NewScalping(sc)
					}
					break
				}
			}
			if strat == nil {
				stratCfg := config.StrategyConfig{
					Name:             domain.StrategyName(stratNameLower),
					Enabled:          true,
					Markets:          []string{string(sym)},
					PrimaryTimeframe: tf,
					WarmupCandles:    10,
					Indicators: config.IndicatorConfig{
						EMA:       config.EMAConfig{Fast: 9, Slow: 21, Trend: 50, LongTrend: 200},
						ATR:       config.ATRConfig{Period: 14},
						RSI:       config.RSIConfig{Period: 14, Oversold: 30, Overbought: 70},
						Bollinger: config.BollingerConfig{Period: 20, StdDev: 2.0},
					},
				}
				switch stratNameLower {
				case "mean_reversion":
					strat = stratpkg.NewMeanReversion(stratCfg)
				case "trend_following":
					strat = stratpkg.NewTrendFollowing(stratCfg)
				case "volatility_breakout":
					strat = stratpkg.NewVolatilityBreakout(stratCfg)
				case "scalping":
					strat = stratpkg.NewScalping(stratCfg)
				default:
					return fmt.Errorf("strategy %q not found in config or not supported", strategy)
				}
			}

			store := backtest.NewMemTradeStore()
			book := paper.NewBook()
			matcher := paper.NewMatcher(book, store, 0.0004)
			clock := backtest.NewSimClock(filtered[0].OpenTime)
			hub := marketdata.NewHub()
			sim := backtest.NewSimulator(filtered, hub, []port.Strategy{strat}, matcher, clock)
			sim.SetTradeStore(store)
	orderQty := decimal.NewFromFloat(qty)
	if orderQty.IsZero() || orderQty.IsNegative() {
		orderQty = decimal.NewFromFloat(7.5)
	}
			sim.SetDefaultQuantity(orderQty)

			maxPos := cfg.Risk.MaxOpenPositions
			if maxPos <= 0 {
				maxPos = 5
			}
			if maxPositionsFlag > 0 {
				maxPos = maxPositionsFlag
			}
			maxPosSym := cfg.Risk.MaxOpenPositionsPerSymbol
			if maxPosSym <= 0 {
				maxPosSym = maxPos
			}
			if maxPositionsFlag > 0 {
				maxPosSym = maxPositionsFlag
			}
			riskCfg := config.RiskConfig{
				MaxDrawdownPct:            cfg.Risk.MaxDrawdownPct,
				MaxDailyLossPct:           cfg.Risk.MaxDailyLossPct,
				MaxOpenPositions:          maxPos,
				MaxOpenPositionsPerSymbol: maxPosSym,
			}
			gate := risk.NewGate(riskCfg, backtest.NewMemCache(), risk.NewCalendarBlackout())
			gate.SetStartingEquity(decimal.NewFromInt(10_000))
			matcher.SetPnLReporter(gate)
			sim.SetRiskGate(gate)

			dedupWin := 1 * time.Minute
			if tf == domain.TF1h || tf == domain.TF4h || tf == domain.TF1d {
				dedupWin = 1 * time.Hour
			}
			sim.SetDedup(stratpkg.NewDedupWindow(dedupWin))
			sim.SetDedupWindow(dedupWin)
			tracker := backtest.NewDecisionTracker()
			switch strings.ToLower(strings.TrimSpace(agentMode)) {
			case "live":
				providers := app.BuildLLMProviders(cfg)
				if len(providers) == 0 {
					return fmt.Errorf("no LLM providers configured with valid API keys for live agent evaluation")
				}
				chain := llm.NewFallbackChain(providers, cfg.Agent.LLM.FallbackOn)
				riskAgent := backtest.BuildLiveRiskAgent(chain, matcher, gate, clock, filtered, sym, strategy, orderQty, cfg.Agent)
				sim.SetRiskAgent(riskAgent)
				sim.SetDecisionTracker(tracker)
				fmt.Printf("🤖 Live AI Risk Agent enabled using %s (%s)\n", providers[0].Provider(), providers[0].ModelID())
			case "smart":
				routeFn := func(ctx context.Context, oReq agenttools.AgentOrderRequest) error {
					intent := domain.OrderIntent{
						ID:          fmt.Sprintf("intent-%s-%d", oReq.Side, time.Now().UnixNano()),
						Symbol:      oReq.Symbol,
						Side:        oReq.Side,
						Quantity:    orderQty,
						Strategy:    domain.StrategyName(strategy),
						Environment: domain.EnvironmentPaper,
						CreatedAt:   clock.Now(),
					}
					if _, err := matcher.PlaceOrder(ctx, intent); err != nil {
						return err
					}
					sl := oReq.StopLoss
					tp := oReq.TakeProfit1
					if sl.IsZero() {
						currPrice := filtered[0].Close
						qSource := backtest.NewSimQuoteSource(clock, filtered)
						if q, ok := qSource.LatestQuote(oReq.Symbol); ok && !q.Last.IsZero() {
							currPrice = q.Last
						}
						if intent.Side == domain.SideBuy {
							sl = currPrice.Mul(decimal.NewFromFloat(0.985))
							tp = currPrice.Mul(decimal.NewFromFloat(1.005))
						} else {
							sl = currPrice.Mul(decimal.NewFromFloat(1.015))
							tp = currPrice.Mul(decimal.NewFromFloat(0.995))
						}
					}
					_, _ = matcher.PlaceBracket(ctx, domain.BracketRequest{
						ParentIntentID: intent.ID,
						CorrelationID:  intent.CorrelationID,
						Symbol:         intent.Symbol,
						Venue:          intent.Venue,
						Side:           intent.Side,
						Quantity:       intent.Quantity,
						StopLoss:       sl,
						TakeProfit:     tp,
					})
					return nil
				}
				riskAgent := backtest.BuildFreeRiskAgent(routeFn, "smart")
				riskAgent.SetConfiguredSymbols([]domain.Symbol{sym})
				riskAgent.SetQuoteSource(backtest.NewSimQuoteSource(clock, filtered))
				if gate != nil {
					riskAgent.SetCache(gate.Cache())
				}
				sim.SetRiskAgent(riskAgent)
				sim.SetDecisionTracker(tracker)
			}

			sim.SetBracketResolver(func(c domain.Candle, sig domain.Signal) (decimal.Decimal, decimal.Decimal) {
				slPct := decimal.NewFromFloat(0.02)
				tpPct := decimal.NewFromFloat(0.005)
				switch stratNameLower {
				case "mean_reversion":
					slPct = decimal.NewFromFloat(0.025)
					tpPct = decimal.NewFromFloat(0.003)
					if tf == domain.TF1h || tf == domain.TF4h {
						slPct = decimal.NewFromFloat(0.03)
						tpPct = decimal.NewFromFloat(0.008)
					}
				case "scalping":
					slPct = decimal.NewFromFloat(0.015)
					tpPct = decimal.NewFromFloat(0.004)
				case "trend_following":
					slPct = decimal.NewFromFloat(0.01)
					tpPct = decimal.NewFromFloat(0.02)
				case "volatility_breakout":
					slPct = decimal.NewFromFloat(0.015)
					tpPct = decimal.NewFromFloat(0.025)
				}
				if sig.Side == domain.SideBuy {
					return c.Close.Mul(decimal.NewFromInt(1).Sub(slPct)), c.Close.Mul(decimal.NewFromInt(1).Add(tpPct))
				}
				return c.Close.Mul(decimal.NewFromInt(1).Add(slPct)), c.Close.Mul(decimal.NewFromInt(1).Sub(tpPct))
			})
			report, err := backtest.RunWithReporter(cmd.Context(), sim, strategy, string(sym), fromTime, toTime)
			if err != nil {
				return fmt.Errorf("backtest failed: %w", err)
			}
			report.Print()
			if tracker != nil && len(tracker.Records()) > 0 {
				fmt.Printf("─── AGENT DECISION AUDIT (%d decisions) ───\n", len(tracker.Records()))
				for _, d := range tracker.Records() {
					fmt.Printf("[%s] %s %s @ %.2f -> %s (%s) | %s\n",
						d.Timestamp.Format("2006-01-02 15:04"), d.Strategy, d.SignalSide, d.Price,
						strings.ToUpper(d.Action), d.Verdict, d.Rationale)
				}
				fmt.Println()
			}
			if output != "" {
				if err := report.Save(output); err != nil {
					return fmt.Errorf("save report: %w", err)
				}
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&strategy, "strategy", "", "strategy name to backtest")
	cmd.Flags().StringVar(&dataFile, "data", "", "path to OHLCV CSV file (optional: auto-downloads from Binance if omitted)")
	cmd.Flags().StringVar(&symbol, "symbol", "BTC/USDT", "market symbol (e.g. BTC/USDT)")
	cmd.Flags().StringVar(&timeframe, "timeframe", "1m", "candle timeframe (e.g. 1m, 5m, 1h)")
	cmd.Flags().StringVar(&from, "from", "", "start date (YYYY-MM-DD)")
	cmd.Flags().StringVar(&to, "to", "", "end date (YYYY-MM-DD)")
	cmd.Flags().StringVar(&output, "output", "", "optional path to write JSON report")
	cmd.Flags().Float64Var(&qty, "qty", 0, "order quantity in base asset (default 7.5)")
	cmd.Flags().StringVar(&agentMode, "agent", "none", "risk agent decision mode: live (real LLM), smart (heuristic mock), none (technical rules only)")
	cmd.Flags().IntVar(&limit, "limit", 0, "max number of candles to replay (0 = all)")
	cmd.Flags().IntVar(&maxPositionsFlag, "max-positions", 0, "max open positions limit (default from app.yaml or 5)")

	return cmd
}

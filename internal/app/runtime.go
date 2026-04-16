package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"os"
	"path"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	binanceadapter "github.com/azhar/cerebro/internal/adapter/binance"
	"github.com/azhar/cerebro/internal/adapter/binance/futures"
	"github.com/azhar/cerebro/internal/adapter/binance/spot"
	"github.com/azhar/cerebro/internal/adapter/llm"
	agentpkg "github.com/azhar/cerebro/internal/agent"
	agenttools "github.com/azhar/cerebro/internal/agent/tools"
	"github.com/azhar/cerebro/internal/chatops"
	"github.com/azhar/cerebro/internal/config"
	"github.com/azhar/cerebro/internal/domain"
	"github.com/azhar/cerebro/internal/execution"
	"github.com/azhar/cerebro/internal/execution/paper"
	"github.com/azhar/cerebro/internal/ingest/calendar"
	"github.com/azhar/cerebro/internal/ingest/coinglass"
	"github.com/azhar/cerebro/internal/ingest/news"
	"github.com/azhar/cerebro/internal/ingest/scrape"
	"github.com/azhar/cerebro/internal/marketdata"
	"github.com/azhar/cerebro/internal/observability"
	"github.com/azhar/cerebro/internal/port"
	"github.com/azhar/cerebro/internal/risk"
	"github.com/azhar/cerebro/internal/strategy"
	"github.com/azhar/cerebro/internal/tui"
	"github.com/azhar/cerebro/internal/watchdog"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"golang.org/x/sync/errgroup"
)

// paperStartingEquity is the virtual account balance for paper trading (USDT).
// Phase 4: replace with a live account balance query.
const paperStartingEquity = 10_000.0

// runRuntime is the single engine entry point for all three environments.
//
//	Paper: in-memory paper broker  + synthetic random-walk market data.
//	Demo:  Binance Demo REST broker + mainnet WebSocket (real prices, virtual funds).
//	Live:  Binance Live REST broker + mainnet WebSocket (real prices, real funds).
//
// The strategy engine, risk gate, agent subsystem, and TUI are identical in
// all environments; only the broker and data-source adapters differ.
func (a *App) runRuntime(ctx context.Context) error {
	env := a.cfg.Environment

	// ── Startup banner ────────────────────────────────────────────────────────
	enabledStrategies := countEnabledStrategies(a.cfg)
	enabledSymbols := countEnabledSymbols(a.cfg.Markets)
	slog.Info("▶ cerebro engine initialising",
		"env", env,
		"kill_switch", a.cfg.Engine.KillSwitch,
		"strategies_enabled", enabledStrategies,
		"symbols_enabled", enabledSymbols,
		"eval_interval_ms", a.cfg.Engine.EvaluationIntervalMS,
	)

	// ── Core in-memory infrastructure ────────────────────────────────────────
	hub := marketdata.NewHub()
	defer hub.Close()

	cache := newMemoryCache()
	trades := newMemoryTradeStore()
	audit := &memoryAuditStore{}
	agentLog := &memoryAgentLogStore{}

	// ── Broker: paper (in-memory) or live (Binance) ───────────────────────────
	// matcher is non-nil only in paper mode; it drives synthetic fill simulation
	// via runFillMonitor. In live mode the broker handles fills itself.
	var matcher *paper.Matcher
	var broker port.Broker

	if env == domain.EnvironmentPaper {
		book := paper.NewBook()
		matcher = paper.NewMatcher(book, trades, a.cfg.Backtest.CommissionPct)
		broker = matcher
		slog.Debug("paper broker wired", "commission_pct", a.cfg.Backtest.CommissionPct)
	} else {
		// demo and live both use a real Binance REST broker; only the base URL differs.
		broker = buildLiveBroker(a.cfg, env)
		if err := broker.Connect(ctx); err != nil {
			return fmt.Errorf("%s broker connect: %w", env, err)
		}
	}

	// ── Symbol config index ───────────────────────────────────────────────────
	symbolCfgs := buildSymbolConfigMap(a.cfg.Markets)

	// ── Risk gate ─────────────────────────────────────────────────────────────
	cal := risk.NewCalendarBlackout()
	gate := risk.NewGate(a.cfg.Risk, cache, cal)

	if a.cfg.Engine.KillSwitch {
		gate.SetHalt(domain.HaltModePause)
		slog.Warn("kill_switch=true → engine halted; send /resume via ChatOps to trade")
	}

	// ── Watchdog ─────────────────────────────────────────────────────────────
	wd := watchdog.New([]port.Broker{broker}, cache, audit)
	if err := wd.Reconcile(ctx); err != nil {
		return fmt.Errorf("watchdog startup reconcile: %w", err)
	}
	slog.Debug("watchdog startup reconcile complete")

	// ── Execution router + worker ─────────────────────────────────────────────
	router := execution.NewRouter([]domain.Venue{domain.VenueBinanceSpot})
	workerCh, _ := router.Channel(domain.VenueBinanceSpot)
	worker := execution.NewWorker(domain.VenueBinanceSpot, broker, trades, audit, cache, workerCh)

	// ── Strategy registry ─────────────────────────────────────────────────────
	registry := registerStrategies(a.cfg)
	dedup := strategy.NewDedupWindow(time.Minute)

	evalInterval := time.Duration(a.cfg.Engine.EvaluationIntervalMS) * time.Millisecond
	if evalInterval <= 0 {
		evalInterval = 500 * time.Millisecond
	}

	// ── Metrics ───────────────────────────────────────────────────────────────
	var metrics runtimeMetrics

	// ── TUI ───────────────────────────────────────────────────────────────────
	// Starts when max_agent_log_lines > 0 AND an interactive TTY is present.
	// In non-TTY environments (Docker, CI, backgrounded shells) the TUI is
	// silently skipped and all output goes to stderr via slog (pretty handler).
	var tuiRunner *tui.Runner
	if a.cfg.TUI.MaxAgentLogLines > 0 && hasTTY() {
		tuiRunner = tui.NewRunner(hub, a.cfg.TUI.MaxAgentLogLines)
		// Wire the TUI as the secondary slog sink so every log line also
		// appears in the TUI log panel. Must happen after NewRunner since
		// observability.Setup is called before the TUI exists.
		observability.SetLogSink(tuiRunner)
		slog.Debug("TUI runner created", "max_log_lines", a.cfg.TUI.MaxAgentLogLines)
	} else if a.cfg.TUI.MaxAgentLogLines > 0 {
		slog.Info("no interactive terminal detected — TUI disabled; all output goes to stderr")
	}

	// ── Ingest feeds ─────────────────────────────────────────────────────────
	var derivFeed port.DerivativesFeed
	var newsFeed port.NewsFeed
	var calFeed port.CalendarFeed

	if a.cfg.Ingest.CoinGlass.Enabled && a.cfg.Secrets.CoinGlassAPIKey != "" {
		cgClient := coinglass.New(a.cfg.Secrets.CoinGlassAPIKey)
		derivFeed = coinglass.NewFeed(cgClient)
		slog.Info("ingest: CoinGlass derivatives feed enabled")
	} else {
		slog.Debug("ingest: CoinGlass disabled (COINGLASS_API_KEY not set or disabled in config)")
	}

	switch {
	case a.cfg.Ingest.CryptoPanic.Enabled && a.cfg.Secrets.CryptoPanicAPIKey != "":
		newsFeed = news.NewCryptoPanic(a.cfg.Secrets.CryptoPanicAPIKey)
		slog.Info("ingest: CryptoPanic news feed enabled")
	case a.cfg.Ingest.FinancialJuice.Enabled:
		newsFeed = scrape.NewFinancialJuice()
		slog.Info("ingest: FinancialJuice news scraper enabled (no API key required)")
	default:
		slog.Debug("ingest: no news feed configured; fetch_latest_news tool unavailable")
	}

	if a.cfg.Ingest.Myfxbook.Enabled {
		calFeed = calendar.New()
		slog.Info("ingest: Myfxbook calendar feed enabled")
	}

	// ── Notifiers (Telegram/Discord wired in a future phase) ──────────────────
	var notifiers []port.Notifier
	slog.Debug("ChatOps: Telegram/Discord bots deferred to Phase 6")

	// ── Tool registry ─────────────────────────────────────────────────────────
	toolReg := buildToolRegistry(
		a.cfg, cache, trades, agentLog, audit, gate,
		[]port.Broker{broker}, derivFeed, newsFeed, calFeed, notifiers,
	)

	// ── LLM runtime + agents ──────────────────────────────────────────────────
	llmProviders := buildLLMProviders(a.cfg)
	var agentRuntime *agentpkg.Runtime
	var copilotFn func(ctx context.Context, query string) (string, error)

	if len(llmProviders) > 0 {
		llmChain := llm.NewFallbackChain(llmProviders, a.cfg.Agent.LLM.FallbackOn)
		agentRuntime = agentpkg.NewRuntime(llmChain, agentLog, a.cfg.Agent)
		cop := agentpkg.NewCopilot(agentRuntime, toolReg.ForAgent("copilot"))
		copilotFn = cop.Ask
		slog.Info("LLM runtime wired", "providers", len(llmProviders))
	} else {
		slog.Warn("no LLM API keys configured — screening / copilot / reviewer disabled",
			"hint", "set GEMINI_API_KEY, ANTHROPIC_API_KEY, or OPENAI_API_KEY in secrets.env")
	}

	// ── ChatOps dispatcher (CLI-only; no bot transport) ───────────────────────
	confirmTimeout := a.cfg.ChatOps.FlattenConfirmationTimeoutSeconds
	if confirmTimeout <= 0 {
		confirmTimeout = 30
	}
	_ = chatops.New(chatops.Deps{
		RiskGate:    gate,
		Cache:       cache,
		Brokers:     []port.Broker{broker},
		AuditStore:  audit,
		CopilotFn:   copilotFn,
		AllowlistFn: nil,
	}, confirmTimeout)
	slog.Debug("ChatOps dispatcher wired (CLI-only)")

	// ── errgroup ──────────────────────────────────────────────────────────────
	g, gctx := errgroup.WithContext(ctx)

	// Execution worker
	g.Go(func() error {
		slog.Debug("execution worker starting")
		return worker.Run(gctx)
	})

	// Market data: synthetic feeder + fill-monitor (paper) or live Binance WS (live)
	if env == domain.EnvironmentPaper {
		g.Go(func() error {
			return runSyntheticFeeder(gctx, hub, a.cfg.Markets, evalInterval, tuiRunner, &metrics)
		})
		g.Go(func() error {
			return runFillMonitor(gctx, hub, matcher, tuiRunner, &metrics)
		})
	} else {
		spawnLiveKlinesWS(g, gctx, a.cfg, hub)
	}

	// Strategy engine
	g.Go(func() error {
		return runStrategyEngine(gctx, hub, registry, dedup, gate, broker, env, router, symbolCfgs, tuiRunner, &metrics)
	})

	// Heartbeat: prints full metrics summary every 10 s
	g.Go(func() error {
		return runHeartbeat(gctx, gate, broker, tuiRunner, &metrics)
	})

	// LLM agents (conditional)
	if agentRuntime != nil {
		symbols := collectSymbolList(a.cfg.Markets)
		biasTTL := time.Duration(a.cfg.Agent.BiasTTLMinutes) * time.Minute
		if biasTTL <= 0 {
			biasTTL = 4 * time.Hour
		}
		screeningAgent := agentpkg.NewScreeningAgent(
			agentRuntime, cache,
			toolReg.ForAgent("screening"),
			a.cfg.Agent, symbols, biasTTL,
		)
		g.Go(func() error {
			slog.Info("screening agent starting",
				"interval_min", a.cfg.Agent.ScreeningIntervalMinutes,
				"symbols", len(symbols))
			return screeningAgent.Run(gctx)
		})

		if a.cfg.Reviewer.Enabled {
			reviewer := agentpkg.NewReviewerAgent(
				agentRuntime, trades, agentLog, notifiers,
				a.cfg.Reviewer.LookbackDays, a.cfg.Reviewer.MinTradesRequired,
			)
			g.Go(func() error {
				slog.Info("reviewer agent starting", "lookback_days", a.cfg.Reviewer.LookbackDays)
				return reviewer.Run(gctx)
			})
		}
	}

	// Ingest scheduled runners
	startIngestRunners(gctx, g, a.cfg, cache, derivFeed, newsFeed, calFeed)

	// TUI (alt screen — ticker + positions + agent log panel)
	// When the user presses q, the TUI exits cleanly (nil) → we signal engine shutdown.
	// On unexpected errors → log and continue running in log-only mode.
	if tuiRunner != nil {
		g.Go(func() error {
			slog.Info("TUI starting on alt screen (press q or Ctrl-C to quit)")
			if err := tuiRunner.Run(gctx); err != nil {
				slog.Warn("TUI exited with error; engine continues in log-only mode", "error", err)
				<-gctx.Done()
				return nil
			}
			// nil = user pressed q intentionally — request engine shutdown
			slog.Info("TUI closed by operator — shutting down engine")
			return fmt.Errorf("operator quit")
		})
	}

	// Graceful shutdown
	g.Go(func() error {
		<-gctx.Done()
		slog.Info("cerebro shutting down gracefully")
		router.Close()
		return nil
	})

	slog.Info("▶ cerebro engine running",
		"env", env,
		"strategies", len(registry),
		"tui", tuiRunner != nil,
	)

	return g.Wait()
}

// ── Strategy registration ─────────────────────────────────────────────────────

func registerStrategies(cfg *config.Config) []strategyCandidate {
	var out []strategyCandidate
	for _, sc := range cfg.Strategies.Strategies {
		if !sc.Enabled {
			continue
		}
		name := strings.ToLower(string(sc.Name))
		switch name {
		case "mean_reversion":
			out = append(out, strategyCandidate{impl: strategy.NewMeanReversion(sc), cfg: sc})
		case "trend_following":
			out = append(out, strategyCandidate{impl: strategy.NewTrendFollowing(sc), cfg: sc})
		case "volatility_breakout":
			out = append(out, strategyCandidate{impl: strategy.NewVolatilityBreakout(sc), cfg: sc})
		default:
			slog.Warn("strategy preset not executable by runtime; skipping",
				"name", sc.Name,
				"hint", "only mean_reversion / trend_following / volatility_breakout are wired")
		}
	}

	if len(out) == 0 {
		slog.Warn("no executable strategies enabled — bot will observe market data but place no orders",
			"hint", "enable mean_reversion, trend_following, or volatility_breakout in strategies.yaml")
	} else {
		names := make([]string, len(out))
		for i, c := range out {
			names[i] = string(c.cfg.Name)
		}
		slog.Info("strategies wired", "count", len(out), "names", names)
	}
	return out
}

type strategyCandidate struct {
	impl interface {
		Name() domain.StrategyName
		OnCandle(ctx context.Context, c domain.Candle) (domain.Signal, bool)
	}
	cfg config.StrategyConfig
}

// ── Goroutine workers ─────────────────────────────────────────────────────────

// runSyntheticFeeder generates realistic random-walk candles for every
// timeframe listed in each enabled symbol's config. This simulates live market
// data in paper mode without requiring an API connection.
//
// Price model: momentum-biased random walk (85% direction persistence).
// Candles for ALL configured timeframes are emitted at each tick so that
// strategies on 1m, 5m, 15m, and 1h timeframes all receive data.
func runSyntheticFeeder(
	ctx context.Context,
	hub *marketdata.Hub,
	venues []config.VenueConfig,
	interval time.Duration,
	tuiRunner *tui.Runner,
	metrics *runtimeMetrics,
) error {
	feeds := collectAllTimeframeFeeds(venues)
	if len(feeds) == 0 {
		return fmt.Errorf("runSyntheticFeeder: no enabled symbols in markets config")
	}

	slog.Info("synthetic market feeder started",
		"feeds", len(feeds),
		"interval", interval.String())

	type symState struct {
		price     decimal.Decimal
		direction int // +1 (up) or -1 (down)
		volFactor decimal.Decimal
	}

	// Initialise per-symbol state (shared across all timeframes for that symbol).
	stateMap := make(map[domain.Symbol]*symState)
	for _, f := range feeds {
		if _, ok := stateMap[f.symbol]; ok {
			continue
		}
		seedPrice := decimal.NewFromFloat(100 + rand.Float64()*900)
		stateMap[f.symbol] = &symState{
			price:     seedPrice,
			direction: 1,
			volFactor: decimal.NewFromFloat(0.003 + rand.Float64()*0.007),
		}
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case ts := <-ticker.C:
			// Advance each symbol's random walk once per tick.
			for sym, st := range stateMap {
				// 15% probability of direction reversal (creates RSI swings).
				if rand.Float64() < 0.15 {
					st.direction = -st.direction
				}
				pctMove := st.volFactor.Mul(decimal.NewFromFloat(0.5 + rand.Float64()))
				delta := st.price.Mul(pctMove)
				if st.direction < 0 {
					delta = delta.Neg()
				}
				newPrice := st.price.Add(delta)
				if newPrice.LessThan(decimal.NewFromFloat(0.01)) {
					newPrice = decimal.NewFromFloat(0.01)
					st.direction = 1
				}

				open := st.price
				closePx := newPrice
				high := open
				low := closePx
				if closePx.GreaterThan(open) {
					high, low = closePx, open
				}
				spread := st.price.Mul(decimal.NewFromFloat(0.0001))
				high = high.Add(st.price.Mul(decimal.NewFromFloat(rand.Float64() * 0.001)))
				low = low.Sub(st.price.Mul(decimal.NewFromFloat(rand.Float64() * 0.001)))

				hub.PublishQuote(domain.Quote{
					Symbol:    sym,
					Bid:       closePx.Sub(spread),
					Ask:       closePx.Add(spread),
					Mid:       closePx,
					Timestamp: ts.UTC(),
				})

				slog.Debug("quote",
					"symbol", sym,
					"mid", closePx.StringFixed(4),
					"bid", closePx.Sub(spread).StringFixed(4),
					"ask", closePx.Add(spread).StringFixed(4),
				)

				st.price = newPrice
				_ = sym
				stateMap[sym] = st

				// Emit a candle for every configured timeframe.
				for _, f := range feeds {
					if f.symbol != sym {
						continue
					}
					hub.PublishCandle(domain.Candle{
						Symbol:    sym,
						Timeframe: f.timeframe,
						OpenTime:  ts.UTC().Add(-interval),
						CloseTime: ts.UTC(),
						Open:      open,
						High:      high,
						Low:       low,
						Close:     closePx,
						Volume:    decimal.NewFromFloat(10 + rand.Float64()*90),
						Closed:    true,
					})
					metrics.candlesProduced.Add(1)
				}
			}
		}
	}
}

// runFillMonitor drives the paper matcher on every new candle, simulating
// order fills at market price.
func runFillMonitor(
	ctx context.Context,
	hub *marketdata.Hub,
	matcher *paper.Matcher,
	tuiRunner *tui.Runner,
	metrics *runtimeMetrics,
) error {
	_, candles := hub.Subscribe()
	slog.Info("fill monitor started")
	for {
		select {
		case <-ctx.Done():
			return nil
		case evt, ok := <-candles:
			if !ok {
				return nil
			}
			matcher.OnCandle(ctx, evt.Candle)
			metrics.candlesConsumedByFiller.Add(1)
			slog.Debug("fill-monitor candle",
				"symbol", evt.Candle.Symbol,
				"tf", evt.Candle.Timeframe,
				"close", evt.Candle.Close.StringFixed(4),
			)
		}
	}
}

// runStrategyEngine evaluates every strategy against every incoming candle,
// routes approved signals through the risk gate, sizes positions, and dispatches
// order intents to the execution router.
func runStrategyEngine(
	ctx context.Context,
	hub *marketdata.Hub,
	strategies []strategyCandidate,
	dedup *strategy.DedupWindow,
	gate *risk.Gate,
	broker port.Broker,
	env domain.Environment,
	router *execution.Router,
	symbolCfgs map[domain.Symbol]*config.SymbolConfig,
	tuiRunner *tui.Runner,
	metrics *runtimeMetrics,
) error {
	_, candles := hub.Subscribe()
	slog.Info("strategy engine started", "strategies", len(strategies))
	for {
		select {
		case <-ctx.Done():
			return nil
		case evt, ok := <-candles:
			if !ok {
				return nil
			}
			metrics.candlesConsumedByStrategy.Add(1)
			c := evt.Candle

			slog.Debug("strategy engine candle",
				"symbol", c.Symbol, "tf", c.Timeframe,
				"close", c.Close.StringFixed(4),
				"candles_total", metrics.candlesConsumedByStrategy.Load(),
			)

			if len(strategies) == 0 {
				continue
			}

			positions, _ := broker.Positions(ctx)

			for _, s := range strategies {
				sig, fired := s.impl.OnCandle(ctx, c)
				if !fired {
					continue
				}
				metrics.signalsFired.Add(1)

				slog.Info("⚡ SIGNAL fired",
					"strategy", sig.Strategy,
					"symbol", sig.Symbol,
					"side", sig.Side,
					"tf", sig.Timeframe,
					"reason", sig.Reason,
				)
				pushTUI(tuiRunner, fmt.Sprintf("⚡ SIGNAL %s %s %s — %s",
					sig.Side, sig.Symbol, sig.Strategy, sig.Reason))

				if !dedup.Allow(sig) {
					metrics.signalsDeduped.Add(1)
					slog.Debug("signal deduped (same symbol+strategy within window)",
						"strategy", sig.Strategy, "symbol", sig.Symbol)
					continue
				}

				if err := gate.Check(ctx, sig, positions); err != nil {
					metrics.signalsRejectedByRisk.Add(1)
					slog.Warn("✗ SIGNAL rejected by risk gate",
						"strategy", sig.Strategy, "symbol", sig.Symbol,
						"side", sig.Side, "reason", err)
					pushTUI(tuiRunner, fmt.Sprintf("✗ RISK-REJECT %s %s — %v",
						sig.Symbol, sig.Strategy, err))
					continue
				}

				qty := computeQuantity(sig, c.Close, s.cfg, symbolCfgs)

				intent := domain.OrderIntent{
					ID:            uuid.New().String(),
					CorrelationID: sig.CorrelationID,
					Symbol:        sig.Symbol,
					Side:          sig.Side,
					Quantity:      qty,
					Strategy:      sig.Strategy,
					Environment:   env,
					CreatedAt:     time.Now().UTC(),
				}
				resp, err := router.Route(ctx, intent, domain.VenueBinanceSpot)
				if err != nil {
					metrics.orderRouteErrors.Add(1)
					slog.Error("order route failed",
						"symbol", sig.Symbol, "strategy", sig.Strategy, "error", err)
					continue
				}

				metrics.ordersRouted.Add(1)
				slog.Info("✓ ORDER routed",
					"strategy", sig.Strategy,
					"symbol", sig.Symbol,
					"side", sig.Side,
					"qty", qty.String(),
					"broker_id", resp.BrokerOrderID,
				)
				pushTUI(tuiRunner, fmt.Sprintf("✓ ORDER %s %s %.6f @ %.4f [%s]",
					sig.Side, sig.Symbol,
					qty.InexactFloat64(), c.Close.InexactFloat64(),
					sig.Strategy))
			}
		}
	}
}

// runHeartbeat prints a compact runtime summary every 10 seconds.
// When the TUI is running it pushes a one-line summary to the status bar;
// in non-TUI mode it writes a multi-line pretty block to stderr via slog.
func runHeartbeat(
	ctx context.Context,
	gate *risk.Gate,
	broker port.Broker,
	tuiRunner *tui.Runner,
	metrics *runtimeMetrics,
) error {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			positions, _ := broker.Positions(ctx)

			f := observability.HeartbeatFields{
				TradingState:        gate.TradingState(),
				Halted:              gate.IsHalted(),
				OpenPositions:       len(positions),
				CandlesProduced:     metrics.candlesProduced.Load(),
				CandlesStrategy:     metrics.candlesConsumedByStrategy.Load(),
				CandlesFiller:       metrics.candlesConsumedByFiller.Load(),
				SignalsFired:        metrics.signalsFired.Load(),
				SignalsDeduped:      metrics.signalsDeduped.Load(),
				SignalsRiskRejected: metrics.signalsRejectedByRisk.Load(),
				OrdersRouted:        metrics.ordersRouted.Load(),
				OrderErrors:         metrics.orderRouteErrors.Load(),
				Timestamp:           time.Now(),
			}

			if tuiRunner != nil {
				// TUI active: push one-line summary to the status bar.
				summary := fmt.Sprintf(
					"state=%-8s  halted=%-5v  pos=%-3d  candles=%-4d  signals=%-4d  orders=%-4d",
					f.TradingState, f.Halted, f.OpenPositions,
					f.CandlesProduced, f.SignalsFired, f.OrdersRouted,
				)
				tuiRunner.SendHeartbeat(summary)
			} else {
				// No TUI: emit a readable multi-line block to stderr.
				block := observability.FormatHeartbeat(f, nil)
				fmt.Fprint(os.Stderr, block)
			}
		}
	}
}

// ── Position sizing ───────────────────────────────────────────────────────────

func computeQuantity(
	sig domain.Signal,
	entryPrice decimal.Decimal,
	sc config.StrategyConfig,
	symbolCfgs map[domain.Symbol]*config.SymbolConfig,
) decimal.Decimal {
	mkt, ok := symbolCfgs[sig.Symbol]
	if !ok {
		slog.Debug("symbol not in market config; using fallback quantity",
			"symbol", sig.Symbol)
		return decimal.NewFromFloat(0.001)
	}

	sl := deriveStopLoss(sig.Side, entryPrice, sc)
	riskPct := sc.RiskPctPerTrade
	if riskPct <= 0 {
		riskPct = 0.5
	}

	params, err := risk.CalculatePositionSize(
		decimal.NewFromFloat(paperStartingEquity),
		riskPct,
		entryPrice,
		sl,
		mkt.MinLotUnits,
		mkt.MaxLotUnits,
		mkt.MinNotional,
	)
	if err != nil {
		slog.Debug("position sizing failed; falling back to min lot",
			"symbol", sig.Symbol, "error", err)
		if !mkt.MinLotUnits.IsZero() {
			return mkt.MinLotUnits
		}
		return decimal.NewFromFloat(0.001)
	}
	slog.Debug("position sized",
		"symbol", sig.Symbol, "side", sig.Side,
		"entry", entryPrice.StringFixed(4),
		"stop_loss", sl.StringFixed(4),
		"qty", params.Quantity.String(),
		"risk_usd", params.RiskAmountQuote.StringFixed(2),
	)
	return params.Quantity
}

func deriveStopLoss(side domain.Side, entry decimal.Decimal, sc config.StrategyConfig) decimal.Decimal {
	isBuy := side == domain.SideBuy

	switch sc.StopLoss.Type {
	case domain.SLTypeFixedPct:
		pct := sc.StopLoss.FixedPct
		if pct <= 0 {
			pct = 0.5
		}
		factor := decimal.NewFromFloat(pct / 100)
		if isBuy {
			return entry.Mul(decimal.NewFromFloat(1).Sub(factor))
		}
		return entry.Mul(decimal.NewFromFloat(1).Add(factor))

	case domain.SLTypeFixedPips:
		dist := decimal.NewFromFloat(sc.StopLoss.FixedPips)
		if dist.IsZero() {
			dist = decimal.NewFromFloat(10)
		}
		if isBuy {
			return entry.Sub(dist)
		}
		return entry.Add(dist)

	default:
		// ATR-based SL needs live indicator data; fall back to 0.5% in paper mode.
		factor := decimal.NewFromFloat(0.005)
		if isBuy {
			return entry.Mul(decimal.NewFromFloat(1).Sub(factor))
		}
		return entry.Mul(decimal.NewFromFloat(1).Add(factor))
	}
}

// ── Ingest runners ────────────────────────────────────────────────────────────

func startIngestRunners(
	ctx context.Context,
	g *errgroup.Group,
	cfg *config.Config,
	cache port.Cache,
	derivFeed port.DerivativesFeed,
	newsFeed port.NewsFeed,
	calFeed port.CalendarFeed,
) {
	symbols := collectSymbolList(cfg.Markets)

	if cfg.Ingest.CoinGlass.Enabled && derivFeed != nil {
		interval := durOrDefault(cfg.Ingest.CoinGlass.IntervalMinutes, 30) * time.Minute
		timeout := durOrDefault(cfg.Ingest.CoinGlass.TimeoutSeconds, 10) * time.Second
		runner := scrape.NewRunner("coinglass", interval, timeout, func(ctx context.Context) error {
			for _, sym := range symbols {
				snap, err := derivFeed.Snapshot(ctx, sym)
				if err != nil {
					slog.Warn("coinglass: snapshot failed", "symbol", sym, "error", err)
					continue
				}
				b, _ := json.Marshal(snap)
				_ = cache.Set(ctx, fmt.Sprintf("derivatives:%s", sym), b, interval*2)
				slog.Debug("coinglass: snapshot cached", "symbol", sym)
			}
			return nil
		})
		g.Go(func() error { return runner.Run(ctx) })
	}

	if cfg.Ingest.CryptoPanic.Enabled && newsFeed != nil {
		interval := durOrDefault(cfg.Ingest.CryptoPanic.IntervalMinutes, 15) * time.Minute
		timeout := durOrDefault(cfg.Ingest.CryptoPanic.TimeoutSeconds, 10) * time.Second
		runner := scrape.NewRunner("cryptopanic", interval, timeout, func(ctx context.Context) error {
			_, err := newsFeed.FetchLatest(ctx, "BTC", 1)
			if err != nil {
				slog.Warn("cryptopanic: ping failed", "error", err)
			}
			return nil
		})
		g.Go(func() error { return runner.Run(ctx) })
	} else if cfg.Ingest.FinancialJuice.Enabled && newsFeed != nil {
		interval := durOrDefault(cfg.Ingest.FinancialJuice.IntervalMinutes, 10) * time.Minute
		timeout := durOrDefault(cfg.Ingest.FinancialJuice.TimeoutSeconds, 30) * time.Second
		runner := scrape.NewRunner("financialjuice", interval, timeout, func(ctx context.Context) error {
			_, err := newsFeed.FetchLatest(ctx, "", 10)
			if err != nil {
				slog.Debug("financialjuice: fetch attempted (JS rendering may be needed)", "error", err)
			}
			return nil
		})
		g.Go(func() error { return runner.Run(ctx) })
	}

	if cfg.Ingest.Myfxbook.Enabled && calFeed != nil {
		interval := durOrDefault(cfg.Ingest.Myfxbook.IntervalMinutes, 60) * time.Minute
		timeout := durOrDefault(cfg.Ingest.Myfxbook.TimeoutSeconds, 15) * time.Second
		runner := scrape.NewRunner("myfxbook", interval, timeout, func(ctx context.Context) error {
			events, err := calFeed.UpcomingEvents(ctx, 24)
			if err != nil {
				return err
			}
			b, _ := json.Marshal(events)
			_ = cache.Set(ctx, "calendar:upcoming", b, interval*2)
			slog.Debug("myfxbook: calendar cached", "events", len(events))
			return nil
		})
		g.Go(func() error { return runner.Run(ctx) })
	}
}

// ── Builder helpers ───────────────────────────────────────────────────────────

func buildLLMProviders(cfg *config.Config) []port.LLM {
	var providers []port.LLM
	for _, name := range cfg.Agent.LLM.Providers {
		switch strings.ToLower(name) {
		case "anthropic":
			if cfg.Secrets.AnthropicAPIKey == "" {
				slog.Warn("LLM: anthropic configured but ANTHROPIC_API_KEY not set; skipping")
				continue
			}
			mc := cfg.Agent.LLM.Models["anthropic"]
			providers = append(providers, llm.NewAnthropic(
				cfg.Secrets.AnthropicAPIKey, mc.ModelID, mc.Temperature, mc.MaxOutputTokens,
			))
			slog.Info("LLM provider wired", "provider", "anthropic", "model", mc.ModelID)

		case "openai", "openai_compatible":
			if cfg.Secrets.OpenAIAPIKey == "" {
				slog.Warn("LLM: openai configured but OPENAI_API_KEY not set; skipping")
				continue
			}
			mc := cfg.Agent.LLM.Models["openai_compatible"]
			if mc.ModelID == "" {
				mc = cfg.Agent.LLM.Models["openai"]
			}
			providers = append(providers, llm.NewOpenAI(
				cfg.Secrets.OpenAIAPIKey, cfg.Secrets.OpenAIBaseURL,
				mc.ModelID, mc.Temperature, mc.MaxOutputTokens,
			))
			slog.Info("LLM provider wired", "provider", "openai", "model", mc.ModelID)

		case "gemini":
			if cfg.Secrets.GeminiAPIKey == "" {
				slog.Warn("LLM: gemini configured but GEMINI_API_KEY not set; skipping")
				continue
			}
			mc := cfg.Agent.LLM.Models["gemini"]
			providers = append(providers, llm.NewGemini(
				cfg.Secrets.GeminiAPIKey, mc.ModelID, mc.Temperature, mc.MaxOutputTokens,
			))
			slog.Info("LLM provider wired", "provider", "gemini", "model", mc.ModelID)

		default:
			slog.Warn("LLM: unknown provider in config; skipping", "provider", name)
		}
	}
	return providers
}

func buildToolRegistry(
	cfg *config.Config,
	cache port.Cache,
	trades port.TradeStore,
	agentLog port.AgentLogStore,
	audit port.AuditStore,
	gate *risk.Gate,
	brokers []port.Broker,
	derivFeed port.DerivativesFeed,
	newsFeed port.NewsFeed,
	calFeed port.CalendarFeed,
	notifiers []port.Notifier,
) *agenttools.Registry {
	reg := agenttools.NewRegistry(cfg.Agent.ToolPolicy)
	reg.Register("get_active_positions", agenttools.GetActivePositions(brokers))
	reg.Register("get_current_drawdown", agenttools.GetCurrentDrawdown(gate)())
	reg.Register("calculate_position_size", agenttools.CalculatePositionSize())
	reg.Register("force_halt_trading", agenttools.ForceHaltTrading(gate, audit, notifiers))
	reg.Register("reject_signal", agenttools.RejectSignal(audit))
	reg.Register("query_agent_logs", agenttools.QueryAgentLogs(agentLog))
	if derivFeed != nil {
		reg.Register("get_derivatives_data", agenttools.GetDerivativesData(derivFeed))
	}
	if newsFeed != nil {
		reg.Register("fetch_latest_news", agenttools.FetchLatestNews(newsFeed))
	}
	if calFeed != nil {
		reg.Register("get_economic_events", agenttools.GetEconomicEvents(calFeed))
	}
	slog.Debug("tool registry built")
	return reg
}

func buildSymbolConfigMap(venues []config.VenueConfig) map[domain.Symbol]*config.SymbolConfig {
	out := make(map[domain.Symbol]*config.SymbolConfig)
	for i := range venues {
		for j := range venues[i].Symbols {
			sym := &venues[i].Symbols[j]
			out[domain.Symbol(sym.Symbol)] = sym
		}
	}
	return out
}

func collectSymbolList(venues []config.VenueConfig) []domain.Symbol {
	var syms []domain.Symbol
	for _, v := range venues {
		for _, s := range v.Symbols {
			if s.Enabled {
				syms = append(syms, domain.Symbol(s.Symbol))
			}
		}
	}
	return syms
}

// collectAllTimeframeFeeds returns one feed entry per (symbol, timeframe) pair
// across all enabled symbols. This ensures strategies on any configured timeframe
// receive candle data, not just the symbol's primary timeframe.
func collectAllTimeframeFeeds(venues []config.VenueConfig) []symbolFeedConfig {
	out := make([]symbolFeedConfig, 0, 32)
	for _, v := range venues {
		for _, s := range v.Symbols {
			if !s.Enabled {
				continue
			}
			tfs := s.Timeframes
			if len(tfs) == 0 {
				tfs = []domain.Timeframe{domain.TF1m}
			}
			for _, tf := range tfs {
				out = append(out, symbolFeedConfig{
					symbol:    domain.Symbol(s.Symbol),
					timeframe: tf,
				})
			}
		}
	}
	return out
}

// ── Count helpers ─────────────────────────────────────────────────────────────

func countEnabledStrategies(cfg *config.Config) int {
	n := 0
	for _, s := range cfg.Strategies.Strategies {
		if s.Enabled {
			n++
		}
	}
	return n
}

func countEnabledSymbols(venues []config.VenueConfig) int {
	n := 0
	for _, v := range venues {
		for _, s := range v.Symbols {
			if s.Enabled {
				n++
			}
		}
	}
	return n
}

// ── Utility helpers ───────────────────────────────────────────────────────────

// hasTTY returns true when the process is attached to an interactive terminal.
// Bubbletea requires /dev/tty to render; skip TUI in Docker/CI/backgrounded processes.
func hasTTY() bool {
	f, err := os.OpenFile("/dev/tty", os.O_RDONLY, 0)
	if err != nil {
		return false
	}
	f.Close()
	return true
}

func pushTUI(runner *tui.Runner, line string) {
	if runner != nil {
		runner.Push(tui.AgentLogMsg{Line: line})
	}
}

func durOrDefault(minutes, defaultMinutes int) time.Duration {
	if minutes <= 0 {
		return time.Duration(defaultMinutes)
	}
	return time.Duration(minutes)
}

// ── Metrics ───────────────────────────────────────────────────────────────────

type runtimeMetrics struct {
	candlesProduced           atomic.Int64
	candlesConsumedByStrategy atomic.Int64
	candlesConsumedByFiller   atomic.Int64
	signalsFired              atomic.Int64
	signalsDeduped            atomic.Int64
	signalsRejectedByRisk     atomic.Int64
	ordersRouted              atomic.Int64
	orderRouteErrors          atomic.Int64
}

// ── Symbolic feed config ──────────────────────────────────────────────────────

type symbolFeedConfig struct {
	symbol    domain.Symbol
	timeframe domain.Timeframe
}

// ── Live adapter builders ─────────────────────────────────────────────────────

// buildLiveBroker creates the Binance Spot REST broker for demo or live paths
// and sets the appropriate package-level flags so that subsequent WS connections
// (via spawnLiveKlinesWS) resolve to the correct endpoint automatically.
//
//   - demo  → REST: demo-api.binance.com   (virtual matching, real price feed)
//             WS flags: unchanged → mainnet streams (real prices)
//   - live  → REST: api.binance.com        (real trading)
//             WS flags: UseTestnet=true if testnet keys present, else unchanged
func buildLiveBroker(cfg *config.Config, env domain.Environment) port.Broker {
	var client interface {
		// satisfied by both *binance.Client and *futures.Client via spot.NewSpotBroker
	}
	_ = client // unused; kept for documentation

	if env == domain.EnvironmentDemo {
		c := binanceadapter.NewDemoSpotClient(
			cfg.Secrets.BinanceDemoAPIKey,
			cfg.Secrets.BinanceDemoAPISecret,
		)
		broker := spot.NewSpotBroker(c)
		slog.Info("demo spot broker wired",
			"base_url", binanceadapter.DemoSpotBaseURL,
			"venue", broker.Venue(),
		)
		return broker
	}

	// live: prefer testnet keys when present (safe way to smoke-test live wiring)
	isTestnet := cfg.Secrets.BinanceTestnetAPIKey != ""
	apiKey := cfg.Secrets.BinanceAPIKey
	apiSecret := cfg.Secrets.BinanceAPISecret
	if isTestnet {
		apiKey = cfg.Secrets.BinanceTestnetAPIKey
		apiSecret = cfg.Secrets.BinanceTestnetAPISecret
	}
	c := binanceadapter.NewSpotClient(apiKey, apiSecret, isTestnet)
	broker := spot.NewSpotBroker(c)
	slog.Info("live spot broker wired",
		"testnet", isTestnet,
		"venue", broker.Venue(),
	)
	return broker
}

// spawnLiveKlinesWS starts one Binance WebSocket kline stream per
// (venue, timeframe) pair found in markets config, publishing closed candles
// to the hub.
//
// WS endpoint selection is controlled by the package-level flags that
// buildLiveBroker already set before this function runs:
//
//   - live (real keys)    → gobinance.UseTestnet=false  → wss://stream.binance.com:9443/stream   (spot)
//   - live (testnet keys) → gobinance.UseTestnet=true   → wss://stream.testnet.binance.vision     (spot)
//   - demo               → no flag set                 → mainnet stream (real prices) for both venues
//
// Kline streams are public — no API key is required, so no client is created here.
// The actual URL dialled is logged inside KlinesWS.connect() before each connection attempt.
func spawnLiveKlinesWS(
	g *errgroup.Group,
	ctx context.Context,
	cfg *config.Config,
	hub *marketdata.Hub,
) {
	for _, venueCfg := range cfg.Markets {
		tfSymbols := groupSymbolsByTimeframe(venueCfg)
		if len(tfSymbols) == 0 {
			continue
		}

		switch venueCfg.Venue {
		case domain.VenueBinanceSpot:
			for tf, syms := range tfSymbols {
				tf, syms := tf, syms
				ws := spot.NewKlinesWS(hub, syms, tf, nil)
				g.Go(func() error {
					if err := ws.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
						return fmt.Errorf("spot klines WS %s: %w", tf, err)
					}
					return nil
				})
			}

		case domain.VenueBinanceFutures:
			for tf, syms := range tfSymbols {
				tf, syms := tf, syms
				ws := futures.NewKlinesWS(hub, syms, tf, nil)
				g.Go(func() error {
					if err := ws.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
						return fmt.Errorf("futures klines WS %s: %w", tf, err)
					}
					return nil
				})
			}

		default:
			slog.Warn("unknown venue in markets config; skipping live WS", "venue", venueCfg.Venue)
		}
	}
}

// groupSymbolsByTimeframe groups enabled symbols from a single venue config by
// their configured timeframes. Used to create one WS stream per timeframe.
func groupSymbolsByTimeframe(vc config.VenueConfig) map[domain.Timeframe][]domain.Symbol {
	out := make(map[domain.Timeframe][]domain.Symbol)
	for _, s := range vc.Symbols {
		if !s.Enabled {
			continue
		}
		tfs := s.Timeframes
		if len(tfs) == 0 {
			tfs = []domain.Timeframe{domain.TF1m}
		}
		for _, tf := range tfs {
			out[tf] = append(out[tf], domain.Symbol(s.Symbol))
		}
	}
	return out
}

// ── In-memory store implementations ──────────────────────────────────────────

type memoryTradeStore struct {
	mu      sync.Mutex
	intents map[string]domain.OrderIntent
	status  map[string]domain.OrderStatus
	trades  []domain.Trade
}

func newMemoryTradeStore() *memoryTradeStore {
	return &memoryTradeStore{
		intents: make(map[string]domain.OrderIntent),
		status:  make(map[string]domain.OrderStatus),
	}
}

func (m *memoryTradeStore) SaveIntent(_ context.Context, i domain.OrderIntent) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.intents[i.ID] = i
	m.status[i.ID] = domain.OrderStatusPending
	slog.Debug("trade store: intent saved", "id", i.ID, "symbol", i.Symbol, "side", i.Side)
	return nil
}

func (m *memoryTradeStore) UpdateIntentStatus(_ context.Context, id string, status domain.OrderStatus, brokerID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.status[id] = status
	slog.Debug("trade store: status updated", "id", id, "status", status, "broker_id", brokerID)
	return nil
}

func (m *memoryTradeStore) SaveTrade(_ context.Context, t domain.Trade) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.trades = append(m.trades, t)
	slog.Info("trade store: trade recorded",
		"id", t.ID, "symbol", t.Symbol, "side", t.Side,
		"qty", t.Quantity.StringFixed(6), "fill_price", t.FillPrice.StringFixed(4))
	return nil
}

func (m *memoryTradeStore) TradesByWindow(_ context.Context, from, to time.Time) ([]domain.Trade, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]domain.Trade, 0, len(m.trades))
	for _, t := range m.trades {
		if !t.CreatedAt.Before(from) && !t.CreatedAt.After(to) {
			out = append(out, t)
		}
	}
	return out, nil
}

type memoryAuditStore struct{}

func (m *memoryAuditStore) SaveEvent(_ context.Context, e domain.AuditEvent) error {
	slog.Debug("audit", "type", e.EventType, "actor", e.Actor)
	return nil
}

type memoryAgentLogStore struct {
	mu   sync.Mutex
	runs []domain.AgentRun
	msgs []domain.AgentMessage
}

func (m *memoryAgentLogStore) SaveRun(_ context.Context, r domain.AgentRun) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.runs = append(m.runs, r)
	slog.Debug("agent run saved",
		"agent", r.Agent, "model", r.Model, "latency_ms", r.LatencyMS, "outcome", r.Outcome)
	return nil
}

func (m *memoryAgentLogStore) RunsByWindow(_ context.Context, agentName string, from, to time.Time) ([]domain.AgentRun, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []domain.AgentRun
	for _, r := range m.runs {
		if string(r.Agent) == agentName && !r.CreatedAt.Before(from) && !r.CreatedAt.After(to) {
			out = append(out, r)
		}
	}
	return out, nil
}

func (m *memoryAgentLogStore) SaveMessage(_ context.Context, msg domain.AgentMessage) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.msgs = append(m.msgs, msg)
	slog.Debug("agent message saved", "role", msg.Role, "tool", msg.ToolName)
	return nil
}

type memoryCache struct {
	mu   sync.RWMutex
	data map[string]memoryValue
}

type memoryValue struct {
	payload   []byte
	expiresAt time.Time
}

func newMemoryCache() *memoryCache {
	return &memoryCache{data: make(map[string]memoryValue)}
}

func (m *memoryCache) Set(_ context.Context, key string, value []byte, ttl time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	v := memoryValue{payload: append([]byte(nil), value...)}
	if ttl > 0 {
		v.expiresAt = time.Now().Add(ttl)
	}
	m.data[key] = v
	slog.Debug("cache: set", "key", key, "ttl", ttl.String())
	return nil
}

func (m *memoryCache) Get(_ context.Context, key string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.data[key]
	if !ok {
		return nil, nil
	}
	if !v.expiresAt.IsZero() && time.Now().After(v.expiresAt) {
		delete(m.data, key)
		return nil, nil
	}
	return append([]byte(nil), v.payload...), nil
}

func (m *memoryCache) Delete(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.data, key)
	return nil
}

func (m *memoryCache) IncrBy(ctx context.Context, key string, delta int64, ttl time.Duration) (int64, error) {
	curr, err := m.Get(ctx, key)
	if err != nil {
		return 0, err
	}
	var n int64
	if len(curr) > 0 {
		_ = json.Unmarshal(curr, &n)
	}
	n += delta
	b, _ := json.Marshal(n)
	return n, m.Set(ctx, key, b, ttl)
}

func (m *memoryCache) Keys(_ context.Context, patternExpr string) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var keys []string
	for k, v := range m.data {
		if !v.expiresAt.IsZero() && time.Now().After(v.expiresAt) {
			delete(m.data, k)
			continue
		}
		if matched, err := path.Match(patternExpr, k); err == nil && matched {
			keys = append(keys, k)
		}
	}
	return keys, nil
}

func (m *memoryCache) Exists(_ context.Context, key string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.data[key]
	if !ok {
		return false, nil
	}
	if !v.expiresAt.IsZero() && time.Now().After(v.expiresAt) {
		delete(m.data, key)
		return false, nil
	}
	return true, nil
}

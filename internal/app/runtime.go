package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/azhar/cerebro/internal/adapter/llm"
	"github.com/azhar/cerebro/internal/adapter/postgres"
	"github.com/azhar/cerebro/internal/adapter/telegram"
	"github.com/jackc/pgx/v5/pgxpool"
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

	// ── Core infrastructure ──────────────────────────────────────────────────
	hub := marketdata.NewHub()
	defer hub.Close()

	cache := newMemoryCache()

	var (
		trades   port.TradeStore
		audit    port.AuditStore
		agentLog port.AgentLogStore
		pool     *pgxpool.Pool
	)

	if a.cfg.Secrets.DatabaseURL != "" {
		var err error
		pool, err = postgres.NewPool(ctx, a.cfg.Secrets.DatabaseURL)
		if err != nil {
			slog.Warn("database connection failed; falling back to in-memory stores", "error", err)
			trades = newMemoryTradeStore()
			audit = &memoryAuditStore{}
			agentLog = &memoryAgentLogStore{}
		} else {
			defer pool.Close()
			slog.Info("database connected")

			trades = postgres.NewTradeStore(pool)
			audit = postgres.NewAuditStore(pool)
			agentLog = postgres.NewAgentLogStore(pool)
			slog.Info("stores wired: postgres")
		}
	} else {
		trades = newMemoryTradeStore()
		audit = &memoryAuditStore{}
		agentLog = &memoryAgentLogStore{}
		slog.Warn("DATABASE_URL not set; using in-memory stores — data will not persist")
	}

	// ── Symbol config index ───────────────────────────────────────────────────
	symbolMeta, err := buildSymbolMetaMap(a.cfg.Markets)
	if err != nil {
		return fmt.Errorf("build symbol index: %w", err)
	}
	activeVenues := collectActiveVenues(a.cfg.Markets)

	// ── Broker: paper (in-memory) or live (Binance) ───────────────────────────
	var matcher *paper.Matcher
	brokersByVenue := make(map[domain.Venue]port.Broker, len(activeVenues))
	var brokerList []port.Broker
	var kc *klineClients

	if env == domain.EnvironmentPaper {
		book := paper.NewBook()
		matcher = paper.NewMatcher(book, trades, a.cfg.Backtest.CommissionPct)
		for _, venue := range activeVenues {
			brokersByVenue[venue] = matcher
		}
		brokerList = []port.Broker{matcher}
		slog.Debug("paper broker wired", "commission_pct", a.cfg.Backtest.CommissionPct)
	} else {
		brokersByVenue, brokerList, kc, err = buildLiveBrokers(ctx, a.cfg, env, activeVenues)
		if err != nil {
			return err
		}
	}

	// ── Risk gate ─────────────────────────────────────────────────────────────
	cal := risk.NewCalendarBlackout()
	gate := risk.NewGate(a.cfg.Risk, cache, cal)

	if a.cfg.Engine.KillSwitch {
		gate.SetHalt(domain.HaltModePause)
		slog.Warn("kill_switch=true → engine halted; send /resume via ChatOps to trade")
	}

	// ── Watchdog ─────────────────────────────────────────────────────────────
	wd := watchdog.New(brokerList, cache, audit)
	if err := wd.Reconcile(ctx); err != nil {
		return fmt.Errorf("watchdog startup reconcile: %w", err)
	}
	slog.Debug("watchdog startup reconcile complete")

	// ── Execution router + worker ─────────────────────────────────────────────
	router := execution.NewRouter(activeVenues)

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
	var tuiRunner *tui.Runner
	if a.cfg.TUI.MaxAgentLogLines > 0 && hasTTY() {
		tuiRunner = tui.NewRunner(hub, a.cfg.TUI.MaxAgentLogLines)
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
		slog.Info("ingest: CoinGlass derivatives feed enabled (API)")
	} else if a.cfg.Ingest.CoinGlassScraper.Enabled {
		timeout := 30 * time.Second
		if a.cfg.Ingest.CoinGlassScraper.TimeoutSeconds > 0 {
			timeout = time.Duration(a.cfg.Ingest.CoinGlassScraper.TimeoutSeconds) * time.Second
		}
		scraper, err := scrape.NewCoinglassScraper(timeout)
		if err != nil {
			slog.Warn("ingest: CoinGlass scraper failed to start; derivatives data unavailable", "error", err)
		} else {
			derivFeed = scraper
			slog.Info("ingest: CoinGlass web scraper enabled (headless Chromium)")
			defer scraper.Close()
		}
	} else {
		slog.Debug("ingest: CoinGlass disabled (no API key or scraper configured)")
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

	if a.cfg.Ingest.Finnhub.Enabled && a.cfg.Secrets.FinnhubAPIKey != "" {
		calFeed = calendar.New(a.cfg.Secrets.FinnhubAPIKey)
		slog.Info("ingest: Finnhub economic calendar feed enabled")
	}

	// ── Notifiers + Telegram bot ──────────────────────────────────────────────
	var notifiers []port.Notifier
	var tgBot *telegram.Bot
	if a.cfg.Secrets.TelegramBotToken != "" {
		allowlistIDs := telegram.ParseAllowlist(a.cfg.Secrets)
		bot, err := telegram.NewBot(a.cfg.Secrets.TelegramBotToken, allowlistIDs)
		if err != nil {
			slog.Warn("telegram: bot creation failed; notifications unavailable", "error", err)
		} else {
			if a.cfg.Secrets.TelegramChatID != 0 {
				bot.SetChannel(string(port.ChannelDefault), a.cfg.Secrets.TelegramChatID)
				bot.SetChannel(string(port.ChannelTradeExecution), a.cfg.Secrets.TelegramChatID)
				bot.SetChannel(string(port.ChannelAIReasoning), a.cfg.Secrets.TelegramChatID)
				bot.SetChannel(string(port.ChannelSystemAlerts), a.cfg.Secrets.TelegramChatID)
			}
			tgBot = bot
			notifiers = append(notifiers, bot)
			slog.Info("telegram bot wired", "chat_id", a.cfg.Secrets.TelegramChatID, "allowlist", len(allowlistIDs))
		}
	}

	// ── Tool registry ─────────────────────────────────────────────────────────
	toolReg := buildToolRegistry(
		a.cfg, hub, cache, trades, agentLog, audit, gate,
		brokerList, buildRouteOrderFn(router, symbolMeta, env, tuiRunner), derivFeed, newsFeed, calFeed, notifiers,
	)

	// ── LLM runtime + agents ──────────────────────────────────────────────────
	llmProviders := buildLLMProviders(a.cfg)
	var agentRuntime *agentpkg.Runtime
	var copilotFn func(ctx context.Context, query string) (string, error)

	if len(llmProviders) > 0 {
		llmChain := llm.NewFallbackChain(llmProviders, a.cfg.Agent.LLM.FallbackOn)
		agentRuntime = agentpkg.NewRuntime(llmChain, agentLog, a.cfg.Agent)
		cop := agentpkg.NewCopilot(agentRuntime, toolReg.ForAgentWithDefs("copilot"))
		copilotFn = cop.Ask
		slog.Info("LLM runtime wired", "providers", len(llmProviders))

		if tuiRunner != nil {
			provider := llmChain.Provider()
			model := llmChain.ModelID()
			agentRuntime.SetOnStep(func(agent, runID string, step agentpkg.AgentStep, toolName, description string, stepNum, maxSteps int) {
				tuiRunner.SendAgentState(tui.AgentStateMsg{
					Agent:       agent,
					RunID:       runID,
					Step:        tui.AgentStep(step),
					ToolName:    toolName,
					Provider:    provider,
					Model:       model,
					Description: description,
					StepNum:     stepNum,
					MaxSteps:    maxSteps,
					At:          time.Now(),
				})
			})
			tuiRunner.SetCopilotFn(copilotFn)
		}
	} else {
		slog.Warn("no LLM API keys configured — screening / copilot / reviewer disabled",
			"hint", "set GEMINI_API_KEY, ANTHROPIC_API_KEY, or OPENAI_API_KEY in secrets.env")
	}

	// ── ChatOps dispatcher ──────────────────────────────────────────────────
	confirmTimeout := a.cfg.ChatOps.FlattenConfirmationTimeoutSeconds
	if confirmTimeout <= 0 {
		confirmTimeout = 30
	}
	var allowlistFn func(actorID string) bool
	if len(a.cfg.Secrets.TelegramAllowlistUserIDs) > 0 {
		allowed := make(map[string]bool, len(a.cfg.Secrets.TelegramAllowlistUserIDs))
		for _, id := range a.cfg.Secrets.TelegramAllowlistUserIDs {
			allowed["telegram:"+id] = true
		}
		allowlistFn = func(actorID string) bool { return allowed[actorID] }
	}
	chatopsDispatcher := chatops.New(chatops.Deps{
		RiskGate:    gate,
		Cache:       cache,
		Brokers:     brokerList,
		AuditStore:  audit,
		CopilotFn:   copilotFn,
		AllowlistFn: allowlistFn,
	}, confirmTimeout)

	if tgBot != nil {
		tgBot.SetDispatcher(func(ctx context.Context, actorID, message string) string {
			return chatopsDispatcher.Dispatch(ctx, actorID, message)
		})
		slog.Info("ChatOps: Telegram dispatcher wired")
	} else {
		slog.Debug("ChatOps dispatcher wired (no bot transport)")
	}

	// ── errgroup ──────────────────────────────────────────────────────────────
	g, gctx := errgroup.WithContext(ctx)

	for _, venue := range activeVenues {
		venue := venue
		workerCh, ok := router.Channel(venue)
		if !ok {
			return fmt.Errorf("worker channel not registered for venue %s", venue)
		}
		worker := execution.NewWorker(venue, brokersByVenue[venue], trades, audit, cache, workerCh)
		g.Go(func() error {
			slog.Debug("execution worker starting", "venue", venue)
			return worker.Run(gctx)
		})
	}

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
		return runStrategyEngine(gctx, hub, registry, dedup, gate, brokersByVenue, env, router, symbolMeta, tuiRunner, &metrics, kc, a.cfg.Markets, agentRuntime, toolReg, trades)
	})

	// Heartbeat: prints full metrics summary every 10 s
	g.Go(func() error {
		return runHeartbeat(gctx, hub, gate, brokersByVenue, tuiRunner, &metrics)
	})

	for _, venue := range activeVenues {
		venue := venue
		monitor := execution.NewMonitor(router, venue, trades, env, func() []domain.Position {
			return positionsForVenue(gctx, brokersByVenue[venue], venue)
		})
		g.Go(func() error {
			return monitor.Run(gctx, hub)
		})
	}

	// LLM agents (conditional)
	if agentRuntime != nil {
		symbols := collectSymbolList(a.cfg.Markets)
		biasTTL := time.Duration(a.cfg.Agent.BiasTTLMinutes) * time.Minute
		if biasTTL <= 0 {
			biasTTL = 4 * time.Hour
		}
		screeningAgent := agentpkg.NewScreeningAgent(
			agentRuntime, cache,
			toolReg.ForAgentWithDefs("screening"),
			trades,
			a.cfg.Agent, symbols, biasTTL,
			notifiers,
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
				toolReg.ForAgentWithDefs("reviewer"),
			)
			g.Go(func() error {
				slog.Info("reviewer agent starting", "lookback_days", a.cfg.Reviewer.LookbackDays)
				return reviewer.Run(gctx)
			})
		}
	}

	// Telegram bot goroutine
	if tgBot != nil {
		g.Go(func() error {
			slog.Info("telegram bot starting")
			if err := tgBot.Run(gctx); err != nil && !errors.Is(err, context.Canceled) {
				return fmt.Errorf("telegram bot: %w", err)
			}
			return nil
		})
	}

	// Ingest scheduled runners
	startIngestRunners(gctx, g, a.cfg, cache, derivFeed, newsFeed, calFeed)

	// Log archival (conditional — requires Postgres pool, nil when no database)
	if logArchiver := buildLogArchiver(pool, a.cfg); logArchiver != nil {
		g.Go(func() error {
			return runLogArchiver(gctx, logArchiver, a.cfg.LogRetention)
		})
	}

	// TUI (alt screen)
	if tuiRunner != nil {
		g.Go(func() error {
			slog.Info("TUI starting on alt screen (press q or Ctrl-C to quit)")
			if err := tuiRunner.Run(gctx); err != nil {
				slog.Warn("TUI exited with error; engine continues in log-only mode", "error", err)
				<-gctx.Done()
				return nil
			}
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

// runHeartbeat prints a compact runtime summary every 10 seconds.
func runHeartbeat(
	ctx context.Context,
	hub *marketdata.Hub,
	gate *risk.Gate,
	brokers map[domain.Venue]port.Broker,
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
			positions := collectPositions(ctx, brokers)

			// Enrich CurrentPrice from live market quotes so the TUI and
			// downstream consumers see real-time prices instead of the
			// stale bootstrap snapshot.
			for i := range positions {
				if q, ok := hub.LatestQuote(positions[i].Symbol); ok {
					if !q.Last.IsZero() {
						positions[i].CurrentPrice = q.Last
					} else if !q.Mid.IsZero() {
						positions[i].CurrentPrice = q.Mid
					}
				}
			}

			spotCount := countPositionsByVenue(positions, domain.VenueBinanceSpot)
			futuresCount := countPositionsByVenue(positions, domain.VenueBinanceFutures)

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
				tuiRunner.SendPositions(positions)
				summary := fmt.Sprintf(
					"state=%-8s  halted=%-5v  pos=%-3d  spot=%-2d  futures=%-2d  candles=%-4d  signals=%-4d  orders=%-4d",
					f.TradingState, f.Halted, f.OpenPositions,
					spotCount, futuresCount, f.CandlesProduced, f.SignalsFired, f.OrdersRouted,
				)
				tuiRunner.SendHeartbeat(summary)
			} else {
				block := observability.FormatHeartbeat(f, nil)
				fmt.Fprint(os.Stderr, block)
			}
		}
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
				cfg.Secrets.AnthropicAPIKey, cfg.Secrets.AnthropicBaseURL, mc.ModelID, mc.Temperature, mc.MaxOutputTokens,
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
	hub *marketdata.Hub,
	cache port.Cache,
	trades port.TradeStore,
	agentLog port.AgentLogStore,
	audit port.AuditStore,
	gate *risk.Gate,
	brokers []port.Broker,
	routeFn func(ctx context.Context, symbol domain.Symbol, side domain.Side, size float64) error,
	derivFeed port.DerivativesFeed,
	newsFeed port.NewsFeed,
	calFeed port.CalendarFeed,
	notifiers []port.Notifier,
) *agenttools.Registry {
	reg := agenttools.NewRegistry(cfg.Agent.ToolPolicy)
	reg.Register("get_active_positions", agenttools.GetActivePositions(brokers))
	reg.Register("get_account_balance", agenttools.GetAccountBalance(brokers))
	reg.Register("get_current_drawdown", agenttools.GetCurrentDrawdown(gate)())
	reg.Register("calculate_position_size", agenttools.CalculatePositionSize())
	reg.Register("force_halt_trading", agenttools.ForceHaltTrading(gate, audit, notifiers))
	reg.Register("reject_signal", agenttools.RejectSignal(audit))
	reg.Register("approve_and_route_order", agenttools.ApproveAndRouteOrder(routeFn))
	reg.Register("resize_and_route_order", agenttools.ResizeAndRouteOrder(routeFn, audit))
	reg.Register("get_strategy_performance", agenttools.GetStrategyPerformance(trades))
	reg.Register("query_agent_logs", agenttools.QueryAgentLogs(agentLog))
	reg.Register("get_market_data", agenttools.GetMarketData(agenttools.QuoteProviderFromHub(hub)))
	reg.Register("get_all_market_data", agenttools.GetAllMarketData(agenttools.QuoteProviderFromHub(hub), collectSymbolList(cfg.Markets)))
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

// ── Symbol and config helpers ────────────────────────────────────────────────

type symbolMeta struct {
	cfg          *config.SymbolConfig
	venue        domain.Venue
	contractType domain.ContractType
}

func buildSymbolMetaMap(venues []config.VenueConfig) (map[domain.Symbol]symbolMeta, error) {
	out := make(map[domain.Symbol]symbolMeta)
	for i := range venues {
		for j := range venues[i].Symbols {
			sym := &venues[i].Symbols[j]
			canonical, err := domain.NormalizeConfigSymbol(sym.Symbol, sym.ContractType)
			if err != nil {
				return nil, fmt.Errorf("symbol %q: %w", sym.Symbol, err)
			}
			sym.Symbol = string(canonical)
			out[canonical] = symbolMeta{
				cfg:          sym,
				venue:        venues[i].Venue,
				contractType: sym.ContractType,
			}
		}
	}
	return out, nil
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

// collectAllTimeframeFeeds returns one feed entry per (symbol, timeframe) pair.
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

func collectActiveVenues(venues []config.VenueConfig) []domain.Venue {
	seen := make(map[domain.Venue]bool)
	var out []domain.Venue
	for _, venueCfg := range venues {
		for _, sym := range venueCfg.Symbols {
			if !sym.Enabled {
				continue
			}
			if !seen[venueCfg.Venue] {
				seen[venueCfg.Venue] = true
				out = append(out, venueCfg.Venue)
			}
			break
		}
	}
	return out
}

// ── Utility helpers ───────────────────────────────────────────────────────────

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

func pushTUIOrder(runner *tui.Runner, line string) {
	if runner != nil {
		runner.Push(tui.OrderMsg{Line: line})
	}
}

func durOrDefault(minutes, defaultMinutes int) time.Duration {
	if minutes <= 0 {
		return time.Duration(defaultMinutes)
	}
	return time.Duration(minutes)
}

// ── Log archival ──────────────────────────────────────────────────────────────

// buildLogArchiver creates a port.LogArchiver when a database pool is
// available. Returns nil when pool is nil (no database configured).
func buildLogArchiver(pool *pgxpool.Pool, cfg *config.Config) port.LogArchiver {
	if pool == nil {
		return nil
	}
	archive := cfg.LogRetention.ArchiveBeforePurge
	if !archive {
		if cfg.LogRetention.AgentLogsDays == 0 && cfg.LogRetention.AuditEventsDays == 0 {
			archive = true
		}
	}
	return postgres.NewLogArchiver(pool, archive)
}

// runLogArchiver periodically archives and purges old log records.
func runLogArchiver(ctx context.Context, archiver port.LogArchiver, cfg config.LogRetentionConfig) error {
	agentDays := cfg.AgentLogsDays
	if agentDays <= 0 {
		agentDays = 90
	}
	auditDays := cfg.AuditEventsDays
	if auditDays <= 0 {
		auditDays = 180
	}
	interval := time.Duration(cfg.PurgeIntervalHours) * time.Hour
	if interval <= 0 {
		interval = 24 * time.Hour
	}

	slog.Info("log archiver starting",
		"agent_logs_days", agentDays,
		"audit_events_days", auditDays,
		"interval", interval.String())

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			archived, purged, err := archiver.ArchiveAndPurge(ctx, agentDays, auditDays)
			if err != nil {
				slog.Error("log archiver failed", "error", err)
				continue
			}
			if archived > 0 || purged > 0 {
				slog.Info("log archiver completed",
					"archived", archived, "purged", purged)
			}
		}
	}
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

type symbolFeedConfig struct {
	symbol    domain.Symbol
	timeframe domain.Timeframe
}

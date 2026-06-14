package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/azhar/cerebro/internal/adapter/llm"
	"github.com/azhar/cerebro/internal/adapter/postgres"
	rediscache "github.com/azhar/cerebro/internal/adapter/redis"
	"github.com/azhar/cerebro/internal/adapter/telegram"
	agentpkg "github.com/azhar/cerebro/internal/agent"
	agenttools "github.com/azhar/cerebro/internal/agent/tools"
	"github.com/azhar/cerebro/internal/chatops"
	"github.com/azhar/cerebro/internal/config"
	"github.com/azhar/cerebro/internal/domain"
	"github.com/azhar/cerebro/internal/execution"
	"github.com/azhar/cerebro/internal/execution/paper"
	"github.com/azhar/cerebro/internal/ingest/calendar"
	"github.com/azhar/cerebro/internal/ingest/coinglass"
	"github.com/azhar/cerebro/internal/ingest/news/cryptopanic"
	"github.com/azhar/cerebro/internal/ingest/scrape"
	"github.com/azhar/cerebro/internal/marketdata"
	"github.com/azhar/cerebro/internal/observability"
	"github.com/azhar/cerebro/internal/port"
	"github.com/azhar/cerebro/internal/risk"
	"github.com/azhar/cerebro/internal/strategy"
	"github.com/azhar/cerebro/internal/tui"
	"github.com/azhar/cerebro/internal/watchdog"
	"github.com/jackc/pgx/v5/pgxpool"
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

	// ── Core infrastructure ──────────────────────────────────────────────────
	hub := marketdata.NewHub()
	defer hub.Close()

	var cache port.Cache
	if a.cfg.Secrets.RedisURL != "" {
		rc, err := rediscache.New(a.cfg.Secrets.RedisURL)
		if err != nil {
			slog.Warn("redis connection failed; falling back to in-memory cache", "error", err)
			cache = newMemoryCache()
		} else if err := rc.Ping(ctx); err != nil {
			slog.Warn("redis ping failed; falling back to in-memory cache", "error", err)
			_ = rc.Close()
			cache = newMemoryCache()
		} else {
			cache = rc
			defer rc.Close()
			slog.Info("cache wired: redis")
		}
	} else {
		cache = newMemoryCache()
		slog.Debug("REDIS_URL not set; using in-memory cache")
	}

	var (
		trades   port.TradeStore
		audit    port.AuditStore
		agentLog port.AgentLogStore
		pool     *pgxpool.Pool
	)

	// dbFallback is captured here and surfaced via the notifier once the
	// notifier is wired below. Running on in-memory stores means trades,
	// audit events, and agent logs are lost on restart — operators MUST
	// know, even when nobody is watching the log file.
	var dbFallback struct {
		active bool
		reason string // either "missing_url" or a redacted connect error
	}

	if a.cfg.Secrets.DatabaseURL != "" {
		var err error
		pool, err = postgres.NewPool(ctx, a.cfg.Secrets.DatabaseURL)
		if err != nil {
			// Log at ERROR (not WARN) so log aggregators alert on it; the
			// DSN is verbose and can include credentials, so redact before
			// emission. Keep the bot running on in-memory stores so live
			// market data still flows; durability is sacrificed but agents
			// can keep evaluating.
			redacted := observability.RedactErrorString(err.Error())
			slog.Error("database connection failed; falling back to in-memory stores — DATA WILL NOT PERSIST",
				"error", redacted)
			dbFallback.active = true
			dbFallback.reason = redacted
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
		dbFallback.active = true
		dbFallback.reason = "DATABASE_URL not set"
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
	gate.SetStartingEquity(decimal.NewFromFloat(paperStartingEquity))
	// Lock the executable-symbol set to markets.yaml so discovery-surfaced
	// symbols (Phase 0) are screening-only and cannot reach a broker.
	gate.SetAllowedSymbols(collectSymbolList(a.cfg.Markets))

	if a.cfg.Engine.KillSwitch {
		gate.SetHalt(domain.HaltModePause)
		slog.Warn("kill_switch=true → engine halted; send /resume via ChatOps to trade")
	}

	// Feed realised PnL from paper closes back into the SAME gate instance so
	// drawdown / daily-loss limits actually trip. The live broker path will
	// wire its own fill-event PnL source when implemented (Phase D.2).
	if matcher != nil {
		matcher.SetPnLReporter(gate)
	}

	// ── Watchdog ─────────────────────────────────────────────────────────────
	minOrphanNotional := collectMinNotional(a.cfg.Markets)
	wd := watchdog.New(brokerList, cache, audit, minOrphanNotional)
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
	var scanFeed port.MarketScanFeed
	var newsFeed port.NewsFeed
	var calFeed port.CalendarFeed

	if a.cfg.Ingest.CoinGlass.Enabled && a.cfg.Secrets.CoinGlassAPIKey != "" {
		cgClient := coinglass.New(a.cfg.Secrets.CoinGlassAPIKey)
		derivFeed = coinglass.NewFeed(cgClient)
		// Same client backs the list-style scanner used by the
		// discovery planner — single shared retry/timeout policy and a
		// single API key consumes one rate-limit bucket on CoinGlass.
		scanFeed = coinglass.NewScanner(cgClient)
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

	// CryptoPanic news feed is instantiated later (after notifiers) so the
	// scraper's circuit-breaker can alert operators when the AES key
	// rotates. FinancialJuice can be wired immediately and runs in parallel
	// with CryptoPanic when both are enabled — the combined refresher merges
	// both streams into a single news:latest cache and TUI digest.
	var fjFeed port.NewsFeed
	if a.cfg.Ingest.FinancialJuice.Enabled {
		fjFeed = scrape.NewFinancialJuice()
		slog.Info("ingest: FinancialJuice news scraper enabled (no API key required)")
	}

	if a.cfg.Ingest.Finnhub.Enabled {
		// Finnhub's /calendar/economic endpoint is premium-only and returns
		// HTTP 403 on the free tier, so the calendar uses the free, keyless
		// FairEconomy weekly feed (the same data ForexFactory publishes)
		// regardless of whether a Finnhub key is configured.
		calFeed = calendar.NewFairEconomy()
		slog.Info("ingest: FairEconomy economic calendar feed enabled (no API key required)")
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

	// ── DB fallback alert (requires notifiers) ────────────────────────────────
	// Surfaced once at startup via the first available notifier. We only emit
	// to ChannelSystemAlerts so this doesn't pollute trade-execution channels.
	if dbFallback.active && len(notifiers) > 0 {
		go func(reason string, n port.Notifier) {
			alertCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			msg := "⚠️ Cerebro started in DEGRADED mode\n" +
				"Database unavailable — running on in-memory stores.\n" +
				"Trades, audit events, and agent logs will NOT persist across restart.\n" +
				"Reason: " + reason
			if err := n.Send(alertCtx, port.ChannelSystemAlerts, msg); err != nil {
				slog.Warn("db fallback alert: notifier send failed", "error", err)
			}
		}(dbFallback.reason, notifiers[0])
	}

	// ── CryptoPanic (RE primary + headless Chromium fallback) ─────────────────
	// Instantiated here so the fallback circuit-breaker can alert via the
	// notifier set up above when CryptoPanic rotates their AES key. The RE
	// path is AES-CBC + zlib over the /web-api/posts/ endpoint; the browser
	// path hooks JSON.parse to capture the decrypted payload from the SPA.
	if a.cfg.Ingest.CryptoPanic.Enabled {
		cpTimeout := time.Duration(a.cfg.Ingest.CryptoPanic.TimeoutSeconds) * time.Second
		if cpTimeout == 0 {
			cpTimeout = 15 * time.Second
		}
		reClient, err := cryptopanic.NewClient(cpTimeout)
		if err != nil {
			slog.Warn("cryptopanic: RE client init failed; news feed unavailable", "error", err)
		} else {
			reFeed := cryptopanic.NewFeed(reClient, a.cfg.Ingest.CryptoPanic.Filter)
			browser := cryptopanic.NewBrowser(60 * time.Second)
			var alertNotifier port.Notifier
			if len(notifiers) > 0 {
				alertNotifier = notifiers[0]
			}
			fallback := cryptopanic.NewFallbackFeed(reFeed, cryptopanic.NewBrowserFeed(browser), cryptopanic.Options{
				Notifier: alertNotifier,
			})
			newsFeed = fallback
			defer fallback.Close()
			slog.Info("ingest: CryptoPanic news feed enabled (RE primary + Chromium fallback)",
				"filter", a.cfg.Ingest.CryptoPanic.Filter)
		}
	}
	// Fall back to FinancialJuice for the agent tool when CryptoPanic isn't
	// available — both ingest runners share the news:latest cache, but the
	// agent tool also needs a live-fetch path for cache misses.
	if newsFeed == nil && fjFeed != nil {
		newsFeed = fjFeed
	}
	if newsFeed == nil {
		slog.Debug("ingest: no news feed configured; fetch_latest_news tool unavailable")
	}

	// ── Symbol source (static + optional discovery) ──────────────────────────
	// Built here so the tool registry can reference it via a late-bound
	// SymbolsProvider closure (discovery-surfaced symbols become visible
	// to get_all_market_data without a restart).
	staticSymbols := collectSymbolList(a.cfg.Markets)
	staticSrc := NewStaticSource(staticSymbols)
	var symbolSource port.SymbolSource = staticSrc
	var discoveryFeeds map[domain.Venue]port.UniverseFeed
	if a.cfg.Agent.Discovery.Enabled {
		discoveryFeeds = buildUniverseFeeds(a.cfg.Agent.Discovery)
		if len(discoveryFeeds) > 0 {
			discSrc := NewDiscoverySource(cache)
			symbolSource = NewUnionSource(staticSrc, discSrc)
			slog.Info("discovery source wired",
				"venues", venueKeys(discoveryFeeds),
				"max_candidates", a.cfg.Agent.Discovery.MaxCandidates)
		} else {
			slog.Warn("discovery enabled but no UniverseFeeds available; falling back to static symbols only")
		}
	}

	// ── Tool registry ─────────────────────────────────────────────────────────
	quoteFallback := buildQuoteFallback(discoveryFeeds)
	toolReg := buildToolRegistry(
		a.cfg, hub, cache, trades, agentLog, audit, gate,
		brokerList, buildRouteOrderFn(router, gate, brokersByVenue, symbolMeta, env, tuiRunner), derivFeed, newsFeed, calFeed, notifiers,
		symbolSource.Symbols, quoteFallback,
	)

	// ── LLM runtime + agents ──────────────────────────────────────────────────
	llmProviders := buildLLMProviders(a.cfg)
	var agentRuntime *agentpkg.Runtime
	var copilotFn func(ctx context.Context, query string) (string, error)
	var costTracker *agentpkg.CostTracker

	if len(llmProviders) > 0 {
		var llmAdapter port.LLM = llm.NewFallbackChain(llmProviders, a.cfg.Agent.LLM.FallbackOn)
		// Wrap with a circuit breaker that short-circuits LLM calls when
		// the provider has been consistently failing. Disabled when
		// `circuit_breaker_error_rate_pct` is 0 (the breaker returns the
		// underlying adapter unchanged).
		llmAdapter = llm.NewCircuitBreaker(llmAdapter, llm.CircuitBreakerOpts{
			ErrorRatePct: a.cfg.Agent.LLM.CircuitBreakerErrorRatePct,
			Window:       time.Duration(a.cfg.Agent.LLM.CircuitBreakerErrorWindowMinutes) * time.Minute,
			Cooldown:     time.Duration(a.cfg.Agent.LLM.CircuitBreakerCooldownMinutes) * time.Minute,
		})
		llmChain := llmAdapter
		agentRuntime = agentpkg.NewRuntime(llmChain, agentLog, a.cfg.Agent)

		// Wire the cost tracker so every LLM invocation records its token
		// usage to Redis. Without this, the daily_token_budget /
		// daily_cost_budget_usd config values are silent no-ops. Pick the
		// first available notifier for budget alerts (nil is fine).
		var budgetNotifier port.Notifier
		if len(notifiers) > 0 {
			budgetNotifier = notifiers[0]
		}
		costTracker = agentpkg.NewCostTracker(
			cache,
			budgetNotifier,
			a.cfg.Agent.LLM.DailyTokenBudget,
			a.cfg.Agent.LLM.DailyCostBudgetUSD,
			a.cfg.Agent.LLM.AlertAtBudgetPct/100.0, // config is 0-100, tracker wants 0-1
		)
		agentRuntime.SetCostTracker(costTracker)

		cop := agentpkg.NewCopilot(agentRuntime, toolReg.ForAgentWithDefs("copilot"))
		copilotFn = cop.Ask
		slog.Info("LLM runtime wired",
			"providers", len(llmProviders),
			"daily_token_budget", a.cfg.Agent.LLM.DailyTokenBudget,
			"daily_cost_budget_usd", a.cfg.Agent.LLM.DailyCostBudgetUSD)

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
		CloseFn:     buildCloseFn(router, env),
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

	bracketTracker := execution.NewBracketTracker()

	// pmQueues holds the per-venue action queues created when the position
	// manager is enabled, so ChatOps /confirm and /reject can resolve an
	// action ID to the queue that owns it.
	pmQueues := make(map[domain.Venue]*execution.ActionQueue)

	for _, venue := range activeVenues {
		venue := venue
		workerCh, ok := router.Channel(venue)
		if !ok {
			return fmt.Errorf("worker channel not registered for venue %s", venue)
		}
		worker := execution.NewWorker(venue, brokersByVenue[venue], trades, audit, cache, bracketTracker, workerCh)
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
		return runStrategyEngine(gctx, hub, registry, dedup, gate, brokersByVenue, env, a.cfg.Agent.LLM, router, symbolMeta, tuiRunner, &metrics, kc, a.cfg.Markets, agentRuntime, toolReg, trades)
	})

	// Heartbeat: prints full metrics summary every 10 s
	g.Go(func() error {
		return runHeartbeat(gctx, hub, gate, brokersByVenue, tuiRunner, &metrics, costTracker)
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

	// ── Position-lifecycle reconciler ──────────────────────────────────────────
	// Job A (bracket guarantee + orphan sweep) is deterministic and runs for
	// every active venue whenever position_manager.enabled is true. Job B
	// (trigger detection → agent decision → action queue) additionally runs
	// when the LLM agent runtime is available. The same bracketTracker created
	// for the workers is shared so "is this position protected?" is consistent.
	if a.cfg.PositionManager.Enabled {
		pmCfg := a.cfg.PositionManager
		biasFor := func(sym domain.Symbol) (domain.BiasScore, bool) {
			raw, err := cache.Get(gctx, fmt.Sprintf("bias:%s", sym))
			if err != nil || raw == nil {
				return domain.BiasNeutral, false
			}
			var b domain.BiasResult
			if json.Unmarshal(raw, &b) != nil {
				return domain.BiasNeutral, false
			}
			return b.Score, true
		}
		biasReason := func(sym domain.Symbol) string {
			raw, err := cache.Get(gctx, fmt.Sprintf("bias:%s", sym))
			if err != nil || raw == nil {
				return ""
			}
			var b domain.BiasResult
			if json.Unmarshal(raw, &b) != nil {
				return ""
			}
			return b.Reasoning
		}
		var decider execution.PositionDecider
		if agentRuntime != nil {
			decider = agentpkg.NewPositionManagerAgent(
				agentRuntime, toolReg.ForAgentWithDefs("position_manager"), pmCfg)
		}
		flipEntry := buildFlipEntryFn(gate, router, brokersByVenue, symbolMeta, env)

		for _, venue := range activeVenues {
			venue := venue
			executor := execution.NewActionExecutor(execution.ActionExecutorDeps{
				Venue:   venue,
				Broker:  brokersByVenue[venue],
				Router:  router,
				Tracker: bracketTracker,
				Env:     env,
				EntryFn: flipEntry,
			})
			queue := execution.NewActionQueue(pmCfg.ConfirmTimeoutSec, pmCfg.AutonomousOnTimeout, executor.Execute)
			queue.SetPositionExists(func(sym domain.Symbol) bool {
				for _, p := range positionsForVenue(gctx, brokersByVenue[venue], venue) {
					if p.Symbol == sym {
						return true
					}
				}
				return false
			})
			// Tick the queue once per second so confirm-window timeouts fire.
			g.Go(func() error {
				t := time.NewTicker(time.Second)
				defer t.Stop()
				for {
					select {
					case <-gctx.Done():
						return nil
					case <-t.C:
						queue.Tick(gctx)
					}
				}
			})
			recon := execution.NewReconciler(execution.ReconcilerDeps{
				Venue:      venue,
				Broker:     brokersByVenue[venue],
				Tracker:    bracketTracker,
				Router:     router,
				Env:        env,
				IntervalMS: pmCfg.ReconcileIntervalMS,
				Positions: func() []domain.Position {
					return positionsForVenue(gctx, brokersByVenue[venue], venue)
				},
				Detector:   execution.NewTriggerDetector(pmCfg.TriggerDebounceSec, pmCfg.ProfitThresholdPct, pmCfg.NearTPSLPct, pmCfg.BiasFlipAgainst),
				Decider:    decider,
				Queue:      queue,
				Bias:       biasFor,
				BiasReason: biasReason,
			})
			g.Go(func() error {
				return recon.Run(gctx)
			})
			// Expose the queue to ChatOps confirm/reject for this venue.
			pmQueues[venue] = queue
		}
		slog.Info("position reconciler wired",
			"venues", len(activeVenues), "job_b_enabled", decider != nil,
			"confirm_timeout_sec", pmCfg.ConfirmTimeoutSec,
			"autonomous_on_timeout", pmCfg.AutonomousOnTimeout)
	}

	// LLM agents (conditional)
	if agentRuntime != nil {
		biasTTL := time.Duration(a.cfg.Agent.BiasTTLMinutes) * time.Minute
		if biasTTL <= 0 {
			biasTTL = 4 * time.Hour
		}

		screeningAgent := agentpkg.NewScreeningAgent(
			agentRuntime, cache,
			toolReg.ForAgentWithDefs("screening"),
			trades,
			a.cfg.Agent, symbolSource, biasTTL,
			notifiers,
		)

		// Attach discovery Phase 0 if configured.
		if len(discoveryFeeds) > 0 {
			discovery := agentpkg.NewDiscovery(
				discoveryFeeds, cache, a.cfg.Agent.Discovery, biasTTL,
			)
			screeningAgent.SetDiscovery(discovery)

			// Attach the deterministic trade planner. Runs even when
			// CoinGlass has no API key (scanFeed nil → price-only
			// regime classification). Plans are cached at
			// agent.DiscoveryPlansCacheKey and pushed to ChatOps.
			planner := agentpkg.NewDiscoveryPlanner(
				scanFeed, cache, notifiers,
				agentpkg.PlannerOptions{
					EnabledStrategy: enabledStrategySet(a.cfg.Strategies),
					MaxPlans:        a.cfg.Agent.Discovery.MaxCandidates,
					MinConfidence:   0.5,
				},
			)
			screeningAgent.SetPlanner(planner)
		}

		// When the TUI is mounted, forward each fresh bias into the
		// Bias / Signals panel. *tui.Runner satisfies BiasPublisher via
		// its SendBias method.
		if tuiRunner != nil {
			screeningAgent.SetBiasPublisher(tuiRunner)
		}
		g.Go(func() error {
			slog.Info("screening agent starting",
				"interval_min", a.cfg.Agent.ScreeningIntervalMinutes,
				"static_symbols", len(staticSymbols),
				"discovery_enabled", a.cfg.Agent.Discovery.Enabled)
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
	} else if len(discoveryFeeds) > 0 {
		// Demo / paper without LLM keys: still run discovery + the
		// deterministic trade planner so the operator gets Telegram
		// candidate reports. The LLM bias is unavailable, so plans
		// carry domain.BiasNeutral.
		biasTTL := time.Duration(a.cfg.Agent.BiasTTLMinutes) * time.Minute
		if biasTTL <= 0 {
			biasTTL = 4 * time.Hour
		}
		discovery := agentpkg.NewDiscovery(
			discoveryFeeds, cache, a.cfg.Agent.Discovery, biasTTL,
		)
		planner := agentpkg.NewDiscoveryPlanner(
			scanFeed, cache, notifiers,
			agentpkg.PlannerOptions{
				EnabledStrategy: enabledStrategySet(a.cfg.Strategies),
				MaxPlans:        a.cfg.Agent.Discovery.MaxCandidates,
				MinConfidence:   0.5,
			},
		)
		interval := time.Duration(a.cfg.Agent.ScreeningIntervalMinutes) * time.Minute
		if interval <= 0 {
			interval = 60 * time.Minute
		}
		g.Go(func() error {
			slog.Info("standalone planner starting (no LLM)",
				"interval_min", interval/time.Minute)
			runStandalonePlanner(gctx, discovery, planner, interval, biasTTL)
			return nil
		})
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
	startIngestRunners(gctx, g, a.cfg, cache, derivFeed, newsFeed, fjFeed, calFeed, tuiRunner)

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
// When a CostTracker is wired it also pushes the current-day LLM budget
// snapshot to the TUI status bar on the same cadence.
func runHeartbeat(
	ctx context.Context,
	hub *marketdata.Hub,
	gate *risk.Gate,
	brokers map[domain.Venue]port.Broker,
	tuiRunner *tui.Runner,
	metrics *runtimeMetrics,
	costTracker *agentpkg.CostTracker,
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

				if costTracker != nil {
					snap := costTracker.Snapshot(ctx)
					per := make(map[string]tui.BudgetProviderUsage, len(snap.PerProvider))
					for provider, u := range snap.PerProvider {
						per[provider] = tui.BudgetProviderUsage{Tokens: u.Tokens, CostUSD: u.CostUSD}
					}
					tuiRunner.SendBudget(tui.BudgetSnapshot{
						Date:          snap.Date,
						TokensUsed:    snap.TokensUsed,
						CostUSD:       snap.CostUSD,
						TokenBudget:   snap.TokenBudget,
						CostBudgetUSD: snap.CostBudgetUSD,
						PerProvider:   per,
						At:            snap.At,
					})
				}
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
	// Loud, visible startup warning when there is no real fallback. Field
	// data (see log analysis) showed 91/105 LLM failures were OpenRouter
	// timeouts on a single-provider chain — a second provider would have
	// caught most of those.
	if len(providers) == 1 {
		slog.Warn("LLM: only one provider wired — no fallback available; "+
			"add a second provider (anthropic or gemini) and set its API key "+
			"in secrets.env to harden against upstream timeouts",
			"provider", providers[0].Provider())
	} else if len(providers) == 0 {
		slog.Error("LLM: no providers wired — all agent invocations will fail closed")
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
	routeFn func(ctx context.Context, req agenttools.AgentOrderRequest) error,
	derivFeed port.DerivativesFeed,
	newsFeed port.NewsFeed,
	calFeed port.CalendarFeed,
	notifiers []port.Notifier,
	symbolsFn agenttools.SymbolsProvider,
	quoteFallback agenttools.QuoteFallback,
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
	reg.Register("get_market_data", agenttools.GetMarketData(agenttools.QuoteProviderFromHub(hub), quoteFallback))
	reg.Register("get_all_market_data", agenttools.GetAllMarketData(
		agenttools.QuoteProviderFromHub(hub),
		symbolsFn,
	))
	reg.Register("get_discovery_candidates", agenttools.GetDiscoveryCandidates(cache))
	if derivFeed != nil {
		reg.Register("get_derivatives_data", agenttools.GetDerivativesData(derivFeed))
	}
	if newsFeed != nil {
		reg.Register("fetch_latest_news", agenttools.FetchLatestNews(newsFeed, cache))
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

// collectMinNotional returns the smallest min_notional across all enabled
// symbols. Used by the watchdog to filter dust-level orphaned positions.
func collectMinNotional(venues []config.VenueConfig) decimal.Decimal {
	result := decimal.Zero
	for _, vc := range venues {
		for _, s := range vc.Symbols {
			if !s.Enabled || s.MinNotional.IsZero() {
				continue
			}
			if result.IsZero() || s.MinNotional.LessThan(result) {
				result = s.MinNotional
			}
		}
	}
	return result
}

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
	// signalsTechOnlyFallback counts signals where the LLM risk agent
	// errored but `technical_only_fallback` let the signal proceed through
	// the deterministic routing path with a reduced position size.
	signalsTechOnlyFallback atomic.Int64
	ordersRouted            atomic.Int64
	orderRouteErrors        atomic.Int64
}

type symbolFeedConfig struct {
	symbol    domain.Symbol
	timeframe domain.Timeframe
}

package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/azhar/cerebro/internal/domain"
	"github.com/joho/godotenv"
	"gopkg.in/yaml.v3"
)

// Config is the merged, validated application configuration.
type Config struct {
	Environment     domain.Environment    `yaml:"environment"`
	Log             LogConfig             `yaml:"log"`
	Engine          EngineConfig          `yaml:"engine"`
	Risk            RiskConfig            `yaml:"risk"`
	Agent           AgentConfig           `yaml:"agent"`
	PositionManager PositionManagerConfig `yaml:"position_manager"`
	Reviewer        ReviewerConfig        `yaml:"reviewer"`
	WebSocket       WSConfig              `yaml:"websocket"`
	ChatOps         ChatOpsConfig         `yaml:"chatops"`
	TUI             TUIConfig             `yaml:"tui"`
	Ingest          IngestConfig          `yaml:"ingest"`
	Backtest        BacktestConfig        `yaml:"backtest"`
	LogRetention    LogRetentionConfig    `yaml:"log_retention"`

	// Loaded separately from markets.yaml and strategies.yaml.
	Markets    []VenueConfig    `yaml:"-"`
	Strategies StrategiesConfig `yaml:"-"`

	// Secrets loaded from secrets.env / environment variables.
	Secrets SecretsConfig `yaml:"-"`
}

type LogConfig struct {
	Level      string `yaml:"level"`        // debug | info | warn | error
	Format     string `yaml:"format"`       // json | text
	File       string `yaml:"file"`         // path to log file (e.g. logs/cerebro.log); empty = no file output
	MaxSizeMB  int    `yaml:"max_size_mb"`  // max file size before rotation (default 100)
	MaxBackups int    `yaml:"max_backups"`  // max old log files to keep (default 5)
	MaxAgeDays int    `yaml:"max_age_days"` // max days to retain old logs (default 30)
}

type LogRetentionConfig struct {
	AgentLogsDays      int  `yaml:"agent_logs_days"`      // purge agent runs/messages older than N days (default 90)
	AuditEventsDays    int  `yaml:"audit_events_days"`    // purge audit events older than N days (default 180)
	ArchiveBeforePurge bool `yaml:"archive_before_purge"` // move to archived_* tables before deleting (default true)
	PurgeIntervalHours int  `yaml:"purge_interval_hours"` // how often to run purge (default 24)
}

type EngineConfig struct {
	EvaluationIntervalMS int  `yaml:"evaluation_interval_ms"`
	KillSwitch           bool `yaml:"kill_switch"`
}

type RiskConfig struct {
	MaxDrawdownPct             float64         `yaml:"max_drawdown_pct"`
	MaxDailyLossPct            float64         `yaml:"max_daily_loss_pct"`
	MaxExposurePct             float64         `yaml:"max_exposure_pct"`
	MaxOpenPositions           int             `yaml:"max_open_positions"`
	MaxOpenPositionsPerVenue   int             `yaml:"max_open_positions_per_venue"`
	MaxOpenPositionsPerSymbol  int             `yaml:"max_open_positions_per_symbol"`
	HaltModeOnDrawdown         domain.HaltMode `yaml:"halt_mode_on_drawdown"`
	ResumeRequiresConfirmation bool            `yaml:"resume_requires_confirmation"`
	MinEquityToTrade           float64         `yaml:"min_equity_to_trade"`
}

type AgentConfig struct {
	ScreeningIntervalMinutes  int              `yaml:"screening_interval_minutes"`
	BiasTTLMinutes            int              `yaml:"bias_ttl_minutes"`
	ScreeningMaxOpportunities int              `yaml:"screening_max_opportunities"`
	MaxTurns                  int              `yaml:"max_turns"`
	TimeoutPerTurnSeconds     int              `yaml:"timeout_per_turn_seconds"`
	TimeoutTotalSeconds       int              `yaml:"timeout_total_seconds"`
	RetryOnTransient          int              `yaml:"retry_on_transient"`
	LLM                       LLMConfig        `yaml:"llm"`
	ToolPolicy                ToolPolicyConfig `yaml:"tool_policy"`
	Discovery                 DiscoveryConfig  `yaml:"discovery"`
}

// PositionManagerConfig controls the position-lifecycle reconciler and the
// Position Manager agent. When Enabled is false the reconciler goroutine is
// not started and existing Monitor/bracket behaviour is unchanged.
type PositionManagerConfig struct {
	Enabled             bool    `yaml:"enabled"`
	ReconcileIntervalMS int     `yaml:"reconcile_interval_ms"`
	ConfirmTimeoutSec   int     `yaml:"confirm_timeout_sec"`
	AutonomousOnTimeout bool    `yaml:"autonomous_on_timeout"`
	TriggerDebounceSec  int     `yaml:"trigger_debounce_sec"`
	LLMFailureAction    string  `yaml:"llm_failure_action"` // tighten_breakeven | hold
	ProfitThresholdPct  float64 `yaml:"profit_threshold_pct"`
	NearTPSLPct         float64 `yaml:"near_tp_sl_pct"`
	BiasFlipAgainst     bool    `yaml:"bias_flip_against"`
}

// Validate checks the position-manager config. A disabled block skips all
// numeric checks so operators can leave placeholder zeros.
func (p PositionManagerConfig) Validate() error {
	if !p.Enabled {
		return nil
	}
	if p.ReconcileIntervalMS <= 0 {
		return fmt.Errorf("position_manager.reconcile_interval_ms must be > 0")
	}
	if p.ConfirmTimeoutSec <= 0 {
		return fmt.Errorf("position_manager.confirm_timeout_sec must be > 0")
	}
	if p.TriggerDebounceSec < 0 {
		return fmt.Errorf("position_manager.trigger_debounce_sec must be >= 0")
	}
	switch p.LLMFailureAction {
	case "tighten_breakeven", "hold":
	default:
		return fmt.Errorf("position_manager.llm_failure_action must be tighten_breakeven|hold, got %q", p.LLMFailureAction)
	}
	if p.ProfitThresholdPct < 0 {
		return fmt.Errorf("position_manager.profit_threshold_pct must be >= 0")
	}
	if p.NearTPSLPct < 0 {
		return fmt.Errorf("position_manager.near_tp_sl_pct must be >= 0")
	}
	return nil
}

// DiscoveryConfig controls the dynamic symbol-discovery phase (Phase 0) that
// expands the screening universe beyond markets.yaml by scanning Binance's
// full USDT-M futures ticker feed for top movers and new listings.
//
// Discovered symbols are screening-only: the risk gate still blocks any
// execution path for symbols that are not configured in markets.yaml.
type DiscoveryConfig struct {
	Enabled                 bool     `yaml:"enabled"`
	IncludeVenues           []string `yaml:"include_venues"`               // e.g. [binance_futures]
	QuoteAsset              string   `yaml:"quote_asset"`                  // e.g. USDT
	MinQuoteVolume24hUSD    float64  `yaml:"min_quote_volume_24h_usd"`     // liquidity floor
	MinAbsPriceChangePct24h float64  `yaml:"min_abs_price_change_pct_24h"` // |Δ24h| floor
	MaxCandidates           int      `yaml:"max_candidates"`               // top-K cap
	NewListingMaxAgeDays    int      `yaml:"new_listing_max_age_days"`     // 0 = disabled
	BoostNewListings        bool     `yaml:"boost_new_listings"`
}

type LLMConfig struct {
	Providers                   []string                  `yaml:"providers"`
	FallbackOn                  []string                  `yaml:"fallback_on"`
	TechnicalOnlyFallback       bool                      `yaml:"technical_only_fallback"`
	TechnicalOnlySizeMultiplier float64                   `yaml:"technical_only_size_multiplier"`
	Models                      map[string]LLMModelConfig `yaml:"models"`
	DailyTokenBudget            int                       `yaml:"daily_token_budget"`
	DailyCostBudgetUSD          float64                   `yaml:"daily_cost_budget_usd"`
	AlertAtBudgetPct            float64                   `yaml:"alert_at_budget_pct"`

	// Circuit breaker — trips when the observed LLM error rate exceeds the
	// threshold over a sliding window, then stops calling the LLM for a
	// cooldown period. Set ErrorRatePct to 0 to disable.
	CircuitBreakerErrorRatePct       float64 `yaml:"circuit_breaker_error_rate_pct"`
	CircuitBreakerErrorWindowMinutes int     `yaml:"circuit_breaker_error_window_minutes"`
	CircuitBreakerCooldownMinutes    int     `yaml:"circuit_breaker_cooldown_minutes"`
}

type LLMModelConfig struct {
	ModelID         string  `yaml:"model_id"`
	Temperature     float64 `yaml:"temperature"`
	MaxOutputTokens int     `yaml:"max_output_tokens"`
}

type ToolPolicyConfig struct {
	Copilot   ToolPolicy `yaml:"copilot"`
	Screening ToolPolicy `yaml:"screening"`
	Risk      ToolPolicy `yaml:"risk"`
	Reviewer  ToolPolicy `yaml:"reviewer"`
}

type ToolPolicy struct {
	Denied []string `yaml:"denied"`
}

type ReviewerConfig struct {
	Enabled           bool   `yaml:"enabled"`
	ScheduleCron      string `yaml:"schedule_cron"`
	MinTradesRequired int    `yaml:"min_trades_required"`
	LookbackDays      int    `yaml:"lookback_days"`
}

type WSConfig struct {
	ReconnectBaseDelayMS int `yaml:"reconnect_base_delay_ms"`
	ReconnectMaxDelayMS  int `yaml:"reconnect_max_delay_ms"`
	PingIntervalSeconds  int `yaml:"ping_interval_seconds"`
	PongTimeoutSeconds   int `yaml:"pong_timeout_seconds"`
	AlertAfterFailures   int `yaml:"alert_after_failures"`
}

type ChatOpsConfig struct {
	FlattenConfirmationTimeoutSeconds int `yaml:"flatten_confirmation_timeout_seconds"`
}

type TUIConfig struct {
	RefreshRateMS    int `yaml:"refresh_rate_ms"`
	MaxAgentLogLines int `yaml:"max_agent_log_lines"`
}

type IngestConfig struct {
	CoinGlass        IngestSourceConfig `yaml:"coinglass"`
	CoinGlassScraper IngestSourceConfig `yaml:"coinglass_scraper"`
	CryptoPanic      CryptoPanicConfig  `yaml:"cryptopanic"`
	Finnhub          IngestSourceConfig `yaml:"finnhub"`
	FinancialJuice   IngestSourceConfig `yaml:"financialjuice"`
}

type IngestSourceConfig struct {
	Enabled         bool `yaml:"enabled"`
	IntervalMinutes int  `yaml:"interval_minutes"`
	TimeoutSeconds  int  `yaml:"timeout_seconds"`
}

// CryptoPanicConfig extends the shared ingest knobs with CryptoPanic-specific
// tuning. Filter maps to the SPA's filter query parameter
// (hot|rising|bullish|bearish|important|saved|lol). Currencies limits the
// per-tick scrape to the given tickers (empty = global feed). MaxItems caps
// how many posts we keep in the Redis cache per tick.
type CryptoPanicConfig struct {
	IngestSourceConfig `yaml:",inline"`
	Filter             string   `yaml:"filter"`
	Currencies         []string `yaml:"currencies"`
	MaxItems           int      `yaml:"max_items"`
}

type BacktestConfig struct {
	FillModel     string  `yaml:"fill_model"`
	CommissionPct float64 `yaml:"commission_pct"`
	SlippagePct   float64 `yaml:"slippage_pct"`
}

// SecretsConfig holds all credentials loaded from environment variables.
type SecretsConfig struct {
	Environment string

	// Mainnet
	BinanceAPIKey    string
	BinanceAPISecret string
	// Testnet (paper mode)
	BinanceTestnetAPIKey    string
	BinanceTestnetAPISecret string
	// Demo Trading — register at https://demo.binance.com
	BinanceDemoAPIKey    string
	BinanceDemoAPISecret string

	// Futures mainnet
	BinanceFuturesAPIKey    string
	BinanceFuturesAPISecret string
	// Futures testnet
	BinanceFuturesTestnetAPIKey    string
	BinanceFuturesTestnetAPISecret string
	// Futures demo
	BinanceDemoFuturesAPIKey    string
	BinanceDemoFuturesAPISecret string

	CoinGlassAPIKey string
	FinnhubAPIKey   string

	DatabaseURL string
	RedisURL    string

	GeminiAPIKey     string
	AnthropicAPIKey  string
	AnthropicBaseURL string
	OpenAIAPIKey     string
	OpenAIBaseURL    string

	TelegramBotToken             string
	TelegramChatID               int64
	TelegramAllowlistUserIDs     []string
	DiscordBotToken              string
	DiscordGuildID               string
	DiscordTradeChannelID        string
	DiscordAIReasoningChannelID  string
	DiscordSystemAlertsChannelID string
}

// Load reads and merges all four config sources.
func Load(secretsPath, appPath, marketsPath, strategiesPath string) (*Config, error) {
	// 1. Load secrets.env into environment (Overload so .env values always win).
	if err := godotenv.Overload(secretsPath); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("load secrets: %w", err)
	}

	// 2. Parse app.yaml
	cfg := &Config{}
	if err := loadYAML(appPath, cfg); err != nil {
		return nil, fmt.Errorf("load app.yaml: %w", err)
	}

	// 3. Parse markets.yaml (with venue-level `defaults:` merged into each symbol).
	venues, err := loadMarkets(marketsPath)
	if err != nil {
		return nil, fmt.Errorf("load markets.yaml: %w", err)
	}
	cfg.Markets = venues

	// 4. Parse strategies.yaml
	strategies, err := loadStrategies(strategiesPath)
	if err != nil {
		return nil, fmt.Errorf("load strategies.yaml: %w", err)
	}
	cfg.Strategies = strategies

	if err := normalizeSymbols(cfg); err != nil {
		return nil, fmt.Errorf("normalize symbols: %w", err)
	}

	// 4b. Resolve per-symbol `strategies:` opt-ins and `default_strategies:`
	//     fallback into each strategy's Markets list. This must happen after
	//     normalizeSymbols so all symbol names are in canonical form.
	resolveStrategyAssignments(cfg)

	// 5. Populate secrets from env
	cfg.Secrets = loadSecrets()

	return cfg, nil
}

func loadYAML(path string, v any) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return yaml.NewDecoder(f).Decode(v)
}

func loadSecrets() SecretsConfig {
	s := SecretsConfig{
		Environment:                    os.Getenv("ENVIRONMENT"),
		BinanceAPIKey:                  os.Getenv("BINANCE_API_KEY"),
		BinanceAPISecret:               os.Getenv("BINANCE_API_SECRET"),
		BinanceTestnetAPIKey:           os.Getenv("BINANCE_TESTNET_API_KEY"),
		BinanceTestnetAPISecret:        os.Getenv("BINANCE_TESTNET_API_SECRET"),
		BinanceDemoAPIKey:              os.Getenv("BINANCE_DEMO_API_KEY"),
		BinanceDemoAPISecret:           os.Getenv("BINANCE_DEMO_API_SECRET"),
		BinanceFuturesAPIKey:           os.Getenv("BINANCE_FUTURES_API_KEY"),
		BinanceFuturesAPISecret:        os.Getenv("BINANCE_FUTURES_API_SECRET"),
		BinanceFuturesTestnetAPIKey:    os.Getenv("BINANCE_FUTURES_TESTNET_API_KEY"),
		BinanceFuturesTestnetAPISecret: os.Getenv("BINANCE_FUTURES_TESTNET_API_SECRET"),
		BinanceDemoFuturesAPIKey:       os.Getenv("BINANCE_DEMO_FUTURES_API_KEY"),
		BinanceDemoFuturesAPISecret:    os.Getenv("BINANCE_DEMO_FUTURES_API_SECRET"),
		CoinGlassAPIKey:                os.Getenv("COINGLASS_API_KEY"),
		FinnhubAPIKey:                  os.Getenv("FINNHUB_API_KEY"),
		DatabaseURL:                    os.Getenv("DATABASE_URL"),
		RedisURL:                       os.Getenv("REDIS_URL"),
		GeminiAPIKey:                   os.Getenv("GEMINI_API_KEY"),
		AnthropicAPIKey:                os.Getenv("ANTHROPIC_API_KEY"),
		AnthropicBaseURL:               os.Getenv("ANTHROPIC_BASE_URL"),
		OpenAIAPIKey:                   os.Getenv("OPENAI_API_KEY"),
		OpenAIBaseURL:                  os.Getenv("OPENAI_BASE_URL"),
		TelegramBotToken:               os.Getenv("TELEGRAM_BOT_TOKEN"),
		DiscordBotToken:                os.Getenv("DISCORD_BOT_TOKEN"),
		DiscordGuildID:                 os.Getenv("DISCORD_GUILD_ID"),
		DiscordTradeChannelID:          os.Getenv("DISCORD_TRADE_CHANNEL_ID"),
		DiscordAIReasoningChannelID:    os.Getenv("DISCORD_AI_REASONING_CHANNEL_ID"),
		DiscordSystemAlertsChannelID:   os.Getenv("DISCORD_SYSTEM_ALERTS_CHANNEL_ID"),
	}

	if raw := os.Getenv("TELEGRAM_CHAT_ID"); raw != "" {
		if id, err := strconv.ParseInt(raw, 10, 64); err == nil {
			s.TelegramChatID = id
		}
	}

	if raw := os.Getenv("TELEGRAM_ALLOWLIST_USER_IDS"); raw != "" {
		for _, id := range strings.Split(raw, ",") {
			if id = strings.TrimSpace(id); id != "" {
				s.TelegramAllowlistUserIDs = append(s.TelegramAllowlistUserIDs, id)
			}
		}
	}

	return s
}

// Validate enforces all cross-config rules from TECHNICAL_DESIGN.md §11.5.
func (c *Config) Validate(cliEnv domain.Environment) error {
	errs := &validationErrors{}

	// Triple-agreement check
	if domain.Environment(c.Secrets.Environment) != c.Environment {
		errs.add("ENVIRONMENT env var (%q) does not match app.yaml environment (%q)", c.Secrets.Environment, c.Environment)
	}
	if cliEnv != "" && cliEnv != c.Environment {
		errs.add("CLI flag environment (%q) does not match app.yaml environment (%q)", cliEnv, c.Environment)
	}

	// Bias TTL must be >= screening interval
	if c.Agent.BiasTTLMinutes < c.Agent.ScreeningIntervalMinutes {
		errs.add("bias_ttl_minutes (%d) must be >= screening_interval_minutes (%d)",
			c.Agent.BiasTTLMinutes, c.Agent.ScreeningIntervalMinutes)
	}

	// Discovery config (Phase 0) — only validated when enabled.
	if c.Agent.Discovery.Enabled {
		d := c.Agent.Discovery
		if d.MaxCandidates <= 0 {
			errs.add("agent.discovery.max_candidates (%d) must be > 0 when discovery is enabled", d.MaxCandidates)
		}
		if d.MaxCandidates > 100 {
			errs.add("agent.discovery.max_candidates (%d) must be <= 100 to keep the LLM prompt tractable", d.MaxCandidates)
		}
		if strings.TrimSpace(d.QuoteAsset) == "" {
			errs.add("agent.discovery.quote_asset must be set when discovery is enabled (e.g. USDT)")
		}
		if len(d.IncludeVenues) == 0 {
			errs.add("agent.discovery.include_venues must list at least one venue when discovery is enabled")
		}
		if d.MinQuoteVolume24hUSD < 0 {
			errs.add("agent.discovery.min_quote_volume_24h_usd must be >= 0")
		}
		if d.MinAbsPriceChangePct24h < 0 {
			errs.add("agent.discovery.min_abs_price_change_pct_24h must be >= 0")
		}
		if d.NewListingMaxAgeDays < 0 {
			errs.add("agent.discovery.new_listing_max_age_days must be >= 0 (0 disables new-listing detection)")
		}
		// Every include_venues entry must match a venue declared in markets.yaml.
		known := make(map[domain.Venue]bool, len(c.Markets))
		for _, v := range c.Markets {
			known[v.Venue] = true
		}
		for _, raw := range d.IncludeVenues {
			v := domain.Venue(strings.TrimSpace(raw))
			if !known[v] {
				errs.add("agent.discovery.include_venues: venue %q is not declared in markets.yaml", raw)
			}
		}
	}

	// Risk guardrails: at least one limit must be set
	if c.Risk.MaxDrawdownPct == 0 && c.Risk.MaxDailyLossPct == 0 {
		errs.add("at least one of max_drawdown_pct or max_daily_loss_pct must be > 0 (no guardrails)")
	}

	// DEMO env requires Binance Demo Trading API keys
	if c.Environment == domain.EnvironmentDemo {
		if c.Secrets.BinanceDemoAPIKey == "" {
			errs.add("BINANCE_DEMO_API_KEY is required in DEMO environment (register at https://demo.binance.com)")
		}
		if c.Secrets.BinanceDemoAPISecret == "" {
			errs.add("BINANCE_DEMO_API_SECRET is required in DEMO environment")
		}
	}

	// LIVE env requires broker API keys
	if c.Environment == domain.EnvironmentLive {
		if c.Secrets.BinanceAPIKey == "" {
			errs.add("BINANCE_API_KEY is required in LIVE environment")
		}
		if c.Secrets.BinanceAPISecret == "" {
			errs.add("BINANCE_API_SECRET is required in LIVE environment")
		}
	}

	// Build symbol timeframe lookup for strategy validation
	symbolTimeframes := buildSymbolTimeframes(c.Markets)

	// Strategy-level validations
	for _, s := range c.Strategies.Strategies {
		if !s.Enabled {
			continue
		}

		if s.MaxPositionSizePct > 0 && s.RiskPctPerTrade > s.MaxPositionSizePct {
			errs.add("strategy %q: risk_pct_per_trade (%.2f) > max_position_size_pct (%.2f)",
				s.Name, s.RiskPctPerTrade, s.MaxPositionSizePct)
		}

		if s.TrailTriggerPct > 0 && s.TrailStepPct == 0 {
			errs.add("strategy %q: trail_trigger_pct > 0 but trail_step_pct is 0", s.Name)
		}

		if len(s.TakeProfitLevels) > 0 {
			var sum float64
			for _, tp := range s.TakeProfitLevels {
				sum += tp.ScaleOutPct
			}
			if sum != 100 {
				errs.add("strategy %q: take_profit_levels scale_out_pct sums to %.1f, must be 100", s.Name, sum)
			}
		}

		for _, sym := range s.Markets {
			tfs, ok := symbolTimeframes[sym]
			if !ok {
				continue
			}
			if !containsTimeframe(tfs, s.PrimaryTimeframe) {
				errs.add("strategy %q: primary_timeframe %q not in symbol %q timeframes", s.Name, s.PrimaryTimeframe, sym)
			}
			if s.TrendTimeframe != "" && !isHigherTimeframe(s.TrendTimeframe, s.PrimaryTimeframe) {
				errs.add("strategy %q: trend_timeframe %q is not higher than primary_timeframe %q", s.Name, s.TrendTimeframe, s.PrimaryTimeframe)
			}
		}
	}

	// leverage > 1 on spot is invalid
	for _, venue := range c.Markets {
		for _, sym := range venue.Symbols {
			if sym.ContractType == domain.ContractSpot && sym.Leverage > 1 {
				errs.add("symbol %q: leverage > 1 not supported on contract_type: spot", sym.Symbol)
			}
		}
	}

	if errs.hasErrors() {
		return fmt.Errorf("%w: %s", domain.ErrConfigInvalid, errs.Error())
	}
	if err := c.PositionManager.Validate(); err != nil {
		return err
	}
	return nil
}

// BiasTTL returns the bias cache TTL as a time.Duration.
func (c *Config) BiasTTL() time.Duration {
	return time.Duration(c.Agent.BiasTTLMinutes) * time.Minute
}

// ScreeningInterval returns the screening agent cadence as a time.Duration.
func (c *Config) ScreeningInterval() time.Duration {
	return time.Duration(c.Agent.ScreeningIntervalMinutes) * time.Minute
}

// IsPaper returns true if running in paper/testnet mode.
func (c *Config) IsPaper() bool {
	return c.Environment == domain.EnvironmentPaper
}

// --- helpers ---

type validationErrors struct {
	msgs []string
}

func (v *validationErrors) add(format string, args ...any) {
	v.msgs = append(v.msgs, fmt.Sprintf(format, args...))
}

func (v *validationErrors) hasErrors() bool { return len(v.msgs) > 0 }

func (v *validationErrors) Error() string {
	return strings.Join(v.msgs, "; ")
}

func buildSymbolTimeframes(venues []VenueConfig) map[string][]domain.Timeframe {
	out := make(map[string][]domain.Timeframe)
	for _, venue := range venues {
		for _, sym := range venue.Symbols {
			out[sym.Symbol] = sym.Timeframes
		}
	}
	return out
}

func containsTimeframe(tfs []domain.Timeframe, tf domain.Timeframe) bool {
	for _, t := range tfs {
		if t == tf {
			return true
		}
	}
	return false
}

// timeframeRank maps timeframe to a numeric rank for comparison.
var timeframeRank = map[domain.Timeframe]int{
	domain.TF1m: 1, domain.TF5m: 2, domain.TF15m: 3,
	domain.TF1h: 4, domain.TF4h: 5, domain.TF1d: 6,
}

func isHigherTimeframe(trend, primary domain.Timeframe) bool {
	return timeframeRank[trend] > timeframeRank[primary]
}

package config

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/azhar/cerebro/internal/domain"
	"github.com/joho/godotenv"
	"github.com/shopspring/decimal"
	"gopkg.in/yaml.v3"
)

// Config is the merged, validated application configuration.
type Config struct {
	Environment domain.Environment `yaml:"environment"`
	Log         LogConfig          `yaml:"log"`
	Engine      EngineConfig       `yaml:"engine"`
	Risk        RiskConfig         `yaml:"risk"`
	Agent       AgentConfig        `yaml:"agent"`
	Reviewer    ReviewerConfig     `yaml:"reviewer"`
	WebSocket   WSConfig           `yaml:"websocket"`
	ChatOps     ChatOpsConfig      `yaml:"chatops"`
	TUI         TUIConfig          `yaml:"tui"`
	Ingest      IngestConfig       `yaml:"ingest"`
	Backtest    BacktestConfig     `yaml:"backtest"`

	// Loaded separately from markets.yaml and strategies.yaml.
	Markets    []VenueConfig    `yaml:"-"`
	Strategies StrategiesConfig `yaml:"-"`

	// Secrets loaded from secrets.env / environment variables.
	Secrets SecretsConfig `yaml:"-"`
}

type LogConfig struct {
	Level  string `yaml:"level"`  // debug | info | warn | error
	Format string `yaml:"format"` // json | text
}

type EngineConfig struct {
	EvaluationIntervalMS int  `yaml:"evaluation_interval_ms"`
	KillSwitch           bool `yaml:"kill_switch"`
}

type RiskConfig struct {
	MaxDrawdownPct             float64          `yaml:"max_drawdown_pct"`
	MaxDailyLossPct            float64          `yaml:"max_daily_loss_pct"`
	MaxExposurePct             float64          `yaml:"max_exposure_pct"`
	MaxOpenPositions           int              `yaml:"max_open_positions"`
	MaxOpenPositionsPerVenue   int              `yaml:"max_open_positions_per_venue"`
	MaxOpenPositionsPerSymbol  int              `yaml:"max_open_positions_per_symbol"`
	HaltModeOnDrawdown         domain.HaltMode  `yaml:"halt_mode_on_drawdown"`
	ResumeRequiresConfirmation bool             `yaml:"resume_requires_confirmation"`
	MinEquityToTrade           float64          `yaml:"min_equity_to_trade"`
}

type AgentConfig struct {
	ScreeningIntervalMinutes int       `yaml:"screening_interval_minutes"`
	BiasTTLMinutes           int       `yaml:"bias_ttl_minutes"`
	MaxTurns                 int       `yaml:"max_turns"`
	TimeoutPerTurnSeconds    int       `yaml:"timeout_per_turn_seconds"`
	TimeoutTotalSeconds      int       `yaml:"timeout_total_seconds"`
	LLM                      LLMConfig `yaml:"llm"`
	ToolPolicy               ToolPolicyConfig `yaml:"tool_policy"`
}

type LLMConfig struct {
	Providers               []string                    `yaml:"providers"`
	FallbackOn              []string                    `yaml:"fallback_on"`
	TechnicalOnlyFallback   bool                        `yaml:"technical_only_fallback"`
	TechnicalOnlySizeMultiplier float64                 `yaml:"technical_only_size_multiplier"`
	Models                  map[string]LLMModelConfig   `yaml:"models"`
	DailyTokenBudget        int                         `yaml:"daily_token_budget"`
	DailyCostBudgetUSD      float64                     `yaml:"daily_cost_budget_usd"`
	AlertAtBudgetPct        float64                     `yaml:"alert_at_budget_pct"`
	CircuitBreakerErrorRate float64                     `yaml:"circuit_breaker_error_rate"`
	CircuitBreakerWindowS   int                         `yaml:"circuit_breaker_window_seconds"`
}

type LLMModelConfig struct {
	ModelID         string  `yaml:"model_id"`
	Temperature     float64 `yaml:"temperature"`
	MaxOutputTokens int     `yaml:"max_output_tokens"`
}

type ToolPolicyConfig struct {
	Copilot   ToolPolicy `yaml:"copilot"`
	Screening ToolPolicy `yaml:"screening"`
}

type ToolPolicy struct {
	Denied []string `yaml:"denied"`
}

type ReviewerConfig struct {
	Enabled          bool   `yaml:"enabled"`
	ScheduleCron     string `yaml:"schedule_cron"`
	MinTradesRequired int   `yaml:"min_trades_required"`
	LookbackDays     int    `yaml:"lookback_days"`
}

type WSConfig struct {
	ReconnectBaseDelayMS  int `yaml:"reconnect_base_delay_ms"`
	ReconnectMaxDelayMS   int `yaml:"reconnect_max_delay_ms"`
	PingIntervalSeconds   int `yaml:"ping_interval_seconds"`
	PongTimeoutSeconds    int `yaml:"pong_timeout_seconds"`
	AlertAfterFailures    int `yaml:"alert_after_failures"`
}

type ChatOpsConfig struct {
	FlattenConfirmationTimeoutSeconds int `yaml:"flatten_confirmation_timeout_seconds"`
}

type TUIConfig struct {
	RefreshRateMS    int `yaml:"refresh_rate_ms"`
	MaxAgentLogLines int `yaml:"max_agent_log_lines"`
}

type IngestConfig struct {
	CoinGlass  IngestSourceConfig `yaml:"coinglass"`
	CryptoPanic IngestSourceConfig `yaml:"cryptopanic"`
	Myfxbook   IngestSourceConfig `yaml:"myfxbook"`
	FinancialJuice IngestSourceConfig `yaml:"financialjuice"`
}

type IngestSourceConfig struct {
	Enabled         bool `yaml:"enabled"`
	IntervalMinutes int  `yaml:"interval_minutes"`
	TimeoutSeconds  int  `yaml:"timeout_seconds"`
}

type BacktestConfig struct {
	FillModel     string  `yaml:"fill_model"`
	CommissionPct float64 `yaml:"commission_pct"`
	SlippagePct   float64 `yaml:"slippage_pct"`
}

// VenueConfig holds all symbol configs for a single broker venue.
type VenueConfig struct {
	Venue   domain.Venue   `yaml:"venue"`
	Symbols []SymbolConfig `yaml:"symbols"`
}

// SymbolConfig is the full per-symbol market configuration.
type SymbolConfig struct {
	Symbol             string             `yaml:"symbol"`
	ContractType       domain.ContractType `yaml:"contract_type"`
	Leverage           int                `yaml:"leverage"`
	MarginType         domain.MarginType  `yaml:"margin_type"`
	TickSize           decimal.Decimal    `yaml:"tick_size"`
	LotSize            decimal.Decimal    `yaml:"lot_size"`
	MinLotUnits        decimal.Decimal    `yaml:"min_lot_units"`
	MaxLotUnits        decimal.Decimal    `yaml:"max_lot_units"`
	MinNotional        decimal.Decimal    `yaml:"min_notional"`
	MaxOrderNotional   decimal.Decimal    `yaml:"max_order_notional"`
	MaxPositionSizePct float64            `yaml:"max_position_size_pct"`
	MaxSpreadPct       float64            `yaml:"max_spread_pct"`
	Timeframes         []domain.Timeframe `yaml:"timeframes"`
	PrimaryTimeframe   domain.Timeframe   `yaml:"primary_timeframe"`
	TrendTimeframe     domain.Timeframe   `yaml:"trend_timeframe"`
	Enabled            bool               `yaml:"enabled"`
}

// StrategiesConfig wraps all named strategy presets.
type StrategiesConfig struct {
	Strategies []StrategyConfig `yaml:"strategies"`
}

// StrategyConfig holds all parameters for a single strategy preset.
type StrategyConfig struct {
	Name                    domain.StrategyName   `yaml:"name"`
	Enabled                 bool                  `yaml:"enabled"`
	Markets                 []string              `yaml:"markets"`
	PrimaryTimeframe        domain.Timeframe      `yaml:"primary_timeframe"`
	TrendTimeframe          domain.Timeframe      `yaml:"trend_timeframe"`
	WarmupCandles           int                   `yaml:"warmup_candles"`
	OrderType               domain.OrderType      `yaml:"order_type"`
	LimitOffsetPips         float64               `yaml:"limit_offset_pips"`
	TimeInForce             domain.TimeInForce    `yaml:"time_in_force"`
	OrderCancelAfterSeconds int                   `yaml:"order_cancel_after_seconds"`
	ConfirmationCandles     int                   `yaml:"confirmation_candles"`
	SignalDedupWindowSeconds int                  `yaml:"signal_dedup_window_seconds"`
	RiskPctPerTrade         float64               `yaml:"risk_pct_per_trade"`
	MaxPositionSizePct      float64               `yaml:"max_position_size_pct"`
	StopLoss                StopLossConfig        `yaml:"stop_loss"`
	TakeProfitLevels        []TPLevel             `yaml:"take_profit_levels"`
	TrailTriggerPct         float64               `yaml:"trail_trigger_pct"`
	TrailStepPct            float64               `yaml:"trail_step_pct"`
	Indicators              IndicatorConfig       `yaml:"indicators"`
	SessionFilter           domain.SessionFilter  `yaml:"session_filter"`
	NewsBlackoutBeforeMin   int                   `yaml:"news_blackout_before_minutes"`
	NewsBlackoutAfterMin    int                   `yaml:"news_blackout_after_minutes"`
	MaxSpreadPct            float64               `yaml:"max_spread_pct"`
	RequireBiasAlignment    bool                  `yaml:"require_bias_alignment"`
	RequireTrendAlignment   bool                  `yaml:"require_trend_alignment"`
	// Derivatives filters
	FundingRateLongMaxPct     float64 `yaml:"funding_rate_long_max_pct"`
	FundingRateShortMinPct    float64 `yaml:"funding_rate_short_min_pct"`
	OIDivergenceFilter        bool    `yaml:"oi_divergence_filter"`
	LongShortRatioMaxLong     float64 `yaml:"long_short_ratio_max_long"`
	LongShortRatioMinShort    float64 `yaml:"long_short_ratio_min_short"`
	RequirePositiveTakerDelta bool    `yaml:"require_positive_taker_delta"`
	AvoidLiquidationZonePct   float64 `yaml:"avoid_liquidation_zone_pct"`
	FearGreedLongMin          int     `yaml:"fear_greed_long_min"`
	FearGreedShortMax         int     `yaml:"fear_greed_short_max"`
}

type StopLossConfig struct {
	Type           domain.StopLossType `yaml:"type"`
	ATRMultiplier  float64             `yaml:"atr_multiplier"`
	FixedPips      float64             `yaml:"fixed_pips"`
	FixedPct       float64             `yaml:"fixed_pct"`
	MinDistancePips float64            `yaml:"min_distance_pips"`
}

type TPLevel struct {
	RRRatio           float64 `yaml:"rr_ratio"`
	ScaleOutPct       float64 `yaml:"scale_out_pct"`
	MoveSLToBreakeven bool    `yaml:"move_sl_to_breakeven"`
}

type IndicatorConfig struct {
	RSI       RSIConfig       `yaml:"rsi"`
	EMA       EMAConfig       `yaml:"ema"`
	Bollinger BollingerConfig `yaml:"bollinger"`
	ATR       ATRConfig       `yaml:"atr"`
	MACD      MACDConfig      `yaml:"macd"`
	Volume    VolumeConfig    `yaml:"volume"`
}

type RSIConfig struct {
	Period    int `yaml:"period"`
	Oversold  int `yaml:"oversold"`
	Overbought int `yaml:"overbought"`
}

type EMAConfig struct {
	Fast      int `yaml:"fast"`
	Slow      int `yaml:"slow"`
	Trend     int `yaml:"trend"`
	LongTrend int `yaml:"long_trend"`
}

type BollingerConfig struct {
	Period int     `yaml:"period"`
	StdDev float64 `yaml:"std_dev"`
}

type ATRConfig struct {
	Period int `yaml:"period"`
}

type MACDConfig struct {
	Fast   int `yaml:"fast"`
	Slow   int `yaml:"slow"`
	Signal int `yaml:"signal"`
}

type VolumeConfig struct {
	MinVolumeMultiplier float64 `yaml:"min_volume_multiplier"`
	VolumeAvgPeriod     int     `yaml:"volume_avg_period"`
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

	CoinGlassAPIKey    string
	CryptoPanicAPIKey  string

	DatabaseURL string
	RedisURL    string

	GeminiAPIKey    string
	AnthropicAPIKey string
	OpenAIAPIKey    string
	OpenAIBaseURL   string

	TelegramBotToken          string
	TelegramAllowlistUserIDs  []string
	DiscordBotToken           string
	DiscordGuildID            string
	DiscordTradeChannelID     string
	DiscordAIReasoningChannelID string
	DiscordSystemAlertsChannelID string
}

// Load reads and merges all four config sources.
func Load(secretsPath, appPath, marketsPath, strategiesPath string) (*Config, error) {
	// 1. Load secrets.env into environment
	if err := godotenv.Load(secretsPath); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("load secrets: %w", err)
	}

	// 2. Parse app.yaml
	cfg := &Config{}
	if err := loadYAML(appPath, cfg); err != nil {
		return nil, fmt.Errorf("load app.yaml: %w", err)
	}

	// 3. Parse markets.yaml
	var marketsFile struct {
		Venues []VenueConfig `yaml:"venues"`
	}
	if err := loadYAML(marketsPath, &marketsFile); err != nil {
		return nil, fmt.Errorf("load markets.yaml: %w", err)
	}
	cfg.Markets = marketsFile.Venues

	// 4. Parse strategies.yaml
	strategies, err := loadStrategies(strategiesPath)
	if err != nil {
		return nil, fmt.Errorf("load strategies.yaml: %w", err)
	}
	cfg.Strategies = strategies

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

// loadStrategies supports both supported schema variants:
// 1) list-style: { strategies: [ ... ] }
// 2) preset-map style: { defaults: { ... }, mean_reversion: { ... }, ... }
func loadStrategies(path string) (StrategiesConfig, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return StrategiesConfig{}, err
	}

	// First, try canonical list-style schema.
	var direct StrategiesConfig
	if err := yaml.Unmarshal(raw, &direct); err != nil {
		return StrategiesConfig{}, err
	}
	if len(direct.Strategies) > 0 {
		return direct, nil
	}

	// Fallback: preset-map schema with optional "defaults" inheritance.
	var top map[string]any
	if err := yaml.Unmarshal(raw, &top); err != nil {
		return StrategiesConfig{}, err
	}

	defaults := map[string]any{}
	if d, ok := asMap(top["defaults"]); ok {
		defaults = d
	}

	out := make([]StrategyConfig, 0, len(top))
	names := make([]string, 0, len(top))
	for key := range top {
		if key == "defaults" {
			continue
		}
		names = append(names, key)
	}
	sort.Strings(names)

	for _, name := range names {
		section, ok := asMap(top[name])
		if !ok {
			continue
		}

		merged := deepCopyMap(defaults)
		deepMergeMap(merged, section)

		encoded, err := yaml.Marshal(merged)
		if err != nil {
			return StrategiesConfig{}, err
		}

		var sc StrategyConfig
		if err := yaml.Unmarshal(encoded, &sc); err != nil {
			return StrategiesConfig{}, err
		}
		sc.Name = domain.StrategyName(name)
		out = append(out, sc)
	}

	return StrategiesConfig{Strategies: out}, nil
}

func asMap(v any) (map[string]any, bool) {
	m, ok := v.(map[string]any)
	return m, ok
}

func deepCopyMap(src map[string]any) map[string]any {
	dst := make(map[string]any, len(src))
	for k, v := range src {
		if m, ok := asMap(v); ok {
			dst[k] = deepCopyMap(m)
			continue
		}
		dst[k] = v
	}
	return dst
}

func deepMergeMap(dst, src map[string]any) {
	for k, v := range src {
		srcMap, srcIsMap := asMap(v)
		if !srcIsMap {
			dst[k] = v
			continue
		}

		dstMap, dstIsMap := asMap(dst[k])
		if !dstIsMap {
			dst[k] = deepCopyMap(srcMap)
			continue
		}
		deepMergeMap(dstMap, srcMap)
		dst[k] = dstMap
	}
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
		CryptoPanicAPIKey:              os.Getenv("CRYPTOPANIC_API_KEY"),
		DatabaseURL:                    os.Getenv("DATABASE_URL"),
		RedisURL:                       os.Getenv("REDIS_URL"),
		GeminiAPIKey:                   os.Getenv("GEMINI_API_KEY"),
		AnthropicAPIKey:                os.Getenv("ANTHROPIC_API_KEY"),
		OpenAIAPIKey:                   os.Getenv("OPENAI_API_KEY"),
		OpenAIBaseURL:                  os.Getenv("OPENAI_BASE_URL"),
		TelegramBotToken:               os.Getenv("TELEGRAM_BOT_TOKEN"),
		DiscordBotToken:                os.Getenv("DISCORD_BOT_TOKEN"),
		DiscordGuildID:                 os.Getenv("DISCORD_GUILD_ID"),
		DiscordTradeChannelID:          os.Getenv("DISCORD_TRADE_CHANNEL_ID"),
		DiscordAIReasoningChannelID:    os.Getenv("DISCORD_AI_REASONING_CHANNEL_ID"),
		DiscordSystemAlertsChannelID:   os.Getenv("DISCORD_SYSTEM_ALERTS_CHANNEL_ID"),
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
// Any failure returns a wrapped ErrConfigInvalid.
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

		// risk_pct_per_trade must not exceed max_position_size_pct
		if s.MaxPositionSizePct > 0 && s.RiskPctPerTrade > s.MaxPositionSizePct {
			errs.add("strategy %q: risk_pct_per_trade (%.2f) > max_position_size_pct (%.2f)",
				s.Name, s.RiskPctPerTrade, s.MaxPositionSizePct)
		}

		// trail_trigger_pct > 0 requires trail_step_pct > 0
		if s.TrailTriggerPct > 0 && s.TrailStepPct == 0 {
			errs.add("strategy %q: trail_trigger_pct > 0 but trail_step_pct is 0", s.Name)
		}

		// take_profit scale_out_pct must sum to 100
		if len(s.TakeProfitLevels) > 0 {
			var sum float64
			for _, tp := range s.TakeProfitLevels {
				sum += tp.ScaleOutPct
			}
			if sum != 100 {
				errs.add("strategy %q: take_profit_levels scale_out_pct sums to %.1f, must be 100", s.Name, sum)
			}
		}

		// primary_timeframe must be in symbol's timeframes list
		for _, sym := range s.Markets {
			tfs, ok := symbolTimeframes[sym]
			if !ok {
				continue
			}
			if !containsTimeframe(tfs, s.PrimaryTimeframe) {
				errs.add("strategy %q: primary_timeframe %q not in symbol %q timeframes", s.Name, s.PrimaryTimeframe, sym)
			}
			// trend_timeframe must be higher than primary_timeframe
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

package config

import (
	"fmt"
	"os"
	"sort"

	"github.com/azhar/cerebro/internal/domain"
	"github.com/shopspring/decimal"
	"gopkg.in/yaml.v3"
)

// StrategiesConfig wraps all named strategy presets.
type StrategiesConfig struct {
	Strategies []StrategyConfig `yaml:"strategies"`
}

// StrategyConfig holds all parameters for a single strategy preset.
type StrategyConfig struct {
	Name                     domain.StrategyName  `yaml:"name"`
	Enabled                  bool                 `yaml:"enabled"`
	Markets                  []string             `yaml:"markets"`
	PrimaryTimeframe         domain.Timeframe     `yaml:"primary_timeframe"`
	TrendTimeframe           domain.Timeframe     `yaml:"trend_timeframe"`
	WarmupCandles            int                  `yaml:"warmup_candles"`
	OrderType                domain.OrderType     `yaml:"order_type"`
	LimitOffsetPips          float64              `yaml:"limit_offset_pips"`
	TimeInForce              domain.TimeInForce   `yaml:"time_in_force"`
	OrderCancelAfterSeconds  int                  `yaml:"order_cancel_after_seconds"`
	ConfirmationCandles      int                  `yaml:"confirmation_candles"`
	SignalDedupWindowSeconds int                  `yaml:"signal_dedup_window_seconds"`
	RiskPctPerTrade          float64              `yaml:"risk_pct_per_trade"`
	MaxPositionSizePct       float64              `yaml:"max_position_size_pct"`
	StopLoss                 StopLossConfig       `yaml:"stop_loss"`
	TakeProfitLevels         []TPLevel            `yaml:"take_profit_levels"`
	TrailTriggerPct          float64              `yaml:"trail_trigger_pct"`
	TrailStepPct             float64              `yaml:"trail_step_pct"`
	Indicators               IndicatorConfig      `yaml:"indicators"`
	SessionFilter            domain.SessionFilter `yaml:"session_filter"`
	NewsBlackoutBeforeMin    int                  `yaml:"news_blackout_before_minutes"`
	NewsBlackoutAfterMin     int                  `yaml:"news_blackout_after_minutes"`
	MaxSpreadPct             float64              `yaml:"max_spread_pct"`
	RequireBiasAlignment     bool                 `yaml:"require_bias_alignment"`
	RequireTrendAlignment    bool                 `yaml:"require_trend_alignment"`
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
	Type            domain.StopLossType `yaml:"type"`
	ATRMultiplier   float64             `yaml:"atr_multiplier"`
	FixedPips       float64             `yaml:"fixed_pips"`
	FixedPct        float64             `yaml:"fixed_pct"`
	MinDistancePips float64             `yaml:"min_distance_pips"`
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
	Period     int `yaml:"period"`
	Oversold   int `yaml:"oversold"`
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

// SymbolConfig is the full per-symbol market configuration.
type SymbolConfig struct {
	Symbol             string              `yaml:"symbol"`
	ContractType       domain.ContractType `yaml:"contract_type"`
	Leverage           int                 `yaml:"leverage"`
	MarginType         domain.MarginType   `yaml:"margin_type"`
	TickSize           decimal.Decimal     `yaml:"tick_size"`
	LotSize            decimal.Decimal     `yaml:"lot_size"`
	MinLotUnits        decimal.Decimal     `yaml:"min_lot_units"`
	MaxLotUnits        decimal.Decimal     `yaml:"max_lot_units"`
	MinNotional        decimal.Decimal     `yaml:"min_notional"`
	MaxOrderNotional   decimal.Decimal     `yaml:"max_order_notional"`
	MaxPositionSizePct float64             `yaml:"max_position_size_pct"`
	MaxSpreadPct       float64             `yaml:"max_spread_pct"`
	Timeframes         []domain.Timeframe  `yaml:"timeframes"`
	PrimaryTimeframe   domain.Timeframe    `yaml:"primary_timeframe"`
	TrendTimeframe     domain.Timeframe    `yaml:"trend_timeframe"`
	Enabled            bool                `yaml:"enabled"`
}

// VenueConfig holds all symbol configs for a single broker venue.
type VenueConfig struct {
	Venue   domain.Venue   `yaml:"venue"`
	Symbols []SymbolConfig `yaml:"symbols"`
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

func normalizeSymbols(cfg *Config) error {
	known := make(map[domain.Symbol]bool)
	for i := range cfg.Markets {
		for j := range cfg.Markets[i].Symbols {
			sym := &cfg.Markets[i].Symbols[j]
			canonical, err := domain.NormalizeConfigSymbol(sym.Symbol, sym.ContractType)
			if err != nil {
				return fmt.Errorf("market symbol %q: %w", sym.Symbol, err)
			}
			sym.Symbol = string(canonical)
			known[canonical] = true
		}
	}

	for i := range cfg.Strategies.Strategies {
		markets := cfg.Strategies.Strategies[i].Markets
		for j, raw := range markets {
			canonical, err := resolveStrategySymbol(raw, known)
			if err != nil {
				return fmt.Errorf("strategy %q market %q: %w", cfg.Strategies.Strategies[i].Name, raw, err)
			}
			cfg.Strategies.Strategies[i].Markets[j] = string(canonical)
		}
	}

	return nil
}

func resolveStrategySymbol(raw string, known map[domain.Symbol]bool) (domain.Symbol, error) {
	candidates := make(map[domain.Symbol]bool)
	for _, contractType := range []domain.ContractType{domain.ContractSpot, domain.ContractFuturesPerp} {
		sym, err := domain.NormalizeConfigSymbol(raw, contractType)
		if err == nil && known[sym] {
			candidates[sym] = true
		}
	}

	switch len(candidates) {
	case 0:
		return "", fmt.Errorf("symbol not found in markets.yaml")
	case 1:
		for sym := range candidates {
			return sym, nil
		}
	}

	return "", fmt.Errorf("ambiguous symbol; use canonical form such as BTC/USDT or BTC/USDT-PERP")
}

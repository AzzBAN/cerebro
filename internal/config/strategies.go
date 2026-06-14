package config

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/azhar/cerebro/internal/domain"
	"github.com/shopspring/decimal"
	"gopkg.in/yaml.v3"
)

// StrategiesConfig wraps all named strategy presets.
type StrategiesConfig struct {
	// DefaultStrategies is the global fallback list applied to every market
	// in markets.yaml that doesn't specify its own `strategies:` override.
	// Strategy names must match a defined strategy preset (e.g. mean_reversion).
	DefaultStrategies []domain.StrategyName `yaml:"default_strategies"`

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

	// Strategies is an optional per-symbol override of which strategies fire
	// on this market. When unset (nil), the symbol falls back to
	// StrategiesConfig.DefaultStrategies. When set (even as an empty list),
	// that exact list wins — pass [] to opt the symbol out of all strategies.
	Strategies []domain.StrategyName `yaml:"strategies,omitempty"`
}

// VenueConfig holds all symbol configs for a single broker venue.
//
// Defaults is an optional template merged into every Symbol entry below it
// before the symbol is decoded. Per-symbol fields always win over defaults.
// This lets users add a new watch with just `- symbol: BTC/USDT` while
// inheriting timeframes, leverage, lot/tick sizes, etc. from the venue.
//
// Defaults is preserved as a yaml.Node (not a typed SymbolConfig) so that
// numeric precision for tick/lot sizes survives the round-trip — go-yaml
// would otherwise parse them as float64 via map[string]any.
type VenueConfig struct {
	Venue    domain.Venue   `yaml:"venue"`
	Defaults yaml.Node      `yaml:"defaults"`
	Symbols  []SymbolConfig `yaml:"symbols"`
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

	// `default_strategies:` is the top-level fallback list applied to symbols
	// that don't define their own `strategies:` override in markets.yaml.
	var defaultStrategies []domain.StrategyName
	if rawDefaults, ok := top["default_strategies"]; ok {
		if list, ok := rawDefaults.([]any); ok {
			for _, v := range list {
				if s, ok := v.(string); ok {
					defaultStrategies = append(defaultStrategies, domain.StrategyName(s))
				}
			}
		}
	}

	// Reserved keys that aren't strategy presets.
	reserved := map[string]bool{
		"defaults":           true,
		"default_strategies": true,
	}

	out := make([]StrategyConfig, 0, len(top))
	names := make([]string, 0, len(top))
	for key := range top {
		if reserved[key] {
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

	return StrategiesConfig{
		DefaultStrategies: defaultStrategies,
		Strategies:        out,
	}, nil
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

// loadMarkets reads markets.yaml and applies per-venue `defaults:` to every
// symbol within that venue. Per-symbol fields always win over venue defaults.
//
// We operate on yaml.Node rather than map[string]any so numeric precision
// for tick/lot sizes (decimal.Decimal) survives the merge step.
func loadMarkets(path string) ([]VenueConfig, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	// rawMarkets keeps each symbol as a yaml.Node so we can splice in the
	// venue defaults map without losing precision.
	type rawVenue struct {
		Venue    domain.Venue `yaml:"venue"`
		Defaults yaml.Node    `yaml:"defaults"`
		Symbols  []yaml.Node  `yaml:"symbols"`
	}
	var rawFile struct {
		Venues []rawVenue `yaml:"venues"`
	}
	if err := yaml.Unmarshal(raw, &rawFile); err != nil {
		return nil, err
	}

	out := make([]VenueConfig, 0, len(rawFile.Venues))
	for vi, rv := range rawFile.Venues {
		v := VenueConfig{Venue: rv.Venue, Defaults: rv.Defaults}
		v.Symbols = make([]SymbolConfig, 0, len(rv.Symbols))

		for si, symNode := range rv.Symbols {
			merged := symNode
			if rv.Defaults.Kind == yaml.MappingNode {
				merged = mergeMappingDefaults(symNode, rv.Defaults)
			}

			var sc SymbolConfig
			if err := merged.Decode(&sc); err != nil {
				return nil, fmt.Errorf("venue[%d] symbol[%d]: %w", vi, si, err)
			}
			v.Symbols = append(v.Symbols, sc)
		}
		out = append(out, v)
	}
	return out, nil
}

// mergeMappingDefaults returns a copy of `target` with any keys present in
// `defaults` (but missing from `target`) appended. This is a shallow merge —
// nested maps are taken whole-cloth from defaults if the target lacks the key,
// which is the expected behaviour for per-symbol overrides.
func mergeMappingDefaults(target, defaults yaml.Node) yaml.Node {
	if target.Kind != yaml.MappingNode {
		return target
	}

	// Build a set of keys already defined on the target.
	existing := make(map[string]struct{}, len(target.Content)/2)
	for i := 0; i+1 < len(target.Content); i += 2 {
		existing[target.Content[i].Value] = struct{}{}
	}

	// Copy the target node and append missing key/value pairs from defaults.
	merged := target
	merged.Content = append([]*yaml.Node(nil), target.Content...)
	for i := 0; i+1 < len(defaults.Content); i += 2 {
		k := defaults.Content[i]
		v := defaults.Content[i+1]
		if _, has := existing[k.Value]; has {
			continue
		}
		merged.Content = append(merged.Content, k, v)
	}
	return merged
}

// resolveStrategyAssignments rebuilds each strategy's Markets list from the
// union of:
//  1. its explicit `markets:` field (if any),
//  2. symbols whose per-symbol `strategies:` includes the strategy, and
//  3. symbols with no `strategies:` field, falling back to default_strategies.
//
// Strategy and symbol names are matched case-insensitively. Symbols still
// must be enabled in markets.yaml to be considered.
func resolveStrategyAssignments(cfg *Config) {
	if len(cfg.Markets) == 0 {
		return
	}

	// Collect each strategy's union of opted-in symbols. Seed with explicit
	// `markets:` from strategies.yaml so we never *narrow* an existing list.
	assignments := make(map[string]map[string]struct{}, len(cfg.Strategies.Strategies))
	for _, s := range cfg.Strategies.Strategies {
		key := strings.ToLower(string(s.Name))
		set := make(map[string]struct{}, len(s.Markets))
		for _, m := range s.Markets {
			set[m] = struct{}{}
		}
		assignments[key] = set
	}

	defaults := cfg.Strategies.DefaultStrategies

	for _, venue := range cfg.Markets {
		for _, sym := range venue.Symbols {
			if !sym.Enabled {
				continue
			}
			// `strategies:` set explicitly (even to []) wins; nil = inherit defaults.
			active := sym.Strategies
			if active == nil {
				active = defaults
			}
			for _, sn := range active {
				name := strings.ToLower(string(sn))
				set, ok := assignments[name]
				if !ok {
					// Strategy referenced but not defined; the validator will
					// surface this. Nothing to assign.
					continue
				}
				set[sym.Symbol] = struct{}{}
			}
		}
	}

	// Write resolved sets back into the strategies, sorted for stability.
	for i := range cfg.Strategies.Strategies {
		s := &cfg.Strategies.Strategies[i]
		set := assignments[strings.ToLower(string(s.Name))]
		if set == nil {
			continue
		}
		markets := make([]string, 0, len(set))
		for m := range set {
			markets = append(markets, m)
		}
		sort.Strings(markets)
		s.Markets = markets
	}
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

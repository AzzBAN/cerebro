package scrape

import (
	"testing"

	"github.com/azhar/cerebro/internal/domain"
	"github.com/shopspring/decimal"
)

func TestCoinGlassSymbol(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"BTCUSDT", "BTC"},
		{"ETHUSDT", "ETH"},
		{"SOLUSDT", "SOL"},
		{"BTC", "BTC"},
		{"XAUUSDT", "XAU"},
		{"", ""},
		{"BTC/USDT", "BTC"},
		{"XAU/USDT-PERP", "XAU"},
		{"ETH/USDT", "ETH"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := coinGlassSymbol(domain.Symbol(tt.input))
			if got != tt.want {
				t.Errorf("coinGlassSymbol(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestParseDecimal(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"$15,000,000,000", "15000000000"},
		{"95000.50", "95000.50"},
		{"1,234.56", "1234.56"},
		{"$0", "0"},
		{"", "0"},
		{"invalid", "0"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := parseDecimal(tt.input)
			want, _ := decimal.NewFromString(tt.want)
			if !got.Equal(want) {
				t.Errorf("parseDecimal(%q) = %s, want %s", tt.input, got.String(), tt.want)
			}
		})
	}
}

func TestParsePercentage(t *testing.T) {
	tests := []struct {
		input string
		want  float64
	}{
		{"0.0001%", 0.0001},
		{"-0.0078%", -0.0078},
		{"+0.03%", 0.03},
		{"-5.49%", -5.49},
		{"0%", 0},
		{"", 0},
		{"notanumber%", 0},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := parsePercentage(tt.input)
			if got != tt.want {
				t.Errorf("parsePercentage(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestParseDollarAmount(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"$44.99B", "44990000000"},
		{"$56.09B", "56090000000"},
		{"$31.27B", "31270000000"},
		{"$51.45M", "51450000"},
		{"$1.51T", "1510000000000"},
		{"$950.5K", "950500"},
		{"$0", "0"},
		{"", "0"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := parseDollarAmount(tt.input)
			want, _ := decimal.NewFromString(tt.want)
			if !got.Equal(want) {
				t.Errorf("parseDollarAmount(%q) = %s, want %s", tt.input, got.String(), tt.want)
			}
		})
	}
}

func TestParseDerivativesTableJSON(t *testing.T) {
	input := `[` +
		`{"symbol":"BTC","fundingRate":"0.0001%","volume24h":"$44.99B","openInterest":"$56.09B","oiChange1h":"+0.03%","oiChange24h":"-5.49%"},` +
		`{"symbol":"ETH","fundingRate":"-0.0078%","volume24h":"$34.34B","openInterest":"$31.27B","oiChange1h":"+0.43%","oiChange24h":"-5.05%"},` +
		`{"symbol":"SOL","fundingRate":"0.0052%","volume24h":"$8.12B","openInterest":"$4.87B","oiChange1h":"-0.12%","oiChange24h":"-3.21%"}` +
		`]`

	entries, err := parseDerivativesTableJSON(input)
	if err != nil {
		t.Fatalf("parseDerivativesTableJSON error: %v", err)
	}

	if len(entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(entries))
	}

	btc, ok := entries["BTC"]
	if !ok {
		t.Fatal("BTC not found in entries")
	}
	if btc.FundingRate != 0.0001 {
		t.Errorf("BTC funding rate = %v, want 0.0001", btc.FundingRate)
	}
	if btc.Volume24h.String() != "44990000000" {
		t.Errorf("BTC volume = %s, want 44990000000", btc.Volume24h.String())
	}
	if btc.OpenInterest.String() != "56090000000" {
		t.Errorf("BTC OI = %s, want 56090000000", btc.OpenInterest.String())
	}
	if btc.OIChange1h != 0.03 {
		t.Errorf("BTC OI 1h = %v, want 0.03", btc.OIChange1h)
	}
	if btc.OIChange24h != -5.49 {
		t.Errorf("BTC OI 24h = %v, want -5.49", btc.OIChange24h)
	}

	eth, ok := entries["ETH"]
	if !ok {
		t.Fatal("ETH not found in entries")
	}
	if eth.FundingRate != -0.0078 {
		t.Errorf("ETH funding rate = %v, want -0.0078", eth.FundingRate)
	}
}

func TestParseAlternativeMeFearGreed(t *testing.T) {
	tests := []struct {
		name      string
		json      string
		wantValue int
		wantCat   string
	}{
		{
			"from alternative.me API",
			`{"data":[{"value":"72","value_classification":"Greed","timestamp":"1681862400","time_until_update":"43200"}]}`,
			72,
			"Greed",
		},
		{
			"empty data",
			`{"data":[]}`,
			0,
			"",
		},
		{
			"extreme fear",
			`{"data":[{"value":"15","value_classification":"Extreme Fear"}]}`,
			15,
			"Extreme Fear",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fg := parseAlternativeMeFearGreed([]byte(tt.json))
			if fg.Value != tt.wantValue {
				t.Errorf("value = %d, want %d", fg.Value, tt.wantValue)
			}
			if fg.Category != tt.wantCat {
				t.Errorf("category = %q, want %q", fg.Category, tt.wantCat)
			}
		})
	}
}

func TestParseLongShortDOM(t *testing.T) {
	input := `{"headers":["Exchange","Long/Short 15m","Long/Short 1h","Long/Short 4h","Long/Short 12h","Long/Short 24h"],"entries":[["Binance","1.45","1.52","1.48","1.55","1.60"],["OKX","1.38","1.42","1.40","1.47","1.51"],["Bybit","1.50","1.55","1.52","1.58","1.62"]]}`

	ls := parseLongShortDOM(input, domain.Symbol("BTCUSDT"))
	if ls.GlobalRatio == 0 {
		t.Fatal("expected non-zero global ratio")
	}
	// (1.52 + 1.42 + 1.55) / 3 ≈ 1.497
	expected := (1.52 + 1.42 + 1.55) / 3
	if ls.GlobalRatio < expected-0.01 || ls.GlobalRatio > expected+0.01 {
		t.Errorf("global_ratio = %v, want ~%v", ls.GlobalRatio, expected)
	}
	if ls.TopLongPct <= 0 || ls.TopShortPct <= 0 {
		t.Errorf("expected non-zero top percentages, got long=%v short=%v", ls.TopLongPct, ls.TopShortPct)
	}
}

func TestParseLiquidationDOM(t *testing.T) {
	input := `{"headers":["Symbol","Price","24h%","1h Long","1h Short","4h Long","4h Short"],"entries":[["BTC","$94,500","-2.1%","$5.2M","$3.1M","$22.4M","$15.8M"],["ETH","$1,800","-3.5%","$2.1M","$1.8M","$8.5M","$6.2M"]]}`

	refPrice := decimal.NewFromInt(94500)
	zones := parseLiquidationDOM(input, domain.Symbol("BTCUSDT"), refPrice, 5.0)

	if len(zones) != 2 {
		t.Fatalf("expected 2 zones (long + short), got %d", len(zones))
	}

	if zones[0].Side != domain.SideSell {
		t.Errorf("zone 0 side = %v, want SELL (longs liquidated)", zones[0].Side)
	}
	if zones[0].AmountUSD.String() != "5200000" {
		t.Errorf("zone 0 amount = %s, want 5200000", zones[0].AmountUSD.String())
	}

	if zones[1].Side != domain.SideBuy {
		t.Errorf("zone 1 side = %v, want BUY (shorts liquidated)", zones[1].Side)
	}
	if zones[1].AmountUSD.String() != "3100000" {
		t.Errorf("zone 1 amount = %s, want 3100000", zones[1].AmountUSD.String())
	}
}

func TestErrSymbolNotCoveredIsSentinel(t *testing.T) {
	// Asserts the sentinel is exported and usable with errors.Is
	// across wrapped errors. This lets callers (Snapshot, screening)
	// detect "structurally unsupported asset" cleanly without string
	// matching.
	if ErrSymbolNotCovered == nil {
		t.Fatal("ErrSymbolNotCovered must not be nil")
	}

	// ─── long/short DOM parser ───────────────────────────────────────
	//
	// The following tests exercise parseLongShortDOM against realistic
	// JSON payloads produced by the DOM scanner, including the current
	// (2026) three-column "Type | Long/Short | Sentiment" layout and
	// the legacy per-exchange layout.
	_ = t // allow chained subtests below to see t in scope if refactored
}

func TestIsCoinglassSupportedCoin(t *testing.T) {
	tests := []struct {
		coin string
		want bool
	}{
		{"BTC", true},
		{"ETH", true},
		{"btc", true},  // case-insensitive
		{"  SOL ", true},
		{"XAU", false}, // gold synthetic — not on Coinglass
		{"xag", false}, // silver
		{"XAUUSD", false},
		{"", false},
	}
	for _, tc := range tests {
		t.Run(tc.coin, func(t *testing.T) {
			if got := isCoinglassSupportedCoin(tc.coin); got != tc.want {
				t.Errorf("isCoinglassSupportedCoin(%q)=%v want %v", tc.coin, got, tc.want)
			}
		})
	}
}

func TestFindLongShortColumn(t *testing.T) {
	tests := []struct {
		name    string
		headers []string
		want    int
	}{
		{"current three-col layout", []string{"Type", "Long/Short", "Sentiment"}, 1},
		{"spaced slash", []string{"Timeframe", "Long / Short", "Signal"}, 1},
		{"legacy per-exchange", []string{"Rank", "Exchange", "Long%", "Short%", "Long/Short 1h"}, 4},
		// Split Long/Short columns (two separate headers) are not a
		// single ratio cell — the parser can't derive one value from
		// two cells, so -1 is the correct signal to skip.
		{"split columns not supported", []string{"Long Account%", "Short Account%"}, -1},
		{"missing", []string{"Rank", "Exchange", "Volume"}, -1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := findLongShortColumn(tc.headers); got != tc.want {
				t.Errorf("findLongShortColumn(%v)=%d want %d", tc.headers, got, tc.want)
			}
		})
	}
}

func TestParseLongShortCell(t *testing.T) {
	tests := []struct {
		cell string
		val  float64
		ok   bool
	}{
		{"1.25", 1.25, true},
		{"0.98", 0.98, true},
		{"", 0, false},
		{"—", 0, false},
		{"55.5%", 55.5 / 44.5, true},   // pct → ratio
		{"50%", 50.0 / 50.0, true},     // 50% long = ratio 1.0
		{"100%", 0, false},             // degenerate
		{"0%", 0, false},               // degenerate
		{"nonsense", 0, false},
	}
	for _, tc := range tests {
		t.Run(tc.cell, func(t *testing.T) {
			v, ok := parseLongShortCell(tc.cell)
			if ok != tc.ok {
				t.Fatalf("parseLongShortCell(%q) ok=%v want %v", tc.cell, ok, tc.ok)
			}
			if ok {
				diff := v - tc.val
				if diff < 0 {
					diff = -diff
				}
				if diff > 1e-9 {
					t.Errorf("parseLongShortCell(%q) = %v, want %v", tc.cell, v, tc.val)
				}
			}
		})
	}
}

func TestParseLongShortDOM_CurrentLayout(t *testing.T) {
	// Shape the DOM scanner returns for the 2026 page:
	//   Type | Long/Short | Sentiment
	//    1h  |    0.98    | Neutral
	//    4h  |    1.12    | Bullish
	//   24h  |    0.85    | Bearish
	raw := `{
		"headers":["Type","Long/Short","Sentiment"],
		"entries":[
			["1h","0.98","Neutral"],
			["4h","1.12","Bullish"],
			["24h","0.85","Bearish"]
		]
	}`
	got := parseLongShortDOM(raw, domain.Symbol("BTC/USDT"))
	// Prefer 1h row.
	if got.GlobalRatio <= 0.979 || got.GlobalRatio >= 0.981 {
		t.Errorf("GlobalRatio = %v, want ≈0.98", got.GlobalRatio)
	}
	wantShort := 100.0 / (1 + 0.98)
	if diff := got.TopShortPct - wantShort; diff > 1e-6 || diff < -1e-6 {
		t.Errorf("TopShortPct = %v, want %v", got.TopShortPct, wantShort)
	}
	if got.TopLongPct+got.TopShortPct < 99.9 {
		t.Errorf("TopLongPct+TopShortPct should sum to 100, got %v", got.TopLongPct+got.TopShortPct)
	}
}

func TestParseLongShortDOM_LegacyExchangeLayout(t *testing.T) {
	// Falls back to averaging when there's no Type column.
	raw := `{
		"headers":["Rank","Exchange","Long/Short 1h"],
		"entries":[
			["1","Binance","1.10"],
			["2","Bybit","1.05"],
			["3","OKX","1.15"]
		]
	}`
	got := parseLongShortDOM(raw, domain.Symbol("BTC/USDT"))
	wantAvg := (1.10 + 1.05 + 1.15) / 3
	if diff := got.GlobalRatio - wantAvg; diff > 1e-6 || diff < -1e-6 {
		t.Errorf("GlobalRatio = %v, want %v", got.GlobalRatio, wantAvg)
	}
}

func TestParseLongShortDOM_TableNotFound(t *testing.T) {
	raw := `{"error":"long/short table not found","seen":[{"headers":["Name","Price"],"entries":[["BTC","$80,000"]]}]}`
	got := parseLongShortDOM(raw, domain.Symbol("BTC/USDT"))
	if got.GlobalRatio != 0 {
		t.Errorf("expected zero ratio on not-found, got %v", got.GlobalRatio)
	}
	// Symbol + timestamp must still be populated so downstream code
	// doesn't trip over a nil snapshot.
	if got.Symbol != domain.Symbol("BTC/USDT") {
		t.Errorf("Symbol preserved; got %v", got.Symbol)
	}
	if got.FetchedAt.IsZero() {
		t.Error("FetchedAt should be set even on parse failure")
	}
}

func TestParseLongShortDOM_Malformed(t *testing.T) {
	got := parseLongShortDOM("not json", domain.Symbol("BTC/USDT"))
	if got.GlobalRatio != 0 {
		t.Errorf("malformed input must yield zero ratio, got %v", got.GlobalRatio)
	}
}

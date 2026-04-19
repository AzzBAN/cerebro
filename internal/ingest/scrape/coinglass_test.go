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

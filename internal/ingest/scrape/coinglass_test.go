package scrape

import (
	"encoding/json"
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

func TestParseAPIResponse(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantErr  bool
		wantData string
	}{
		{
			"success envelope",
			`{"success":true,"data":{"rate":0.0001}}`,
			false,
			`{"rate":0.0001}`,
		},
		{
			"code envelope",
			`{"code":"0","data":[{"value":72}]}`,
			false,
			`[{"value":72}]`,
		},
		{
			"error envelope",
			`{"success":false,"message":"rate limited","data":null}`,
			true,
			"",
		},
		{
			"raw array (no envelope)",
			`[{"rate":0.0001}]`,
			false,
			`[{"rate":0.0001}]`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := parseAPIResponse(json.RawMessage(tt.input))
			if (err != nil) != tt.wantErr {
				t.Errorf("parseAPIResponse() err = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr {
				return
			}
			if string(data) != tt.wantData {
				t.Errorf("parseAPIResponse() data = %s, want %s", string(data), tt.wantData)
			}
		})
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

func TestParseLongShortResponse(t *testing.T) {
	body := `{"success":true,"data":[{"globalRatio":1.52,"topLongPct":60.3,"topShortPct":39.7}]}`

	ls := parseLongShortResponse(json.RawMessage(body), domain.Symbol("BTCUSDT"))
	if ls.GlobalRatio != 1.52 {
		t.Errorf("global_ratio = %v, want 1.52", ls.GlobalRatio)
	}
	if ls.TopLongPct != 60.3 {
		t.Errorf("top_long_pct = %v, want 60.3", ls.TopLongPct)
	}
}

func TestParseLiquidationResponse(t *testing.T) {
	body := `{"success":true,"data":[` +
		`{"price":95000,"amountUSD":5000000,"side":"long"},` +
		`{"price":92000,"amountUSD":3000000,"side":"short"},` +
		`{"price":50000,"amountUSD":1000000,"side":"long"}` +
		`]}`

	refPrice := decimal.NewFromInt(94000)
	zones := parseLiquidationResponse(json.RawMessage(body), domain.Symbol("BTCUSDT"), refPrice, 5.0)

	if len(zones) != 2 {
		t.Fatalf("zones count = %d, want 2 (only within 5%% of 94000)", len(zones))
	}

	if zones[0].Side != domain.SideSell {
		t.Errorf("zone 0 side = %v, want SELL (longs liquidated)", zones[0].Side)
	}
	if zones[1].Side != domain.SideBuy {
		t.Errorf("zone 1 side = %v, want BUY (shorts liquidated)", zones[1].Side)
	}
}

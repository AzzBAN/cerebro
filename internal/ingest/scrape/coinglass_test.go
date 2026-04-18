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

func TestParseAPIResponse(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
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

func TestParseFundingRateResponse(t *testing.T) {
	tests := []struct {
		name     string
		json     string
		wantRate float64
	}{
		{
			"array envelope",
			`{"success":true,"data":[{"rate":0.00012,"nextFundingTime":1714000000000}]}`,
			0.00012,
		},
		{
			"object response",
			`{"success":true,"data":{"rate":0.00005,"nextFundingTime":0}}`,
			0.00005,
		},
		{
			"empty data",
			`{"success":true,"data":[]}`,
			0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fr := parseFundingRateResponse(json.RawMessage(tt.json), domain.Symbol("BTCUSDT"))
			if fr.Rate != tt.wantRate {
				t.Errorf("rate = %v, want %v", fr.Rate, tt.wantRate)
			}
			if fr.Symbol != domain.Symbol("BTCUSDT") {
				t.Errorf("symbol = %v, want BTCUSDT", fr.Symbol)
			}
		})
	}
}

func TestParseOpenInterestResponse(t *testing.T) {
	body := `{"success":true,"data":[{"openInterest":15000000000,"change1h":0.5,"change4h":1.2,"change24h":-0.3}]}`

	oi := parseOpenInterestResponse(json.RawMessage(body), domain.Symbol("BTCUSDT"))
	expected := decimal.NewFromInt(15000000000)
	if !oi.TotalUSD.Equal(expected) {
		t.Errorf("total_usd = %s, want %s", oi.TotalUSD.String(), expected.String())
	}
	if oi.Change1h != 0.5 {
		t.Errorf("change_1h = %v, want 0.5", oi.Change1h)
	}
	if oi.Change24h != -0.3 {
		t.Errorf("change_24h = %v, want -0.3", oi.Change24h)
	}
}

func TestParseFearGreedResponse(t *testing.T) {
	tests := []struct {
		name      string
		json      string
		wantValue int
		wantCat   string
	}{
		{
			"from API",
			`{"success":true,"data":[{"value":72,"classification":"Greed"}]}`,
			72,
			"Greed",
		},
		{
			"zero value fallback",
			`{"success":true,"data":[]}`,
			0,
			"Extreme Fear",
		},
		{
			"neutral range",
			`{"success":true,"data":[{"value":48,"classification":""}]}`,
			48,
			"Neutral",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fg := parseFearGreedResponse(json.RawMessage(tt.json))
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
	body := `{"success":true,"data":[
		{"price":95000,"amountUSD":5000000,"side":"long"},
		{"price":92000,"amountUSD":3000000,"side":"short"},
		{"price":50000,"amountUSD":1000000,"side":"long"}
	]}`

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

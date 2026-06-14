package coinglass

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// newTestScanner spins up an httptest server that responds to the given
// path with the given JSON `data` field, wrapped in CoinGlass's standard
// envelope.
func newTestScanner(t *testing.T, expectPath string, data any) *Scanner {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != expectPath {
			t.Errorf("unexpected path: got %q, want %q", r.URL.Path, expectPath)
			http.Error(w, "bad path", http.StatusBadRequest)
			return
		}
		if r.Header.Get("CG-API-KEY") != "test-key" {
			t.Errorf("missing or wrong CG-API-KEY header: %q", r.Header.Get("CG-API-KEY"))
		}
		raw, err := json.Marshal(data)
		if err != nil {
			t.Fatalf("marshal test data: %v", err)
		}
		body := apiResponse{Code: "0", Data: raw}
		_ = json.NewEncoder(w).Encode(body)
	}))
	t.Cleanup(srv.Close)

	c := &Client{
		apiKey: "test-key",
		http:   &http.Client{Timeout: 2 * time.Second},
	}
	// override base URL for the duration of the test
	origBase := baseURLOverride
	baseURLOverride = srv.URL
	t.Cleanup(func() { baseURLOverride = origBase })

	return NewScanner(c)
}

func TestScanner_FundingExtremes_RanksByAbs(t *testing.T) {
	rows := []scanFundingRow{
		{Symbol: "BTCUSDT", FundingRate: 0.0001},
		{Symbol: "PEPEUSDT", FundingRate: -0.0009}, // largest |x|
		{Symbol: "ETHUSDT", FundingRate: 0.0003},
	}
	s := newTestScanner(t, "/api/futures/funding-rate/exchange-list", rows)
	got, err := s.FundingExtremes(context.Background(), 2)
	if err != nil {
		t.Fatalf("FundingExtremes: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want top=2 rows, got %d", len(got))
	}
	if got[0].BaseAsset != "PEPE" {
		t.Errorf("rank[0] base: want PEPE, got %s", got[0].BaseAsset)
	}
	if got[1].BaseAsset != "ETH" {
		t.Errorf("rank[1] base: want ETH, got %s", got[1].BaseAsset)
	}
}

func TestScanner_OpenInterestMovers_RanksByAbs(t *testing.T) {
	rows := []scanOIRow{
		{Symbol: "BTCUSDT", Change24hPct: 2.0, OpenInterestUSD: 10e9},
		{Symbol: "DOGEUSDT", Change24hPct: -18.0, OpenInterestUSD: 1e8},
		{Symbol: "ETHUSDT", Change24hPct: 9.0, OpenInterestUSD: 5e9},
	}
	s := newTestScanner(t, "/api/futures/open-interest/exchange-list", rows)
	got, err := s.OpenInterestMovers(context.Background(), 0)
	if err != nil {
		t.Fatalf("OpenInterestMovers: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("want all 3 rows, got %d", len(got))
	}
	if got[0].BaseAsset != "DOGE" {
		t.Errorf("rank[0]: want DOGE (|−18|), got %s", got[0].BaseAsset)
	}
}

func TestScanner_LiquidationLeaders_ComputesLongRatio(t *testing.T) {
	rows := []scanLiqRow{
		{Symbol: "SOLUSDT", Liquidations24h: 100, LongLiquidation: 70, ShortLiquidation: 30},
		{Symbol: "BTCUSDT", Liquidations24h: 50, LongLiquidation: 10, ShortLiquidation: 40},
	}
	s := newTestScanner(t, "/api/futures/liquidation/coin-list", rows)
	got, err := s.LiquidationLeaders(context.Background(), 0)
	if err != nil {
		t.Fatalf("LiquidationLeaders: %v", err)
	}
	if got[0].BaseAsset != "SOL" || got[0].LiqLongRatio != 0.7 {
		t.Errorf("SOL row: want LiqLongRatio=0.7, got base=%s ratio=%.3f",
			got[0].BaseAsset, got[0].LiqLongRatio)
	}
}

func TestScanner_LongShortExtremes_RanksByDistanceFromOne(t *testing.T) {
	rows := []scanLSRow{
		{Symbol: "BTCUSDT", LongShortRatio: 1.05},
		{Symbol: "DOGEUSDT", LongShortRatio: 2.4}, // furthest from 1.0
		{Symbol: "ETHUSDT", LongShortRatio: 0.7},
	}
	s := newTestScanner(t, "/api/futures/global-long-short-account-ratio/exchange-list", rows)
	got, err := s.LongShortExtremes(context.Background(), 0)
	if err != nil {
		t.Fatalf("LongShortExtremes: %v", err)
	}
	if got[0].BaseAsset != "DOGE" {
		t.Errorf("rank[0]: want DOGE, got %s", got[0].BaseAsset)
	}
}

func TestUpperBase(t *testing.T) {
	cases := map[string]string{
		"BTCUSDT":   "BTC",
		"ethusdt":   "ETH",
		"PEPE_USDT": "PEPE",
		"SOL-USDT":  "SOL",
		"WIF":       "WIF",
		"":          "",
	}
	for in, want := range cases {
		if got := upperBase(in); got != want {
			t.Errorf("upperBase(%q): want %q, got %q", in, want, got)
		}
	}
}

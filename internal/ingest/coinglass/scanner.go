package coinglass

import (
	"context"
	"encoding/json"
	"math"
	"sort"
	"strings"

	"github.com/azhar/cerebro/internal/port"
)

// Scanner implements port.MarketScanFeed using CoinGlass v4 list endpoints.
//
// Unlike Feed (per-symbol), Scanner hits aggregator endpoints that return
// the whole derivatives universe in one call. Results are joined by base
// asset (e.g. "BTC") in the screening layer.
//
// All methods degrade to "best-effort": a partial response is preferred
// over an error so the screening pipeline keeps producing plans even when
// CoinGlass throttles individual endpoints.
type Scanner struct {
	client *Client
}

// NewScanner wraps an existing CoinGlass Client. The Client already
// owns the API key, retry policy and timeout — Scanner only adds the
// list-endpoint surface.
func NewScanner(client *Client) *Scanner {
	return &Scanner{client: client}
}

// scanFundingRow / scanOIRow / scanLiqRow / scanLSRow are the per-endpoint
// payload shapes. CoinGlass uses snake_case in v4 responses. Field names
// were verified against open-api-v4.coinglass.com sample payloads.

type scanFundingRow struct {
	Symbol         string  `json:"symbol"`
	FundingRate    float64 `json:"funding_rate"`
	OpenInterest   float64 `json:"open_interest_usd"`
	OIChange24hPct float64 `json:"open_interest_change_24h_percent"`
}

type scanOIRow struct {
	Symbol          string  `json:"symbol"`
	OpenInterestUSD float64 `json:"open_interest_usd"`
	Change4hPct     float64 `json:"change_4h_percent"`
	Change24hPct    float64 `json:"change_24h_percent"`
}

type scanLiqRow struct {
	Symbol           string  `json:"symbol"`
	Liquidations24h  float64 `json:"liquidation_usd_24h"`
	LongLiquidation  float64 `json:"long_liquidation_usd_24h"`
	ShortLiquidation float64 `json:"short_liquidation_usd_24h"`
}

type scanLSRow struct {
	Symbol         string  `json:"symbol"`
	LongShortRatio float64 `json:"long_short_account_ratio"`
}

// FundingExtremes returns the perpetuals with the largest |funding rate|.
//
// Endpoint: /api/futures/funding-rate/exchange-list (aggregated across
// exchanges in v4).
func (s *Scanner) FundingExtremes(ctx context.Context, top int) ([]port.MarketScanRow, error) {
	var resp apiResponse
	if err := s.client.get(ctx, "/api/futures/funding-rate/exchange-list", nil, &resp); err != nil {
		return nil, err
	}
	var raw []scanFundingRow
	if err := json.Unmarshal(resp.Data, &raw); err != nil {
		return nil, err
	}
	out := make([]port.MarketScanRow, 0, len(raw))
	for _, r := range raw {
		out = append(out, port.MarketScanRow{
			BaseAsset:       upperBase(r.Symbol),
			FundingRate:     r.FundingRate,
			OpenInterestUSD: r.OpenInterest,
			OIChange24hPct:  r.OIChange24hPct,
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		return math.Abs(out[i].FundingRate) > math.Abs(out[j].FundingRate)
	})
	return capRows(out, top), nil
}

// OpenInterestMovers returns the perpetuals with the largest 24h OI Δ%.
//
// Endpoint: /api/futures/open-interest/exchange-list.
func (s *Scanner) OpenInterestMovers(ctx context.Context, top int) ([]port.MarketScanRow, error) {
	var resp apiResponse
	if err := s.client.get(ctx, "/api/futures/open-interest/exchange-list", nil, &resp); err != nil {
		return nil, err
	}
	var raw []scanOIRow
	if err := json.Unmarshal(resp.Data, &raw); err != nil {
		return nil, err
	}
	out := make([]port.MarketScanRow, 0, len(raw))
	for _, r := range raw {
		out = append(out, port.MarketScanRow{
			BaseAsset:       upperBase(r.Symbol),
			OpenInterestUSD: r.OpenInterestUSD,
			OIChange4hPct:   r.Change4hPct,
			OIChange24hPct:  r.Change24hPct,
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		return math.Abs(out[i].OIChange24hPct) > math.Abs(out[j].OIChange24hPct)
	})
	return capRows(out, top), nil
}

// LiquidationLeaders returns the perpetuals with the largest 24h
// liquidation totals.
//
// Endpoint: /api/futures/liquidation/coin-list.
func (s *Scanner) LiquidationLeaders(ctx context.Context, top int) ([]port.MarketScanRow, error) {
	var resp apiResponse
	if err := s.client.get(ctx, "/api/futures/liquidation/coin-list", nil, &resp); err != nil {
		return nil, err
	}
	var raw []scanLiqRow
	if err := json.Unmarshal(resp.Data, &raw); err != nil {
		return nil, err
	}
	out := make([]port.MarketScanRow, 0, len(raw))
	for _, r := range raw {
		ratio := 0.0
		if r.Liquidations24h > 0 {
			ratio = r.LongLiquidation / r.Liquidations24h
		}
		out = append(out, port.MarketScanRow{
			BaseAsset:       upperBase(r.Symbol),
			Liquidations24h: r.Liquidations24h,
			LiqLongRatio:    ratio,
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].Liquidations24h > out[j].Liquidations24h
	})
	return capRows(out, top), nil
}

// LongShortExtremes returns the perpetuals where the global long/short
// account ratio is most lopsided in either direction.
//
// Endpoint: /api/futures/global-long-short-account-ratio/exchange-list.
func (s *Scanner) LongShortExtremes(ctx context.Context, top int) ([]port.MarketScanRow, error) {
	var resp apiResponse
	if err := s.client.get(ctx, "/api/futures/global-long-short-account-ratio/exchange-list", nil, &resp); err != nil {
		return nil, err
	}
	var raw []scanLSRow
	if err := json.Unmarshal(resp.Data, &raw); err != nil {
		return nil, err
	}
	out := make([]port.MarketScanRow, 0, len(raw))
	for _, r := range raw {
		out = append(out, port.MarketScanRow{
			BaseAsset:      upperBase(r.Symbol),
			LongShortRatio: r.LongShortRatio,
		})
	}
	// Sort by distance from 1.0 (perfectly balanced).
	sort.SliceStable(out, func(i, j int) bool {
		return math.Abs(out[i].LongShortRatio-1) > math.Abs(out[j].LongShortRatio-1)
	})
	return capRows(out, top), nil
}

// upperBase normalises a CoinGlass-style symbol ("BTCUSDT", "BTC", or
// rarely "BTC_USDT") to its base-asset code in upper case ("BTC"). The
// scanner emits these so the screening layer can join by base asset.
func upperBase(s string) string {
	s = strings.ToUpper(strings.TrimSpace(s))
	// Strip the longest separator-prefixed suffixes first so that
	// "PEPE_USDT" → "PEPE", not "PEPE_".
	for _, suf := range []string{"_USDT", "-USDT", "_USDC", "-USDC", "_BUSD", "-BUSD"} {
		if strings.HasSuffix(s, suf) {
			return strings.TrimSuffix(s, suf)
		}
	}
	for _, suf := range []string{"USDT", "USDC", "BUSD"} {
		if strings.HasSuffix(s, suf) {
			return strings.TrimSuffix(s, suf)
		}
	}
	return s
}

func capRows(rows []port.MarketScanRow, top int) []port.MarketScanRow {
	if top > 0 && len(rows) > top {
		return rows[:top]
	}
	return rows
}

// Compile-time check.
var _ port.MarketScanFeed = (*Scanner)(nil)

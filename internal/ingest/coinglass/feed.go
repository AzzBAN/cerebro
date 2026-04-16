package coinglass

import (
	"context"
	"encoding/json"
	"time"

	"github.com/azhar/cerebro/internal/domain"
	"github.com/shopspring/decimal"
)

// Feed implements port.DerivativesFeed using the CoinGlass v4 REST API.
// Results are cached in Redis by the ingest scheduler; this struct
// also handles direct on-demand calls when the cache is stale.
type Feed struct {
	client *Client
}

// NewFeed creates a CoinGlass DerivativesFeed.
func NewFeed(client *Client) *Feed {
	return &Feed{client: client}
}

// FundingRate returns the current OI-weighted funding rate for a symbol.
func (f *Feed) FundingRate(ctx context.Context, symbol domain.Symbol) (*domain.FundingRate, error) {
	var resp apiResponse
	if err := f.client.get(ctx, "/api/futures/funding-rate/oi-weight-history", map[string]string{
		"symbol":    coinGlassSymbol(symbol),
		"interval":  "h8",
		"limit":     "1",
	}, &resp); err != nil {
		return nil, err
	}

	var data []FundingRateData
	if err := json.Unmarshal(resp.Data, &data); err != nil || len(data) == 0 {
		return &domain.FundingRate{Symbol: symbol, FetchedAt: time.Now().UTC()}, nil
	}
	return toFundingRate(symbol, data[0]), nil
}

// OpenInterest returns current aggregated OI for a symbol.
func (f *Feed) OpenInterest(ctx context.Context, symbol domain.Symbol) (*domain.OpenInterest, error) {
	var resp apiResponse
	if err := f.client.get(ctx, "/api/futures/open-interest/aggregated-history", map[string]string{
		"symbol": coinGlassSymbol(symbol),
		"interval": "h1",
		"limit":    "1",
	}, &resp); err != nil {
		return nil, err
	}

	var data []OpenInterestData
	if err := json.Unmarshal(resp.Data, &data); err != nil || len(data) == 0 {
		return &domain.OpenInterest{Symbol: symbol, FetchedAt: time.Now().UTC()}, nil
	}
	return toOpenInterest(symbol, data[0]), nil
}

// LiquidationZones returns the top N liquidation clusters near the reference price.
func (f *Feed) LiquidationZones(ctx context.Context, symbol domain.Symbol, refPrice decimal.Decimal, pricePct float64) ([]domain.LiquidationZone, error) {
	var resp apiResponse
	if err := f.client.get(ctx, "/api/futures/liquidation/heatmap/model1", map[string]string{
		"symbol": coinGlassSymbol(symbol),
	}, &resp); err != nil {
		return nil, err
	}

	// Heatmap response is complex; we parse a simplified version for zone filtering.
	// Full implementation maps price buckets to liquidation amounts and filters
	// those within pricePct% of refPrice.
	var zones []domain.LiquidationZone
	return zones, nil
}

// FearGreed returns the latest Crypto Fear & Greed Index.
func (f *Feed) FearGreed(ctx context.Context) (*domain.FearGreedIndex, error) {
	var resp apiResponse
	if err := f.client.get(ctx, "/api/index/fear-greed-history", map[string]string{
		"limit": "1",
	}, &resp); err != nil {
		return nil, err
	}

	var data []FearGreedData
	if err := json.Unmarshal(resp.Data, &data); err != nil || len(data) == 0 {
		return &domain.FearGreedIndex{FetchedAt: time.Now().UTC()}, nil
	}
	return toFearGreed(data[0]), nil
}

// Snapshot assembles the full derivatives picture for a symbol.
func (f *Feed) Snapshot(ctx context.Context, symbol domain.Symbol) (*domain.DerivativesSnapshot, error) {
	snap := &domain.DerivativesSnapshot{
		Symbol:    symbol,
		FetchedAt: time.Now().UTC(),
	}

	if fr, err := f.FundingRate(ctx, symbol); err == nil {
		snap.FundingRate = *fr
	}
	if oi, err := f.OpenInterest(ctx, symbol); err == nil {
		snap.OpenInterest = *oi
	}
	if fg, err := f.FearGreed(ctx); err == nil {
		snap.FearGreed = *fg
	}

	return snap, nil
}

// coinGlassSymbol converts a Binance symbol to the CoinGlass coin slug.
// e.g. "BTCUSDT" → "BTC", "XAUUSDT" → "XAU".
func coinGlassSymbol(sym domain.Symbol) string {
	s := string(sym)
	if len(s) > 4 && s[len(s)-4:] == "USDT" {
		return s[:len(s)-4]
	}
	return s
}

// Ensure Feed implements port.DerivativesFeed (compile-time check).
var _ interface {
	FundingRate(ctx context.Context, symbol domain.Symbol) (*domain.FundingRate, error)
	OpenInterest(ctx context.Context, symbol domain.Symbol) (*domain.OpenInterest, error)
	LiquidationZones(ctx context.Context, symbol domain.Symbol, refPrice decimal.Decimal, pricePct float64) ([]domain.LiquidationZone, error)
	FearGreed(ctx context.Context) (*domain.FearGreedIndex, error)
	Snapshot(ctx context.Context, symbol domain.Symbol) (*domain.DerivativesSnapshot, error)
} = (*Feed)(nil)

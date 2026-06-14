package port

import (
	"context"
)

// MarketScanRow is a single row of a list-style derivatives scan keyed by
// base asset (e.g. "BTC", "ETH"). Different scan endpoints populate
// different fields; consumers should treat unset numerics as zero.
//
// Decoupled from domain.Symbol so the scanner adapter does not need to
// know which venue / quote / contract suffix the consumer canonicalises
// to. The screening layer joins by BaseAsset.
type MarketScanRow struct {
	BaseAsset       string  // e.g. "BTC"
	FundingRate     float64 // current 8h funding (e.g. 0.0001 == 0.01%)
	OpenInterestUSD float64
	OIChange24hPct  float64
	OIChange4hPct   float64
	Liquidations24h float64 // total $ liquidated, both sides combined
	LiqLongRatio    float64 // 0..1; share of liq that was forced longs
	LongShortRatio  float64 // global accounts; >1 = more longs
}

// MarketScanFeed exposes list-style derivatives endpoints. Unlike
// DerivativesFeed (which is per-symbol), these scans return ranked
// snapshots across the whole derivatives universe in a single call.
//
// All methods must be safe for concurrent use. Adapters are expected to
// degrade gracefully on partial failure: returning a shorter list is
// preferred over returning an error, because the screening pipeline is
// best-effort and Coinglass occasionally rate-limits individual
// endpoints while keeping others healthy.
type MarketScanFeed interface {
	// FundingExtremes returns the rows with the highest |funding rate|.
	// `top` caps result length; pass <= 0 for "no cap".
	FundingExtremes(ctx context.Context, top int) ([]MarketScanRow, error)

	// OpenInterestMovers returns the rows with the largest 24h OI growth
	// (positive or negative).
	OpenInterestMovers(ctx context.Context, top int) ([]MarketScanRow, error)

	// LiquidationLeaders returns the rows with the highest 24h liquidation
	// totals.
	LiquidationLeaders(ctx context.Context, top int) ([]MarketScanRow, error)

	// LongShortExtremes returns the rows where the global long/short
	// account ratio is most lopsided in either direction.
	LongShortExtremes(ctx context.Context, top int) ([]MarketScanRow, error)
}

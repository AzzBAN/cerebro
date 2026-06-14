// Package screening turns raw discovery rows + Coinglass scans into
// regime-tagged candidates and concrete TradePlans. It is pure and
// stateless — no I/O, no LLM, no database — so the logic is fully
// table-testable. The only inputs are domain types from
// internal/domain and internal/port.MarketScanRow.
//
// Architecture rationale: keeping this package free of side effects
// means the agent layer can call it both at scheduled cycles and from
// CLI/dry-run tools without re-stubbing dependencies, and the runtime
// gracefully degrades when CoinGlass has no API key (Coinglass-specific
// features are simply zero-valued and the regime classifier falls back
// to price-only heuristics).
package screening

import (
	"github.com/azhar/cerebro/internal/domain"
	"github.com/azhar/cerebro/internal/port"
	"github.com/shopspring/decimal"
)

// Features is the unified per-candidate feature row consumed by the
// regime classifier and the strategy matcher.
//
// Each block is filled best-effort; fields are zero when their data
// source is unavailable. Downstream classifiers must treat zero as
// "missing", not "actually zero", except where explicitly noted.
type Features struct {
	// --- Identity ---
	Symbol    domain.Symbol
	Venue     domain.Venue
	BaseAsset string

	// --- Price / volume (Binance ticker) ---
	LastPrice        decimal.Decimal
	PriceChangePct24 float64
	QuoteVolume24h   decimal.Decimal
	IsNewListing     bool

	// --- Derivatives (Coinglass scan; zero when no API key) ---
	FundingRate     float64 // per 8h, e.g. 0.0001 = 0.01%
	OIChange24hPct  float64
	OIChange4hPct   float64
	Liquidations24h float64 // $
	LiqLongRatio    float64 // 0..1
	LongShortRatio  float64 // >1 = more longs
}

// EnrichFeatures joins a discovery row with the matching Coinglass
// scanner rows by base asset, picking the most informative non-zero
// field from each scan. A nil scanByBase map (or missing entries) is
// fine — features just remain zero.
//
// Inputs are intentionally narrow so the function stays trivially
// testable: no time.Now, no logging, no config.
func EnrichFeatures(
	symbol domain.Symbol,
	venue domain.Venue,
	base string,
	lastPrice decimal.Decimal,
	priceChangePct24 float64,
	quoteVol24h decimal.Decimal,
	isNew bool,
	scanByBase map[string]port.MarketScanRow,
) Features {
	f := Features{
		Symbol:           symbol,
		Venue:            venue,
		BaseAsset:        base,
		LastPrice:        lastPrice,
		PriceChangePct24: priceChangePct24,
		QuoteVolume24h:   quoteVol24h,
		IsNewListing:     isNew,
	}
	if r, ok := scanByBase[base]; ok {
		f.FundingRate = r.FundingRate
		f.OIChange24hPct = r.OIChange24hPct
		f.OIChange4hPct = r.OIChange4hPct
		f.Liquidations24h = r.Liquidations24h
		f.LiqLongRatio = r.LiqLongRatio
		f.LongShortRatio = r.LongShortRatio
	}
	return f
}

// MergeScanRows combines several MarketScanRow slices (one per scan
// endpoint) into a single base-asset → row map, keeping the most
// informative value for each field across slices. Later slices fill
// gaps left by earlier ones.
//
// The merge policy is "first non-zero wins" per field. This way a
// FundingExtremes row that already populated FundingRate is not
// overwritten by an OpenInterestMovers row that only knows OI Δ.
func MergeScanRows(slices ...[]port.MarketScanRow) map[string]port.MarketScanRow {
	out := make(map[string]port.MarketScanRow)
	for _, s := range slices {
		for _, r := range s {
			if r.BaseAsset == "" {
				continue
			}
			cur := out[r.BaseAsset]
			cur.BaseAsset = r.BaseAsset
			if cur.FundingRate == 0 && r.FundingRate != 0 {
				cur.FundingRate = r.FundingRate
			}
			if cur.OpenInterestUSD == 0 && r.OpenInterestUSD != 0 {
				cur.OpenInterestUSD = r.OpenInterestUSD
			}
			if cur.OIChange24hPct == 0 && r.OIChange24hPct != 0 {
				cur.OIChange24hPct = r.OIChange24hPct
			}
			if cur.OIChange4hPct == 0 && r.OIChange4hPct != 0 {
				cur.OIChange4hPct = r.OIChange4hPct
			}
			if cur.Liquidations24h == 0 && r.Liquidations24h != 0 {
				cur.Liquidations24h = r.Liquidations24h
			}
			if cur.LiqLongRatio == 0 && r.LiqLongRatio != 0 {
				cur.LiqLongRatio = r.LiqLongRatio
			}
			if cur.LongShortRatio == 0 && r.LongShortRatio != 0 {
				cur.LongShortRatio = r.LongShortRatio
			}
			out[r.BaseAsset] = cur
		}
	}
	return out
}

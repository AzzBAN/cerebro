package domain

import (
	"time"

	"github.com/shopspring/decimal"
)

// OpenInterest is the aggregate futures open interest for a symbol across all exchanges.
type OpenInterest struct {
	Symbol    Symbol
	TotalUSD  decimal.Decimal
	Change1h  float64
	Change4h  float64
	Change24h float64
	FetchedAt time.Time
}

// FundingRate is the current OI-weighted perpetual funding rate.
type FundingRate struct {
	Symbol          Symbol
	Rate            float64 // e.g. 0.0001 = 0.01% per 8h
	NextFundingTime time.Time
	FetchedAt       time.Time
}

// LongShortRatio measures retail and top-trader directional exposure.
type LongShortRatio struct {
	Symbol      Symbol
	GlobalRatio float64 // >1.0 = more longs than shorts
	TopLongPct  float64
	TopShortPct float64
	FetchedAt   time.Time
}

// LiquidationEvent is a single forced-close event on the derivatives market.
type LiquidationEvent struct {
	Symbol    Symbol
	Side      Side // Buy = shorts liquidated; Sell = longs liquidated
	AmountUSD decimal.Decimal
	Price     decimal.Decimal
	EventTime time.Time
}

// LiquidationZone is a price level with a dense cluster of leveraged positions
// that would be force-closed on touch — a "stop-hunt" magnet.
type LiquidationZone struct {
	PriceLow  decimal.Decimal
	PriceHigh decimal.Decimal
	AmountUSD decimal.Decimal
	Side      Side
}

// TakerDelta measures net aggressive buying vs. selling over an interval.
type TakerDelta struct {
	Symbol    Symbol
	BuyVol    decimal.Decimal
	SellVol   decimal.Decimal
	Delta     decimal.Decimal // BuyVol - SellVol; positive = net buyers
	Interval  string          // "5m", "1h", etc.
	FetchedAt time.Time
}

// CVDSnapshot is the cumulative volume delta for a symbol.
type CVDSnapshot struct {
	Symbol    Symbol
	CVD       decimal.Decimal // positive = net buyers dominate historically
	Change1h  float64
	FetchedAt time.Time
}

// FearGreedIndex is the Crypto Fear & Greed Index (0–100).
type FearGreedIndex struct {
	Value     int    // 0=Extreme Fear, 100=Extreme Greed
	Category  string // "Extreme Fear"|"Fear"|"Neutral"|"Greed"|"Extreme Greed"
	FetchedAt time.Time
}

// FuturesBasis is the spread between futures price and spot price.
type FuturesBasis struct {
	Symbol    Symbol
	BasisPct  float64 // (futures - spot) / spot × 100
	FetchedAt time.Time
}

// DerivativesSnapshot is the full derivatives picture for a symbol,
// assembled from multiple CoinGlass endpoints and cached in Redis with a 5-min TTL.
type DerivativesSnapshot struct {
	Symbol             Symbol
	OpenInterest       OpenInterest
	FundingRate        FundingRate
	LongShortRatio     LongShortRatio
	RecentLiquidations []LiquidationEvent // last 10 significant events
	LiquidationZones   []LiquidationZone  // top-5 zones within 5% of current price
	TakerDelta         TakerDelta         // last 1h window
	CVD                CVDSnapshot
	FearGreed          FearGreedIndex // global BTC-correlated index
	Basis              FuturesBasis
	FetchedAt          time.Time
}

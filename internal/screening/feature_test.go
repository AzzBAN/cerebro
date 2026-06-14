package screening

import (
	"testing"

	"github.com/azhar/cerebro/internal/domain"
	"github.com/azhar/cerebro/internal/port"
	"github.com/shopspring/decimal"
)

func TestMergeScanRows_FirstNonZeroWins(t *testing.T) {
	funding := []port.MarketScanRow{
		{BaseAsset: "BTC", FundingRate: 0.0005, OpenInterestUSD: 10},
	}
	oi := []port.MarketScanRow{
		{BaseAsset: "BTC", OIChange24hPct: 12.0, FundingRate: 0.9}, // funding should NOT overwrite
		{BaseAsset: "ETH", OIChange24hPct: 4.0},
	}

	merged := MergeScanRows(funding, oi)
	if got := merged["BTC"].FundingRate; got != 0.0005 {
		t.Errorf("BTC funding: want 0.0005 (first wins), got %f", got)
	}
	if got := merged["BTC"].OIChange24hPct; got != 12.0 {
		t.Errorf("BTC oi24h: want 12.0 (filled by 2nd slice), got %f", got)
	}
	if _, ok := merged["ETH"]; !ok {
		t.Error("ETH row should be present")
	}
}

func TestEnrichFeatures_JoinsByBaseAsset(t *testing.T) {
	scan := map[string]port.MarketScanRow{
		"BTC": {BaseAsset: "BTC", FundingRate: 0.001, LongShortRatio: 1.4},
	}
	f := EnrichFeatures(
		"BTCUSDT",
		domain.VenueBinanceFutures,
		"BTC",
		decimal.NewFromInt(60_000),
		5.0,
		decimal.NewFromInt(1_000_000_000),
		false,
		scan,
	)
	if f.FundingRate != 0.001 {
		t.Errorf("funding not joined: got %f", f.FundingRate)
	}
	if f.LongShortRatio != 1.4 {
		t.Errorf("L/S not joined: got %f", f.LongShortRatio)
	}
	if f.Symbol != "BTCUSDT" {
		t.Errorf("symbol pass-through broke: %s", f.Symbol)
	}
}

func TestEnrichFeatures_MissingBaseLeavesZeros(t *testing.T) {
	f := EnrichFeatures(
		"WIFUSDT", domain.VenueBinanceFutures, "WIF",
		decimal.NewFromFloat(2.5), 9.0, decimal.NewFromInt(50_000_000),
		false, map[string]port.MarketScanRow{"BTC": {BaseAsset: "BTC"}},
	)
	if f.FundingRate != 0 {
		t.Errorf("missing scan row should leave funding zero, got %f", f.FundingRate)
	}
	if f.PriceChangePct24 != 9.0 {
		t.Errorf("price change should still flow through: got %f", f.PriceChangePct24)
	}
}

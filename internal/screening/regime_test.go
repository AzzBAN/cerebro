package screening

import (
	"testing"

	"github.com/azhar/cerebro/internal/domain"
	"github.com/shopspring/decimal"
)

func makeFeatures() Features {
	return Features{
		Symbol:    "BTCUSDT",
		Venue:     domain.VenueBinanceFutures,
		BaseAsset: "BTC",
		LastPrice: decimal.NewFromInt(60_000),
	}
}

func TestClassify_Trending(t *testing.T) {
	f := makeFeatures()
	f.PriceChangePct24 = 7.0
	f.OIChange24hPct = 5.0
	f.FundingRate = 0.02 // neutral

	r, side := Classify(f, DefaultThresholds())
	if r != domain.RegimeTrending {
		t.Fatalf("regime: want trending, got %s", r)
	}
	if side != domain.SideBuy {
		t.Errorf("side: want buy, got %s", side)
	}
}

func TestClassify_Squeeze_FadesCrowdedLongs(t *testing.T) {
	f := makeFeatures()
	f.FundingRate = 0.12 // extreme positive funding
	f.LongShortRatio = 2.0
	f.PriceChangePct24 = 1.0

	r, side := Classify(f, DefaultThresholds())
	if r != domain.RegimeSqueeze {
		t.Fatalf("regime: want squeeze, got %s", r)
	}
	if side != domain.SideSell {
		t.Errorf("crowded longs should imply SELL, got %s", side)
	}
}

func TestClassify_Breakout_RequiresAlignedOI(t *testing.T) {
	f := makeFeatures()
	f.PriceChangePct24 = 3.0
	f.OIChange4hPct = 5.0 // confirming up

	r, side := Classify(f, DefaultThresholds())
	if r != domain.RegimeBreakout {
		t.Fatalf("regime: want breakout, got %s", r)
	}
	if side != domain.SideBuy {
		t.Errorf("side: want buy, got %s", side)
	}
}

func TestClassify_Range_FadesRecentDirection(t *testing.T) {
	f := makeFeatures()
	f.PriceChangePct24 = 0.4 // very quiet, slightly up

	r, side := Classify(f, DefaultThresholds())
	if r != domain.RegimeRange {
		t.Fatalf("regime: want range, got %s", r)
	}
	// quiet up-move → mean-revert short
	if side != domain.SideSell {
		t.Errorf("range fade should be SELL when price up, got %s", side)
	}
}

func TestClassify_LiqHunt_PreemptsOtherRegimes(t *testing.T) {
	f := makeFeatures()
	f.Liquidations24h = 50_000_000
	f.LiqLongRatio = 0.8 // longs got rekt
	f.PriceChangePct24 = 7.0
	f.OIChange24hPct = 5.0

	r, side := Classify(f, DefaultThresholds())
	if r != domain.RegimeLiqHunt {
		t.Fatalf("regime: want liq_hunt, got %s", r)
	}
	if side != domain.SideSell {
		t.Errorf("longs liquidated should imply SELL, got %s", side)
	}
}

func TestClassify_PriceOnlyFallback_TrendsWithoutDerivatives(t *testing.T) {
	// Demo mode: no CoinGlass scan rows → all derivatives fields zero.
	f := makeFeatures()
	f.PriceChangePct24 = 6.0 // above StrongMovePct
	// All OI / funding / L/S fields stay zero.

	r, side := Classify(f, DefaultThresholds())
	if r != domain.RegimeTrending {
		t.Errorf("price-only fallback: want trending when |Δ24h| ≥ StrongMovePct, got %s", r)
	}
	if side != domain.SideBuy {
		t.Errorf("side: want buy, got %s", side)
	}
}

func TestClassify_Unknown_WhenNoSignal(t *testing.T) {
	f := makeFeatures()
	f.PriceChangePct24 = 3.0 // moderate but no OI confirm
	r, _ := Classify(f, DefaultThresholds())
	if r != domain.RegimeUnknown {
		t.Errorf("regime: want unknown, got %s", r)
	}
}

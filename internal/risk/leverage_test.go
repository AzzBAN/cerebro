package risk

import (
	"testing"

	"github.com/azhar/cerebro/internal/domain"
	"github.com/shopspring/decimal"
)

func TestCalculateSafeLeverage_Long(t *testing.T) {
	// Entry: 100, StopLoss: 98 (2% distance)
	// Equity: 1000 USDT, Risk: 1% (10 USDT risk)
	// Qty = 10 / 2 = 5 units. Notional = 500 USDT.
	// MMR = 0.005 (0.5%)
	// Safe leverage must ensure LiqPrice <= 97 (at least 1.5x SL distance from entry).
	entry := decimal.NewFromInt(100)
	sl := decimal.NewFromInt(98)
	equity := decimal.NewFromInt(1000)
	riskPct := 1.0
	mmr := decimal.NewFromFloat(0.005)

	res, err := CalculateSafeLeverage(entry, sl, domain.SideBuy, equity, riskPct, mmr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if res.MaxSafeLeverage <= 0 {
		t.Fatalf("expected positive MaxSafeLeverage, got %d", res.MaxSafeLeverage)
	}
	// For 2% SL with 1.5x buffer (3%) + 0.5% MMR = 3.5% adverse move => max safe lev <= 1 / 0.035 = 28x
	if res.MaxSafeLeverage > 28 {
		t.Errorf("MaxSafeLeverage = %d; want <= 28", res.MaxSafeLeverage)
	}
	if res.LiquidationPrice.GreaterThanOrEqual(sl) {
		t.Errorf("LiquidationPrice (%s) must be below stop loss (%s)", res.LiquidationPrice, sl)
	}
	if res.RecommendedLeverage <= 0 || res.RecommendedLeverage > res.MaxSafeLeverage {
		t.Errorf("RecommendedLeverage = %d; want between 1 and %d", res.RecommendedLeverage, res.MaxSafeLeverage)
	}
}

func TestCalculateSafeLeverage_Short(t *testing.T) {
	// Entry: 100, StopLoss: 102 (2% distance)
	entry := decimal.NewFromInt(100)
	sl := decimal.NewFromInt(102)
	equity := decimal.NewFromInt(1000)
	riskPct := 1.0
	mmr := decimal.NewFromFloat(0.005)

	res, err := CalculateSafeLeverage(entry, sl, domain.SideSell, equity, riskPct, mmr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if res.LiquidationPrice.LessThanOrEqual(sl) {
		t.Errorf("LiquidationPrice (%s) must be above stop loss (%s) for short", res.LiquidationPrice, sl)
	}
}

func TestCalculateSafeLeverage_InvalidInputs(t *testing.T) {
	entry := decimal.NewFromInt(100)
	sl := decimal.NewFromInt(98)
	equity := decimal.NewFromInt(1000)

	if _, err := CalculateSafeLeverage(decimal.Zero, sl, domain.SideBuy, equity, 1.0, decimal.Zero); err == nil {
		t.Error("expected error on zero entry price")
	}
	if _, err := CalculateSafeLeverage(entry, decimal.Zero, domain.SideBuy, equity, 1.0, decimal.Zero); err == nil {
		t.Error("expected error on zero stop loss")
	}
	if _, err := CalculateSafeLeverage(entry, sl, domain.SideBuy, decimal.Zero, 1.0, decimal.Zero); err == nil {
		t.Error("expected error on zero equity")
	}
	if _, err := CalculateSafeLeverage(entry, sl, domain.SideBuy, equity, -1.0, decimal.Zero); err == nil {
		t.Error("expected error on negative risk pct")
	}
	if _, err := CalculateSafeLeverage(entry, entry, domain.SideBuy, equity, 1.0, decimal.Zero); err == nil {
		t.Error("expected error on zero stop loss distance")
	}
}

package indicators

import (
	"testing"

	"github.com/shopspring/decimal"
)

func TestRSI_NotReady(t *testing.T) {
	rsi := NewRSI(14)
	rsi.Add(decimal.NewFromInt(100))

	_, ready := rsi.Value()
	if ready {
		t.Error("RSI should not be ready with insufficient data")
	}
}

func TestRSI_AllGains(t *testing.T) {
	period := 5
	rsi := NewRSI(period)

	// Feed strictly increasing prices.
	for i := 1; i <= period+1; i++ {
		rsi.Add(decimal.NewFromInt(int64(i * 10)))
	}

	val, ready := rsi.Value()
	if !ready {
		t.Fatal("RSI should be ready")
	}
	// All gains, zero losses → RSI = 100
	if !val.Equal(decimal.NewFromInt(100)) {
		t.Errorf("RSI = %s, want 100", val.Round(2))
	}
}

func TestRSI_MixedPrices(t *testing.T) {
	rsi := NewRSI(14)

	// Feed a known sequence of 15 prices (14 changes).
	prices := []float64{
		44, 44.34, 44.09, 43.61, 44.33, 44.83, 45.10, 45.42,
		45.84, 46.08, 45.89, 46.03, 45.61, 46.28, 46.28, 46.00,
	}
	for _, p := range prices {
		rsi.Add(decimal.NewFromFloat(p))
	}

	val, ready := rsi.Value()
	if !ready {
		t.Fatal("RSI should be ready")
	}
	// Wilder's RSI for this sequence should be in the 65-75 range.
	// Exact value depends on decimal vs float arithmetic.
	f64, _ := val.Float64()
	if f64 < 60 || f64 > 80 {
		t.Errorf("RSI = %.2f, want reasonable range [60, 80]", f64)
	}
}

func TestRSI_IsOversold(t *testing.T) {
	rsi := NewRSI(3)

	// Declining prices → RSI drops.
	prices := []float64{100, 90, 80, 70}
	for _, p := range prices {
		rsi.Add(decimal.NewFromFloat(p))
	}

	if !rsi.IsOversold(30) {
		t.Error("expected oversold")
	}
}

func TestRSI_IsOverbought(t *testing.T) {
	rsi := NewRSI(3)

	prices := []float64{70, 80, 90, 100}
	for _, p := range prices {
		rsi.Add(decimal.NewFromFloat(p))
	}

	if !rsi.IsOverbought(70) {
		t.Error("expected overbought")
	}
}

func TestRSI_IsOversold_NotReady(t *testing.T) {
	rsi := NewRSI(14)
	rsi.Add(decimal.NewFromInt(10))

	if rsi.IsOversold(30) {
		t.Error("should not report oversold when not ready")
	}
}

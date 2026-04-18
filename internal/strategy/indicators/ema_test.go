package indicators

import (
	"testing"

	"github.com/shopspring/decimal"
)

func TestEMA_NotReady(t *testing.T) {
	ema := NewEMA(3)
	ema.Add(decimal.NewFromInt(10))

	_, ready := ema.Value()
	if ready {
		t.Error("EMA should not be ready with fewer than period values")
	}
}

func TestEMA_SMASeed(t *testing.T) {
	ema := NewEMA(3)
	ema.Add(decimal.NewFromInt(10))
	ema.Add(decimal.NewFromInt(20))
	ema.Add(decimal.NewFromInt(30))

	val, ready := ema.Value()
	if !ready {
		t.Fatal("EMA should be ready after period values")
	}
	// SMA seed = (10 + 20 + 30) / 3 = 20
	if !val.Equal(decimal.NewFromInt(20)) {
		t.Errorf("SMA seed = %s, want 20", val)
	}
}

func TestEMA_SubsequentValues(t *testing.T) {
	ema := NewEMA(3)
	prices := []int{10, 20, 30, 40}
	for _, p := range prices {
		ema.Add(decimal.NewFromInt(int64(p)))
	}

	val, _ := ema.Value()
	// k = 2/(3+1) = 0.5
	// After SMA seed = 20, next EMA = 40*0.5 + 20*0.5 = 30
	want := decimal.NewFromInt(30)
	if !val.Equal(want) {
		t.Errorf("EMA = %s, want %s", val, want)
	}
}

func TestCrossOver(t *testing.T) {
	fast := NewEMA(2)
	slow := NewEMA(3)

	// Feed prices so fast rises above slow.
	prices := []int{10, 10, 10, 20, 30}
	for _, p := range prices {
		fast.Add(decimal.NewFromInt(int64(p)))
		slow.Add(decimal.NewFromInt(int64(p)))
	}

	if !CrossOver(fast, slow) {
		t.Error("expected fast to cross above slow")
	}
	if CrossUnder(fast, slow) {
		t.Error("should not cross under when fast > slow")
	}
}

func TestCrossUnder(t *testing.T) {
	fast := NewEMA(2)
	slow := NewEMA(3)

	prices := []int{30, 30, 30, 20, 10}
	for _, p := range prices {
		fast.Add(decimal.NewFromInt(int64(p)))
		slow.Add(decimal.NewFromInt(int64(p)))
	}

	if !CrossUnder(fast, slow) {
		t.Error("expected fast to cross below slow")
	}
}

func TestCrossOver_NotReady(t *testing.T) {
	fast := NewEMA(2)
	slow := NewEMA(3)
	fast.Add(decimal.NewFromInt(10))

	if CrossOver(fast, slow) {
		t.Error("should not cross over when not ready")
	}
}

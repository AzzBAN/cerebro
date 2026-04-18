package indicators

import (
	"math"
	"testing"

	"github.com/shopspring/decimal"
)

func TestBollinger_NotReady(t *testing.T) {
	bb := NewBollinger(3, 2)
	bb.Add(decimal.NewFromInt(10))
	bb.Add(decimal.NewFromInt(20))

	_, _, _, ready := bb.Bands()
	if ready {
		t.Error("Bollinger should not be ready with fewer than period prices")
	}
}

func TestBollinger_Bands(t *testing.T) {
	bb := NewBollinger(3, 2)
	bb.Add(decimal.NewFromInt(10))
	bb.Add(decimal.NewFromInt(20))
	bb.Add(decimal.NewFromInt(30))

	upper, middle, lower, ready := bb.Bands()
	if !ready {
		t.Fatal("Bollinger should be ready")
	}

	// SMA = 20
	if !middle.Equal(decimal.NewFromInt(20)) {
		t.Errorf("middle = %s, want 20", middle)
	}

	// StdDev of [10,20,30]: variance = ((10-20)^2 + (20-20)^2 + (30-20)^2)/3 = 200/3 ≈ 66.67
	// SD = sqrt(66.67) ≈ 8.165
	// Upper = 20 + 2*8.165 ≈ 36.33
	// Lower = 20 - 2*8.165 ≈ 3.67
	upperF, _ := upper.Float64()
	lowerF, _ := lower.Float64()

	if math.Abs(upperF-36.33) > 0.5 {
		t.Errorf("upper = %.2f, want ≈36.33", upperF)
	}
	if math.Abs(lowerF-3.67) > 0.5 {
		t.Errorf("lower = %.2f, want ≈3.67", lowerF)
	}
}

func TestBollinger_RollingWindow(t *testing.T) {
	bb := NewBollinger(3, 2)
	bb.Add(decimal.NewFromInt(10))
	bb.Add(decimal.NewFromInt(20))
	bb.Add(decimal.NewFromInt(30))
	bb.Add(decimal.NewFromInt(40)) // window slides: [20, 30, 40]

	_, middle, _, _ := bb.Bands()
	// SMA = (20 + 30 + 40) / 3 = 30
	if !middle.Equal(decimal.NewFromInt(30)) {
		t.Errorf("middle after slide = %s, want 30", middle)
	}
}

func TestBollinger_IsBelowLower(t *testing.T) {
	bb := NewBollinger(3, 2)
	bb.Add(decimal.NewFromInt(10))
	bb.Add(decimal.NewFromInt(10))
	bb.Add(decimal.NewFromInt(10))

	// Constant prices → SD = 0, upper = lower = middle = 10
	// Price 9 is below lower band (10).
	if !bb.IsBelowLower(decimal.NewFromInt(9)) {
		t.Error("9 should be below lower band of 10")
	}
	if bb.IsBelowLower(decimal.NewFromInt(10)) {
		t.Error("10 should not be below lower band (equal, not less)")
	}
}

func TestBollinger_IsAboveUpper(t *testing.T) {
	bb := NewBollinger(3, 2)
	bb.Add(decimal.NewFromInt(10))
	bb.Add(decimal.NewFromInt(10))
	bb.Add(decimal.NewFromInt(10))

	if !bb.IsAboveUpper(decimal.NewFromInt(11)) {
		t.Error("11 should be above upper band of 10")
	}
	if bb.IsAboveUpper(decimal.NewFromInt(10)) {
		t.Error("10 should not be above upper band (equal)")
	}
}

func TestBollinger_IsMethods_NotReady(t *testing.T) {
	bb := NewBollinger(5, 2)
	bb.Add(decimal.NewFromInt(10))

	if bb.IsBelowLower(decimal.NewFromInt(0)) {
		t.Error("should not detect below lower when not ready")
	}
	if bb.IsAboveUpper(decimal.NewFromInt(99999)) {
		t.Error("should not detect above upper when not ready")
	}
}

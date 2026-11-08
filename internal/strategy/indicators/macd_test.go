package indicators

import (
	"testing"

	"github.com/shopspring/decimal"
)

func TestMACD_NotReady(t *testing.T) {
	macd := NewMACD(12, 26, 9)
	for range 10 {
		macd.Add(decimal.NewFromInt(100))
	}

	_, _, _, ready := macd.Value()
	if ready {
		t.Fatal("MACD should not be ready with insufficient candles")
	}
}

func TestMACD_Readiness(t *testing.T) {
	// fast=3, slow=5, signal=2
	// Slow EMA is ready after 5 candles (1st signal EMA input).
	// Signal EMA is ready after 2nd input (6th candle).
	macd := NewMACD(3, 5, 2)

	for range 5 {
		macd.Add(decimal.NewFromInt(50))
	}
	_, _, _, ready := macd.Value()
	if ready {
		t.Fatal("MACD should not be ready at 5 candles")
	}

	macd.Add(decimal.NewFromInt(50))
	_, _, _, ready = macd.Value()
	if !ready {
		t.Fatal("MACD should be ready at 6 candles")
	}
}

func TestMACD_ConstantPrice(t *testing.T) {
	macd := NewMACD(3, 5, 2)
	for range 10 {
		macd.Add(decimal.NewFromInt(100))
	}

	m, s, h, ready := macd.Value()
	if !ready {
		t.Fatal("expected ready")
	}
	if !m.IsZero() {
		t.Errorf("expected MACD=0 for constant price, got %s", m)
	}
	if !s.IsZero() {
		t.Errorf("expected signal=0 for constant price, got %s", s)
	}
	if !h.IsZero() {
		t.Errorf("expected hist=0 for constant price, got %s", h)
	}
}

func TestMACD_TrendingPrices(t *testing.T) {
	macd := NewMACD(3, 5, 2)
	// Upward trend: fast EMA rises faster than slow EMA, MACD line positive
	for i := range 15 {
		macd.Add(decimal.NewFromInt(int64(100 + i*10)))
	}

	m, s, h, ready := macd.Value()
	if !ready {
		t.Fatal("expected ready")
	}
	if !m.IsPositive() {
		t.Errorf("expected positive MACD in strong uptrend, got %s", m)
	}
	expectedHist := m.Sub(s)
	if !h.Equal(expectedHist) {
		t.Errorf("expected hist = macd - signal (%s), got %s", expectedHist, h)
	}
}

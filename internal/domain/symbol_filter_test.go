package domain

import (
	"errors"
	"testing"

	"github.com/shopspring/decimal"
)

func dec(s string) decimal.Decimal {
	d, err := decimal.NewFromString(s)
	if err != nil {
		panic(err)
	}
	return d
}

func TestSymbolFilter_QuantiseQty(t *testing.T) {
	tests := []struct {
		name string
		step string
		in   string
		want string
	}{
		{"floors to step", "0.001", "0.123456", "0.123"},
		{"exact multiple unchanged", "0.01", "1.50", "1.5"},
		{"zero step is passthrough", "0", "0.123456", "0.123456"},
		{"zero in stays zero", "0.001", "0", "0"},
		{"sub-step rounds down to zero", "1", "0.5", "0"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := SymbolFilter{StepSize: dec(tt.step)}
			got := f.QuantiseQty(dec(tt.in))
			if !got.Equal(dec(tt.want)) {
				t.Errorf("QuantiseQty(%s, step=%s) = %s, want %s",
					tt.in, tt.step, got, tt.want)
			}
		})
	}
}

func TestSymbolFilter_QuantisePrice_Direction(t *testing.T) {
	f := SymbolFilter{TickSize: dec("0.1")}
	// BUY prices round down so we don't overpay past the tick.
	if got := f.QuantisePrice(dec("100.17"), SideBuy); !got.Equal(dec("100.1")) {
		t.Errorf("buy quantise: got %s, want 100.1", got)
	}
	// SELL prices round up so we don't undersell past the tick.
	if got := f.QuantisePrice(dec("100.11"), SideSell); !got.Equal(dec("100.2")) {
		t.Errorf("sell quantise: got %s, want 100.2", got)
	}
}

func TestSymbolFilter_Validate(t *testing.T) {
	f := SymbolFilter{
		MinQty:      dec("0.001"),
		MaxQty:      dec("1000"),
		MinNotional: dec("5"),
	}
	tests := []struct {
		name   string
		qty    string
		price  string
		wantEq error
	}{
		{"valid", "1", "10", nil},
		{"below min qty", "0.0001", "10", ErrOrderBelowMinQty},
		{"above max qty", "2000", "10", ErrOrderAboveMaxQty},
		{"below min notional", "0.01", "10", ErrOrderBelowMinNotional},
		{"market order (zero price) skips notional", "0.01", "0", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := f.Validate(dec(tt.qty), dec(tt.price))
			if !errors.Is(err, tt.wantEq) {
				t.Errorf("Validate(%s @ %s) = %v, want %v", tt.qty, tt.price, err, tt.wantEq)
			}
		})
	}
}

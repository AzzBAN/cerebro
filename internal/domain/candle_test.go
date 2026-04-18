package domain

import (
	"testing"

	"github.com/shopspring/decimal"
)

func TestQuote_SpreadPct(t *testing.T) {
	tests := []struct {
		name       string
		q          Quote
		wantSpread decimal.Decimal
	}{
		{
			name: "normal spread",
			q: Quote{
				Bid: decimal.NewFromInt(100),
				Ask: decimal.NewFromInt(101),
				Mid: decimal.NewFromFloat(100.5),
			},
			wantSpread: decimal.NewFromInt(101).Sub(decimal.NewFromInt(100)).
				Div(decimal.NewFromFloat(100.5)).Mul(decimal.NewFromInt(100)),
		},
		{
			name: "zero mid returns zero",
			q: Quote{
				Bid: decimal.NewFromInt(100),
				Ask: decimal.NewFromInt(101),
				Mid: decimal.Zero,
			},
			wantSpread: decimal.Zero,
		},
		{
			name: "tight spread",
			q: Quote{
				Bid: decimal.NewFromInt(50000),
				Ask: decimal.NewFromInt(50001),
				Mid: decimal.NewFromInt(50000),
			},
			wantSpread: decimal.NewFromInt(1).Div(decimal.NewFromInt(50000)).Mul(decimal.NewFromInt(100)),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.q.SpreadPct()
			if !got.Equal(tt.wantSpread) {
				t.Errorf("SpreadPct() = %s, want %s", got.String(), tt.wantSpread.String())
			}
		})
	}
}

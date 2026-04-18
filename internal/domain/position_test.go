package domain

import (
	"testing"

	"github.com/shopspring/decimal"
)

func TestPosition_UnrealizedPnL(t *testing.T) {
	tests := []struct {
		name string
		p    Position
		want string
	}{
		{
			name: "buy profit",
			p: Position{
				Side:         SideBuy,
				Quantity:     decimal.NewFromInt(2),
				EntryPrice:   decimal.NewFromInt(100),
				CurrentPrice: decimal.NewFromInt(110),
			},
			want: "20",
		},
		{
			name: "buy loss",
			p: Position{
				Side:         SideBuy,
				Quantity:     decimal.NewFromInt(1),
				EntryPrice:   decimal.NewFromInt(200),
				CurrentPrice: decimal.NewFromInt(180),
			},
			want: "-20",
		},
		{
			name: "sell profit",
			p: Position{
				Side:         SideSell,
				Quantity:     decimal.NewFromInt(3),
				EntryPrice:   decimal.NewFromInt(50),
				CurrentPrice: decimal.NewFromInt(40),
			},
			want: "30",
		},
		{
			name: "sell loss",
			p: Position{
				Side:         SideSell,
				Quantity:     decimal.NewFromInt(1),
				EntryPrice:   decimal.NewFromInt(100),
				CurrentPrice: decimal.NewFromInt(120),
			},
			want: "-20",
		},
		{
			name: "breakeven",
			p: Position{
				Side:         SideBuy,
				Quantity:     decimal.NewFromInt(5),
				EntryPrice:   decimal.NewFromInt(500),
				CurrentPrice: decimal.NewFromInt(500),
			},
			want: "0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.p.UnrealizedPnL()
			want := decimal.RequireFromString(tt.want)
			if !got.Equal(want) {
				t.Errorf("UnrealizedPnL() = %s, want %s", got.String(), tt.want)
			}
		})
	}
}

func TestPosition_UnrealizedPnLPct(t *testing.T) {
	tests := []struct {
		name string
		p    Position
		want string
	}{
		{
			name: "buy 10% profit",
			p: Position{
				Side:         SideBuy,
				Quantity:     decimal.NewFromInt(1),
				EntryPrice:   decimal.NewFromInt(100),
				CurrentPrice: decimal.NewFromInt(110),
			},
			want: "10",
		},
		{
			name: "buy 5% loss",
			p: Position{
				Side:         SideBuy,
				Quantity:     decimal.NewFromInt(2),
				EntryPrice:   decimal.NewFromInt(200),
				CurrentPrice: decimal.NewFromInt(190),
			},
			want: "-5",
		},
		{
			name: "zero entry returns zero",
			p: Position{
				Side:         SideBuy,
				Quantity:     decimal.NewFromInt(1),
				EntryPrice:   decimal.Zero,
				CurrentPrice: decimal.NewFromInt(100),
			},
			want: "0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.p.UnrealizedPnLPct()
			want := decimal.RequireFromString(tt.want)
			if !got.Equal(want) {
				t.Errorf("UnrealizedPnLPct() = %s, want %s", got.String(), tt.want)
			}
		})
	}
}

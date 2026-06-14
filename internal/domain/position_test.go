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

func TestPosition_UnrealizedPnLROI(t *testing.T) {
	tests := []struct {
		name string
		p    Position
		want string
	}{
		{
			name: "no leverage falls back to price pct",
			p: Position{
				Side:         SideBuy,
				Quantity:     decimal.NewFromInt(1),
				EntryPrice:   decimal.NewFromInt(100),
				CurrentPrice: decimal.NewFromInt(110),
			},
			want: "10",
		},
		{
			name: "leverage 1 equals price pct",
			p: Position{
				Side:         SideBuy,
				Quantity:     decimal.NewFromInt(1),
				EntryPrice:   decimal.NewFromInt(100),
				CurrentPrice: decimal.NewFromInt(110),
				Leverage:     1,
			},
			want: "10",
		},
		{
			name: "10x amplifies 1% move to 10% ROI",
			p: Position{
				Side:         SideBuy,
				Quantity:     decimal.NewFromInt(1),
				EntryPrice:   decimal.NewFromInt(100),
				CurrentPrice: decimal.NewFromInt(101),
				Leverage:     10,
			},
			want: "10",
		},
		{
			name: "125x short loss ROI",
			// 1% adverse move on 125x short = -125% ROI
			p: Position{
				Side:         SideSell,
				Quantity:     decimal.NewFromInt(1),
				EntryPrice:   decimal.NewFromInt(100),
				CurrentPrice: decimal.NewFromInt(101),
				Leverage:     125,
			},
			want: "-125",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.p.UnrealizedPnLROI()
			want := decimal.RequireFromString(tt.want)
			if !got.Equal(want) {
				t.Errorf("UnrealizedPnLROI() = %s, want %s", got.String(), tt.want)
			}
		})
	}
}

func TestPosition_InitialMargin(t *testing.T) {
	tests := []struct {
		name string
		p    Position
		want string
	}{
		{
			name: "spot equals notional",
			p: Position{
				Quantity:   decimal.RequireFromString("0.5"),
				EntryPrice: decimal.RequireFromString("100"),
				Leverage:   1,
			},
			want: "50",
		},
		{
			name: "leverage zero treated as spot",
			p: Position{
				Quantity:   decimal.RequireFromString("0.5"),
				EntryPrice: decimal.RequireFromString("100"),
			},
			want: "50",
		},
		{
			name: "125x BTC matches Binance display",
			// Reproduces the screenshot in TUI: 0.033 BTC @ 77325.80 / 125x
			// → notional 2551.7514, initial margin ~20.4140
			p: Position{
				Quantity:   decimal.RequireFromString("0.033"),
				EntryPrice: decimal.RequireFromString("77325.80"),
				Leverage:   125,
			},
			want: "20.4140112",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.p.InitialMargin()
			want := decimal.RequireFromString(tt.want)
			if !got.Equal(want) {
				t.Errorf("InitialMargin() = %s, want %s", got.String(), tt.want)
			}
		})
	}
}

func TestPosition_EffectiveMargin(t *testing.T) {
	t.Run("returns reported margin when set", func(t *testing.T) {
		p := Position{
			Quantity:   decimal.RequireFromString("0.033"),
			EntryPrice: decimal.RequireFromString("77325.80"),
			Leverage:   125,
			Margin:     decimal.RequireFromString("50.00"), // user posted extra in isolated mode
			Isolated:   true,
		}
		got := p.EffectiveMargin()
		if !got.Equal(decimal.RequireFromString("50.00")) {
			t.Errorf("EffectiveMargin() = %s, want 50.00", got.String())
		}
	})

	t.Run("falls back to initial margin when unreported", func(t *testing.T) {
		p := Position{
			Quantity:   decimal.RequireFromString("0.033"),
			EntryPrice: decimal.RequireFromString("77325.80"),
			Leverage:   125,
			// Margin unset (zero)
		}
		got := p.EffectiveMargin()
		want := p.InitialMargin()
		if !got.Equal(want) {
			t.Errorf("EffectiveMargin() = %s, want %s", got.String(), want.String())
		}
	})

	t.Run("ignores negative reported margin", func(t *testing.T) {
		p := Position{
			Quantity:   decimal.RequireFromString("1"),
			EntryPrice: decimal.RequireFromString("100"),
			Leverage:   10,
			Margin:     decimal.RequireFromString("-1"),
		}
		got := p.EffectiveMargin()
		want := p.InitialMargin()
		if !got.Equal(want) {
			t.Errorf("EffectiveMargin() with negative Margin should fall back, got %s want %s", got.String(), want.String())
		}
	})
}

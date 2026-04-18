package execution

import (
	"testing"

	"github.com/azhar/cerebro/internal/domain"
	"github.com/shopspring/decimal"
)

func TestMonitor_isStopHit(t *testing.T) {
	m := &Monitor{}

	tests := []struct {
		name         string
		side         domain.Side
		stopLoss     decimal.Decimal
		currentPrice decimal.Decimal
		want         bool
	}{
		{
			name:         "buy SL hit — price drops below",
			side:         domain.SideBuy,
			stopLoss:     decimal.NewFromInt(100),
			currentPrice: decimal.NewFromInt(99),
			want:         true,
		},
		{
			name:         "buy SL not hit — price above",
			side:         domain.SideBuy,
			stopLoss:     decimal.NewFromInt(100),
			currentPrice: decimal.NewFromInt(105),
			want:         false,
		},
		{
			name:         "buy SL hit at exactly SL price",
			side:         domain.SideBuy,
			stopLoss:     decimal.NewFromInt(100),
			currentPrice: decimal.NewFromInt(100),
			want:         true,
		},
		{
			name:         "sell SL hit — price rises above",
			side:         domain.SideSell,
			stopLoss:     decimal.NewFromInt(100),
			currentPrice: decimal.NewFromInt(101),
			want:         true,
		},
		{
			name:         "sell SL not hit — price below",
			side:         domain.SideSell,
			stopLoss:     decimal.NewFromInt(100),
			currentPrice: decimal.NewFromInt(95),
			want:         false,
		},
		{
			name:         "zero stop loss — never hit",
			side:         domain.SideBuy,
			stopLoss:     decimal.Zero,
			currentPrice: decimal.NewFromInt(50),
			want:         false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pos := domain.Position{
				Side:     tt.side,
				StopLoss: tt.stopLoss,
			}
			got := m.isStopHit(pos, tt.currentPrice)
			if got != tt.want {
				t.Errorf("isStopHit() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMonitor_isTP1Hit(t *testing.T) {
	m := &Monitor{}

	tests := []struct {
		name         string
		side         domain.Side
		tp1          decimal.Decimal
		currentPrice decimal.Decimal
		want         bool
	}{
		{
			name:         "buy TP hit — price exceeds TP",
			side:         domain.SideBuy,
			tp1:          decimal.NewFromInt(110),
			currentPrice: decimal.NewFromInt(115),
			want:         true,
		},
		{
			name:         "buy TP at exact price",
			side:         domain.SideBuy,
			tp1:          decimal.NewFromInt(110),
			currentPrice: decimal.NewFromInt(110),
			want:         true,
		},
		{
			name:         "buy TP not hit",
			side:         domain.SideBuy,
			tp1:          decimal.NewFromInt(110),
			currentPrice: decimal.NewFromInt(105),
			want:         false,
		},
		{
			name:         "sell TP hit — price drops below TP",
			side:         domain.SideSell,
			tp1:          decimal.NewFromInt(90),
			currentPrice: decimal.NewFromInt(85),
			want:         true,
		},
		{
			name:         "sell TP not hit",
			side:         domain.SideSell,
			tp1:          decimal.NewFromInt(90),
			currentPrice: decimal.NewFromInt(95),
			want:         false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pos := domain.Position{
				Side:        tt.side,
				TakeProfit1: tt.tp1,
			}
			got := m.isTP1Hit(pos, tt.currentPrice)
			if got != tt.want {
				t.Errorf("isTP1Hit() = %v, want %v", got, tt.want)
			}
		})
	}
}

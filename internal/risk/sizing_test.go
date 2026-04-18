package risk

import (
	"testing"

	"github.com/shopspring/decimal"
)

func TestCalculatePositionSize(t *testing.T) {
	tests := []struct {
		name            string
		equity          decimal.Decimal
		riskPct         float64
		entry           decimal.Decimal
		stopLoss        decimal.Decimal
		minLot          decimal.Decimal
		maxLot          decimal.Decimal
		minNotional     decimal.Decimal
		wantQty         string
		wantRiskAmount  string
		wantErr         bool
		errContains     string
	}{
		{
			name:        "basic long position",
			equity:      decimal.NewFromInt(10000),
			riskPct:     1,
			entry:       decimal.NewFromInt(50000),
			stopLoss:    decimal.NewFromInt(49000),
			minLot:      decimal.Zero,
			maxLot:      decimal.Zero,
			minNotional: decimal.Zero,
			wantQty:     "0.1",
			wantRiskAmount: "100",
		},
		{
			name:        "clamped to min lot",
			equity:      decimal.NewFromInt(100),
			riskPct:     1,
			entry:       decimal.NewFromInt(50000),
			stopLoss:    decimal.NewFromInt(49000),
			minLot:      decimal.NewFromFloat(0.1),
			maxLot:      decimal.Zero,
			minNotional: decimal.Zero,
			wantQty:     "0.1",
			wantRiskAmount: "1",
		},
		{
			name:        "clamped to max lot",
			equity:      decimal.NewFromInt(1000000),
			riskPct:     2,
			entry:       decimal.NewFromInt(50000),
			stopLoss:    decimal.NewFromInt(49900),
			minLot:      decimal.Zero,
			maxLot:      decimal.NewFromInt(1),
			minNotional: decimal.Zero,
			wantQty:     "1",
			wantRiskAmount: "20000",
		},
		{
			name:        "short position (SL above entry)",
			equity:      decimal.NewFromInt(10000),
			riskPct:     1,
			entry:       decimal.NewFromInt(50000),
			stopLoss:    decimal.NewFromInt(50500),
			minLot:      decimal.Zero,
			maxLot:      decimal.Zero,
			minNotional: decimal.Zero,
			wantQty:     "0.2",
			wantRiskAmount: "100",
		},
		{
			name:        "below min notional rejected",
			equity:      decimal.NewFromInt(10),
			riskPct:     0.1,
			entry:       decimal.NewFromInt(50000),
			stopLoss:    decimal.NewFromInt(49000),
			minLot:      decimal.Zero,
			maxLot:      decimal.Zero,
			minNotional: decimal.NewFromInt(10),
			wantErr:     true,
			errContains: "min notional",
		},
		{
			name:        "zero equity rejected",
			equity:      decimal.Zero,
			riskPct:     1,
			entry:       decimal.NewFromInt(50000),
			stopLoss:    decimal.NewFromInt(49000),
			wantErr:     true,
			errContains: "equity must be positive",
		},
		{
			name:        "negative equity rejected",
			equity:      decimal.NewFromInt(-100),
			riskPct:     1,
			entry:       decimal.NewFromInt(50000),
			stopLoss:    decimal.NewFromInt(49000),
			wantErr:     true,
			errContains: "equity must be positive",
		},
		{
			name:        "zero entry price rejected",
			equity:      decimal.NewFromInt(10000),
			riskPct:     1,
			entry:       decimal.Zero,
			stopLoss:    decimal.NewFromInt(49000),
			wantErr:     true,
			errContains: "entry price",
		},
		{
			name:        "zero stop loss rejected",
			equity:      decimal.NewFromInt(10000),
			riskPct:     1,
			entry:       decimal.NewFromInt(50000),
			stopLoss:    decimal.Zero,
			wantErr:     true,
			errContains: "stop loss",
		},
		{
			name:        "SL at entry (zero distance) rejected",
			equity:      decimal.NewFromInt(10000),
			riskPct:     1,
			entry:       decimal.NewFromInt(50000),
			stopLoss:    decimal.NewFromInt(50000),
			wantErr:     true,
			errContains: "distance is zero",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := CalculatePositionSize(
				tt.equity, tt.riskPct, tt.entry, tt.stopLoss,
				tt.minLot, tt.maxLot, tt.minNotional,
			)

			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if tt.errContains != "" && !contains(err.Error(), tt.errContains) {
					t.Errorf("error %q should contain %q", err.Error(), tt.errContains)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			wantQty := decimal.RequireFromString(tt.wantQty)
			if !got.Quantity.Equal(wantQty) {
				t.Errorf("Quantity = %s, want %s", got.Quantity.String(), tt.wantQty)
			}

			wantRisk := decimal.RequireFromString(tt.wantRiskAmount)
			if !got.RiskAmountQuote.Equal(wantRisk) {
				t.Errorf("RiskAmountQuote = %s, want %s", got.RiskAmountQuote.String(), tt.wantRiskAmount)
			}

			if !got.StopLoss.Equal(tt.stopLoss) {
				t.Errorf("StopLoss = %s, want %s", got.StopLoss.String(), tt.stopLoss.String())
			}
		})
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

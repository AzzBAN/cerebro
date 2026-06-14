package domain

import (
	"testing"

	"github.com/shopspring/decimal"
)

func TestManagedAction_RequiresStopForTighten(t *testing.T) {
	tests := []struct {
		name    string
		action  ManagedAction
		wantErr bool
	}{
		{
			name:    "tighten without stop is invalid",
			action:  ManagedAction{Decision: ActionTightenStop},
			wantErr: true,
		},
		{
			name: "tighten with stop is valid",
			action: ManagedAction{
				Decision:    ActionTightenStop,
				NewStopLoss: decimal.NewFromInt(100),
			},
			wantErr: false,
		},
		{"hold is always valid", ManagedAction{Decision: ActionHold}, false},
		{"close is always valid", ManagedAction{Decision: ActionClose}, false},
		{"flip is always valid", ManagedAction{Decision: ActionFlip}, false},
		{"unknown decision is invalid", ManagedAction{Decision: "wat"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.action.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() err = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestBiasOpposesSide(t *testing.T) {
	tests := []struct {
		name string
		bias BiasScore
		side Side
		want bool
	}{
		{"bull vs sell opposes", BiasBullish, SideSell, true},
		{"bear vs buy opposes", BiasBearish, SideBuy, true},
		{"bull vs buy aligns", BiasBullish, SideBuy, false},
		{"bear vs sell aligns", BiasBearish, SideSell, false},
		{"neutral never opposes", BiasNeutral, SideBuy, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := BiasOpposesSide(tt.bias, tt.side); got != tt.want {
				t.Errorf("BiasOpposesSide() = %v, want %v", got, tt.want)
			}
		})
	}
}

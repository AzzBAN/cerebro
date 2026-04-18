package domain

import "testing"

func TestNormalizeConfigSymbol(t *testing.T) {
	tests := []struct {
		name         string
		raw          string
		contractType ContractType
		want         Symbol
		wantErr      bool
	}{
		{name: "spot canonical", raw: "BTC/USDT", contractType: ContractSpot, want: "BTC/USDT"},
		{name: "spot exchange format", raw: "btcusdt", contractType: ContractSpot, want: "BTC/USDT"},
		{name: "futures canonical", raw: "BTC/USDT-PERP", contractType: ContractFuturesPerp, want: "BTC/USDT-PERP"},
		{name: "futures exchange format", raw: "BTCUSDT", contractType: ContractFuturesPerp, want: "BTC/USDT-PERP"},
		{name: "futures missing suffix with slash rejected", raw: "BTC/USDT", contractType: ContractFuturesPerp, wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := NormalizeConfigSymbol(tc.raw, tc.contractType)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("NormalizeConfigSymbol error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
}

func TestExchangeRoundTrip(t *testing.T) {
	sym, err := NormalizeExchangeSymbol("BTCUSDT", ContractFuturesPerp)
	if err != nil {
		t.Fatalf("NormalizeExchangeSymbol error: %v", err)
	}
	if sym != "BTC/USDT-PERP" {
		t.Fatalf("got %q", sym)
	}
	if got := ToExchangeSymbol(sym); got != "BTCUSDT" {
		t.Fatalf("ToExchangeSymbol got %q", got)
	}
}

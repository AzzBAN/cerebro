package domain

import "testing"

func TestEnvironmentValues(t *testing.T) {
	tests := []struct {
		env  Environment
		want string
	}{
		{EnvironmentPaper, "paper"},
		{EnvironmentDemo, "demo"},
		{EnvironmentLive, "live"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if string(tt.env) != tt.want {
				t.Errorf("got %q, want %q", tt.env, tt.want)
			}
		})
	}
}

func TestSideValues(t *testing.T) {
	if string(SideBuy) != "buy" {
		t.Errorf("SideBuy = %q, want %q", SideBuy, "buy")
	}
	if string(SideSell) != "sell" {
		t.Errorf("SideSell = %q, want %q", SideSell, "sell")
	}
}

func TestOrderStatusLifecycle(t *testing.T) {
	statuses := []OrderStatus{
		OrderStatusPending,
		OrderStatusSubmitted,
		OrderStatusFilled,
		OrderStatusRejected,
		OrderStatusCancelled,
	}
	for _, s := range statuses {
		if s == "" {
			t.Error("OrderStatus should not be empty")
		}
	}
}

func TestVenueValues(t *testing.T) {
	if VenueBinanceSpot != "binance_spot" {
		t.Errorf("VenueBinanceSpot = %q", VenueBinanceSpot)
	}
	if VenueBinanceFutures != "binance_futures" {
		t.Errorf("VenueBinanceFutures = %q", VenueBinanceFutures)
	}
}

func TestTimeframeValues(t *testing.T) {
	tfs := map[Timeframe]string{
		TF1m: "1m", TF5m: "5m", TF15m: "15m",
		TF1h: "1h", TF4h: "4h", TF1d: "1d",
	}
	for tf, want := range tfs {
		if string(tf) != want {
			t.Errorf("got %q, want %q", tf, want)
		}
	}
}

func TestContractTypeValues(t *testing.T) {
	if ContractSpot != "spot" {
		t.Errorf("ContractSpot = %q", ContractSpot)
	}
	if ContractFuturesPerp != "futures_perpetual" {
		t.Errorf("ContractFuturesPerp = %q", ContractFuturesPerp)
	}
}

package futures

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	gobinancefutures "github.com/adshao/go-binance/v2/futures"
	"github.com/azhar/cerebro/internal/domain"
	"github.com/shopspring/decimal"
)

// brokerWithMockServer builds a FuturesBroker whose client points at a mock
// HTTP server, with the symbol filter cache pre-seeded so PlaceBracket does not
// hit the network for exchange info.
func brokerWithMockServer(t *testing.T, mode string, handler http.HandlerFunc) (*FuturesBroker, domain.Symbol) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	client := gobinancefutures.NewClient("key", "secret")
	client.SetApiEndpoint(srv.URL)

	b := NewFuturesBroker(client, mode)

	sym, err := domain.NormalizeExchangeSymbol("BTCUSDT", domain.ContractFuturesPerp)
	if err != nil {
		t.Fatalf("normalize symbol: %v", err)
	}
	// Seed the filter cache so Filter() succeeds without a network refresh.
	b.filters.filters = map[domain.Symbol]domain.SymbolFilter{
		sym: {
			Symbol:   sym,
			Venue:    domain.VenueBinanceFutures,
			TickSize: decimal.RequireFromString("0.1"),
			StepSize: decimal.RequireFromString("0.001"),
		},
	}
	return b, sym
}

func bracketReq(sym domain.Symbol) domain.BracketRequest {
	return domain.BracketRequest{
		ParentIntentID: "intent-1",
		CorrelationID:  "corr-1",
		Symbol:         sym,
		Venue:          domain.VenueBinanceFutures,
		Side:           domain.SideSell, // short → exit side BUY
		Quantity:       decimal.RequireFromString("0.01"),
		StopLoss:       decimal.RequireFromString("65231"),
		TakeProfit:     decimal.RequireFromString("63939"),
		ClientTag:      "recon",
	}
}

// TestPlaceBracket_DemoUsesAlgoEndpoint verifies the demo endpoint routes the
// protective legs through /fapi/v1/algoOrder (the Algo Order API), which is the
// fix for Binance error -4120 ("Order type not supported for this endpoint").
func TestPlaceBracket_DemoUsesAlgoEndpoint(t *testing.T) {
	var mu sync.Mutex
	var paths []string
	b, sym := brokerWithMockServer(t, "demo", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"algoId":12345,"orderId":12345}`))
	})

	resp, err := b.PlaceBracket(context.Background(), bracketReq(sym))
	if err != nil {
		t.Fatalf("PlaceBracket (demo) error = %v", err)
	}
	if resp.StopOrderID != "12345" {
		t.Errorf("StopOrderID = %q, want 12345 (algoId)", resp.StopOrderID)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(paths) != 2 {
		t.Fatalf("expected 2 leg requests, got %d: %v", len(paths), paths)
	}
	for _, p := range paths {
		if p != "/fapi/v1/algoOrder" {
			t.Errorf("demo leg hit %q, want /fapi/v1/algoOrder", p)
		}
	}
}

// TestCancelBracket_OrderAlreadyGone verifies that a -2011 ("Unknown order
// sent") response — the order was already filled/cancelled/expired on the
// exchange — is treated as a successful, idempotent cancel rather than a hard
// error. This is the fix for proposal-adjust failures where the cached
// protective order is no longer live by the time the operator confirms.
func TestCancelBracket_OrderAlreadyGone(t *testing.T) {
	tests := []struct {
		name string
		body string
		code int
	}{
		{"unknown order -2011", `{"code":-2011,"msg":"Unknown order sent."}`, http.StatusBadRequest},
		{"order does not exist -2013", `{"code":-2013,"msg":"Order does not exist."}`, http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b, sym := brokerWithMockServer(t, "mainnet", func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.code)
				_, _ = w.Write([]byte(tt.body))
			})
			err := b.CancelBracket(context.Background(), domain.BracketResponse{
				Symbol:            sym,
				StopOrderID:       "111",
				TakeProfitOrderID: "222",
			})
			if err != nil {
				t.Errorf("CancelBracket() = %v, want nil (already-gone is idempotent)", err)
			}
		})
	}
}

// TestCancelBracket_RealErrorSurfaces verifies a genuine cancel failure (not an
// already-gone code) is still aggregated and returned, so a live leg is never
// silently left unprotected.
func TestCancelBracket_RealErrorSurfaces(t *testing.T) {
	b, sym := brokerWithMockServer(t, "mainnet", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"code":-1021,"msg":"Timestamp for this request is outside of the recvWindow."}`))
	})
	err := b.CancelBracket(context.Background(), domain.BracketResponse{
		Symbol:      sym,
		StopOrderID: "111",
	})
	if err == nil {
		t.Fatal("CancelBracket() = nil, want error for genuine cancel failure")
	}
}
func TestPlaceBracket_MainnetUsesStandardEndpoint(t *testing.T) {
	var mu sync.Mutex
	var paths []string
	b, sym := brokerWithMockServer(t, "mainnet", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"orderId":67890}`))
	})

	resp, err := b.PlaceBracket(context.Background(), bracketReq(sym))
	if err != nil {
		t.Fatalf("PlaceBracket (mainnet) error = %v", err)
	}
	if resp.StopOrderID != "67890" {
		t.Errorf("StopOrderID = %q, want 67890 (orderId)", resp.StopOrderID)
	}
	mu.Lock()
	defer mu.Unlock()
	for _, p := range paths {
		if p != "/fapi/v1/order" {
			t.Errorf("mainnet leg hit %q, want /fapi/v1/order", p)
		}
	}
}

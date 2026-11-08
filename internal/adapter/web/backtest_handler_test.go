package web

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/azhar/cerebro/internal/backtest"
	"github.com/azhar/cerebro/internal/marketdata"
)

func TestWeb_HandleBacktestRun_LivePipeline(t *testing.T) {
	hub := marketdata.NewHub()
	tradeStore := backtest.NewMemTradeStore()
	srv := NewServer(Config{ListenAddr: "127.0.0.1:0", AuthToken: "test-token"}, hub, nil, tradeStore, 100)

	mux := http.NewServeMux()
	srv.routes(mux)

	ts := httptest.NewServer(mux)
	defer ts.Close()

	payload, _ := json.Marshal(map[string]any{
		"symbol":     "BTC/USDT",
		"strategy":   "trend_following",
		"timeframe":  "1h",
		"from":       "2024-01-01",
		"to":         "2024-02-01",
		"agent_mode": "smart",
	})

	req, err := http.NewRequest(http.MethodPost, ts.URL+"/api/backtest/run", bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("create request failed: %v", err)
	}
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		var buf bytes.Buffer
		buf.ReadFrom(resp.Body)
		t.Fatalf("expected 200 OK, got %d: %s", resp.StatusCode, buf.String())
	}

	var result backtestResponseDTO
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	// 1. Verify candles returned
	if len(result.Candles) == 0 {
		t.Fatalf("expected candles to be returned, got 0")
	}
	t.Logf("Candles returned: %d (from %d to %d)",
		len(result.Candles), result.Candles[0].Time, result.Candles[len(result.Candles)-1].Time)

	// 2. Verify report fields
	t.Logf("Backtest Report: Strategy=%s, Symbol=%s, TotalTrades=%d, WinRate=%.2f%%, NetPnL=%.2f USDT, MaxDD=%.2f%%",
		result.Report.Strategy, result.Report.Symbol, result.Report.TotalTrades,
		result.Report.WinRate, result.Report.NetPnL, result.Report.MaxDrawdown)

	// 3. Verify decisions and trades
	t.Logf("Decisions recorded: %d, Trades executed: %d", len(result.Decisions), len(result.Trades))

	for i, d := range result.Decisions {
		t.Logf("Decision #%d: [%s] %s %s @ %.2f -> Action: %s, Verdict: %s, Rationale: %s",
			i+1, d.Timestamp.Format("2006-01-02 15:04"), d.Strategy, d.SignalSide, d.Price,
			d.Action, d.Verdict, d.Rationale)
	}
}

func TestWeb_HandleBacktestRun_Scalping(t *testing.T) {
	hub := marketdata.NewHub()
	tradeStore := backtest.NewMemTradeStore()
	srv := NewServer(Config{ListenAddr: "127.0.0.1:0", AuthToken: "test-token"}, hub, nil, tradeStore, 100)

	mux := http.NewServeMux()
	srv.routes(mux)

	ts := httptest.NewServer(mux)
	defer ts.Close()

	payload, _ := json.Marshal(map[string]any{
		"symbol":     "BTC/USDT",
		"strategy":   "scalping",
		"timeframe":  "1m",
		"from":       "2024-01-01",
		"to":         "2024-01-02",
		"agent_mode": "smart",
	})

	req, err := http.NewRequest(http.MethodPost, ts.URL+"/api/backtest/run", bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("create request failed: %v", err)
	}
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", resp.StatusCode)
	}

	var result backtestResponseDTO
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	t.Logf("Scalping Backtest: Candles=%d, Trades=%d, Decisions=%d, NetPnL=%.2f",
		len(result.Candles), len(result.Trades), len(result.Decisions), result.Report.NetPnL)
}

func TestWeb_HandleBacktestRun_VolatilityBreakout(t *testing.T) {
	hub := marketdata.NewHub()
	tradeStore := backtest.NewMemTradeStore()
	srv := NewServer(Config{ListenAddr: "127.0.0.1:0", AuthToken: "test-token"}, hub, nil, tradeStore, 100)

	mux := http.NewServeMux()
	srv.routes(mux)

	ts := httptest.NewServer(mux)
	defer ts.Close()

	payload, _ := json.Marshal(map[string]any{
		"symbol":     "BTC/USDT",
		"strategy":   "volatility_breakout",
		"timeframe":  "1h",
		"from":       "2026-01-01",
		"to":         "2026-02-01",
		"agent_mode": "smart",
	})

	req, err := http.NewRequest(http.MethodPost, ts.URL+"/api/backtest/run", bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("create request failed: %v", err)
	}
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", resp.StatusCode)
	}

	var result backtestResponseDTO
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	t.Logf("VolatilityBreakout: Candles=%d, Trades=%d, Decisions=%d, NetPnL=%.2f",
		len(result.Candles), len(result.Trades), len(result.Decisions), result.Report.NetPnL)
}
func TestWeb_HandleBacktestRun_MeanReversion(t *testing.T) {
	hub := marketdata.NewHub()
	tradeStore := backtest.NewMemTradeStore()
	srv := NewServer(Config{ListenAddr: "127.0.0.1:0", AuthToken: "test-token"}, hub, nil, tradeStore, 100)

	mux := http.NewServeMux()
	srv.routes(mux)

	ts := httptest.NewServer(mux)
	defer ts.Close()

	payload, _ := json.Marshal(map[string]any{
		"symbol":     "BTC/USDT",
		"strategy":   "mean_reversion",
		"timeframe":  "1h",
		"from":       "2024-01-01",
		"to":         "2024-02-01",
		"agent_mode": "smart",
	})

	req, err := http.NewRequest(http.MethodPost, ts.URL+"/api/backtest/run", bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("create request failed: %v", err)
	}
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", resp.StatusCode)
	}

	var result backtestResponseDTO
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	t.Logf("MeanReversion: Candles=%d, Trades=%d, Decisions=%d, WinRate=%.2f%%, NetPnL=%.2f USDT, MaxDD=%.2f%%",
		len(result.Candles), len(result.Trades), len(result.Decisions),
		result.Report.WinRate, result.Report.NetPnL, result.Report.MaxDrawdown)

	if result.Report.TotalTrades == 0 {
		t.Errorf("expected trades to be executed, got 0")
	}
}

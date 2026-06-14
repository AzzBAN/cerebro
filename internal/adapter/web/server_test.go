package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/azhar/cerebro/internal/domain"
	"github.com/azhar/cerebro/internal/uistate"
	"github.com/shopspring/decimal"
)

// stubDispatcher records the last dispatched command and returns a canned reply.
type stubDispatcher struct {
	lastActor string
	lastRaw   string
	reply     string
}

func (s *stubDispatcher) Dispatch(_ context.Context, actorID, raw string) string {
	s.lastActor = actorID
	s.lastRaw = raw
	return s.reply
}

func newTestServer(token string, disp Dispatcher) *Server {
	return NewServer(Config{AuthToken: token}, nil, disp, nil, 100)
}

func TestAuthMiddleware(t *testing.T) {
	tests := []struct {
		name       string
		token      string
		header     string
		query      string
		wantStatus int
	}{
		{"valid bearer", "secret", "Bearer secret", "", http.StatusOK},
		{"missing header", "secret", "", "", http.StatusUnauthorized},
		{"wrong token", "secret", "Bearer nope", "", http.StatusUnauthorized},
		{"valid query token", "secret", "", "secret", http.StatusOK},
		{"wrong query token", "secret", "", "nope", http.StatusUnauthorized},
		{"no token configured allows through", "", "", "", http.StatusOK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestServer(tt.token, nil)
			h := s.auth(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusOK)
			}))
			req := httptest.NewRequest(http.MethodGet, "/api/state", nil)
			if tt.header != "" {
				req.Header.Set("Authorization", tt.header)
			}
			if tt.query != "" {
				q := req.URL.Query()
				q.Set("token", tt.query)
				req.URL.RawQuery = q.Encode()
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
		})
	}
}

func TestHandleCommandRoutesToDispatcher(t *testing.T) {
	disp := &stubDispatcher{reply: "trading paused"}
	s := newTestServer("", disp)

	body := strings.NewReader(`{"command":"/pause"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/command", body)
	rec := httptest.NewRecorder()
	s.handleCommand(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if disp.lastRaw != "/pause" {
		t.Errorf("dispatched raw = %q, want /pause", disp.lastRaw)
	}
	if disp.lastActor != webActorID {
		t.Errorf("actor = %q, want %q", disp.lastActor, webActorID)
	}
	var resp map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["reply"] != "trading paused" {
		t.Errorf("reply = %q, want %q", resp["reply"], "trading paused")
	}
}

func TestHandleCommandRejectsBadBody(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{"empty command", `{"command":""}`},
		{"malformed json", `{`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestServer("", &stubDispatcher{})
			req := httptest.NewRequest(http.MethodPost, "/api/command", strings.NewReader(tt.body))
			rec := httptest.NewRecorder()
			s.handleCommand(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400", rec.Code)
			}
		})
	}
}

func TestHandleCommandNoDispatcher(t *testing.T) {
	s := newTestServer("", nil)
	req := httptest.NewRequest(http.MethodPost, "/api/command", strings.NewReader(`{"command":"/status"}`))
	rec := httptest.NewRecorder()
	s.handleCommand(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rec.Code)
	}
}

func TestSnapshotRoundTrip(t *testing.T) {
	s := newTestServer("", nil)

	s.SendPositions([]domain.Position{{
		Symbol:       "BTCUSDT",
		Venue:        domain.VenueBinanceFutures,
		Side:         domain.SideBuy,
		Quantity:     decimal.RequireFromString("0.5"),
		EntryPrice:   decimal.RequireFromString("60000.12345678"),
		CurrentPrice: decimal.RequireFromString("61000"),
		Leverage:     10,
	}})
	s.SendBias(domain.BiasResult{Symbol: "BTCUSDT", Score: domain.BiasBullish, Reasoning: "trend up"})
	s.SendHeartbeat("state=trading")

	snap := s.Snapshot()

	if len(snap.Positions) != 1 {
		t.Fatalf("positions = %d, want 1", len(snap.Positions))
	}
	// Decimals must serialise as strings with full precision (never float64).
	if snap.Positions[0].EntryPrice != "60000.12345678" {
		t.Errorf("entryPrice = %q, want 60000.12345678", snap.Positions[0].EntryPrice)
	}
	if len(snap.Bias) != 1 || snap.Bias[0].Label != "Bullish" {
		t.Errorf("bias = %+v, want one Bullish entry", snap.Bias)
	}
	if snap.Heartbeat != "state=trading" {
		t.Errorf("heartbeat = %q", snap.Heartbeat)
	}

	// The snapshot must be valid JSON with string-typed decimals.
	raw, err := json.Marshal(snap)
	if err != nil {
		t.Fatalf("marshal snapshot: %v", err)
	}
	if !strings.Contains(string(raw), `"entryPrice":"60000.12345678"`) {
		t.Errorf("marshalled snapshot missing string decimal: %s", raw)
	}
}

func TestLogRingBufferCap(t *testing.T) {
	s := NewServer(Config{}, nil, nil, nil, 3)
	for i := 0; i < 10; i++ {
		s.SendSysLog("INFO", "line")
	}
	snap := s.Snapshot()
	if len(snap.Logs) != 3 {
		t.Errorf("logs = %d, want 3 (ring cap)", len(snap.Logs))
	}
}

func TestSendMacroAndBudget(t *testing.T) {
	s := newTestServer("", nil)
	if snap := s.Snapshot(); snap.Macro != nil || snap.Budget != nil {
		t.Fatal("macro/budget should be nil before any send")
	}
	s.SendMacro(uistate.MacroSnapshot{
		FearGreed: domain.FearGreedIndex{Value: 72, Category: "Greed"},
		UpdatedAt: time.Now(),
	})
	s.SendBudget(uistate.BudgetSnapshot{Date: "2026-06-14", TokensUsed: 48200, CostUSD: 1.24})
	snap := s.Snapshot()
	if snap.Macro == nil || snap.Macro.FearGreedValue != 72 {
		t.Errorf("macro = %+v, want FearGreed 72", snap.Macro)
	}
	if snap.Budget == nil || snap.Budget.TokensUsed != 48200 {
		t.Errorf("budget = %+v, want 48200 tokens", snap.Budget)
	}
}

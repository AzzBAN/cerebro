package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/azhar/cerebro/internal/positionproposal"
	"github.com/gorilla/websocket"
)

// ActorID is the actor identity attributed to commands issued from the web
// dashboard. The chatops dispatcher's allowlist (if configured) is keyed on
// actor IDs; "web:dashboard" lets operators allowlist the web surface
// distinctly from telegram users. The composition root adds this to the
// allowlist when the web server is enabled (the bearer-token gate already
// authenticated the request before it reaches the dispatcher).
const ActorID = "web:dashboard"

// webActorID is the internal alias used by the command handler.
const webActorID = ActorID

// Run starts the HTTP server and blocks until ctx is cancelled. Returns nil on
// a clean (context-driven) shutdown, matching the runtime goroutine contract.
func (s *Server) Run(ctx context.Context) error {
	mux := http.NewServeMux()
	s.routes(mux)

	srv := &http.Server{
		Addr:              s.cfg.ListenAddr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	// Feed live quotes from the market-data hub when one is wired.
	if s.hub != nil {
		go s.consumeQuotes(ctx)
	}

	errCh := make(chan error, 1)
	go func() {
		slog.Info("web dashboard listening", "addr", s.cfg.ListenAddr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case <-ctx.Done():
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
		return nil
	case err := <-errCh:
		return fmt.Errorf("web server: %w", err)
	}
}

// consumeQuotes subscribes to the hub and updates quote state on every tick.
func (s *Server) consumeQuotes(ctx context.Context) {
	quotes, _ := s.hub.Subscribe()
	for {
		select {
		case <-ctx.Done():
			return
		case evt, ok := <-quotes:
			if !ok {
				return
			}
			dto := quoteToDTO(evt.Quote)
			s.mu.Lock()
			s.quotes[dto.Symbol] = dto
			s.mu.Unlock()
			s.broadcast("quote", dto)
		}
	}
}

// routes registers all HTTP handlers on mux. The static frontend (when
// embedded) is served at "/"; the API + WS live under /api and /ws, all gated
// by the bearer-token middleware.
func (s *Server) routes(mux *http.ServeMux) {
	mux.Handle("GET /api/state", s.auth(http.HandlerFunc(s.handleState)))
	mux.Handle("GET /api/trades", s.auth(http.HandlerFunc(s.handleTrades)))
	mux.Handle("GET /api/intents", s.auth(http.HandlerFunc(s.handleIntents)))
	mux.Handle("GET /api/history", s.auth(http.HandlerFunc(s.handleHistory)))
	mux.Handle("POST /api/command", s.auth(http.HandlerFunc(s.handleCommand)))
	mux.Handle("GET /api/strategies/raw", s.auth(http.HandlerFunc(s.handleStrategiesRaw)))
	mux.Handle("POST /api/strategies", s.auth(http.HandlerFunc(s.handleUpdateStrategies)))
	mux.Handle("GET /api/configs", s.auth(http.HandlerFunc(s.handleConfigList)))
	mux.Handle("GET /api/configs/{name}", s.auth(http.HandlerFunc(s.handleConfigGet)))
	mux.Handle("PUT /api/configs/{name}", s.auth(http.HandlerFunc(s.handleConfigPut)))
	mux.Handle("GET /api/configs/{name}/form/schema", s.auth(http.HandlerFunc(s.handleConfigFormSchema)))
	mux.Handle("GET /api/configs/{name}/form", s.auth(http.HandlerFunc(s.handleConfigFormRead)))
	mux.Handle("PUT /api/configs/{name}/form", s.auth(http.HandlerFunc(s.handleConfigFormPut)))
	mux.Handle("POST /api/proposals/{id}/confirm", s.auth(http.HandlerFunc(s.handleProposalAction)))
	mux.Handle("POST /api/proposals/{id}/reject", s.auth(http.HandlerFunc(s.handleProposalAction)))
	mux.Handle("GET /ws", s.auth(http.HandlerFunc(s.handleWS)))
	mux.Handle("POST /api/backtest/run", s.auth(http.HandlerFunc(s.handleBacktestRun)))

	if sub, ok := frontendFS(); ok {
		mux.Handle("/", http.FileServer(http.FS(sub)))
	} else {
		mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			_, _ = w.Write([]byte("Cerebro web dashboard: frontend not built. Run `make web`."))
		})
	}
}

func (s *Server) handleState(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.Snapshot())
}

// parseWindow extracts optional from/to millis query params. Defaults to
// the trailing 24h window so a fresh dashboard request always has data.
func parseWindow(r *http.Request) (time.Time, time.Time) {
	to := time.Now()
	from := to.Add(-24 * time.Hour)
	if v := r.URL.Query().Get("from"); v != "" {
		if ms, err := strconv.ParseInt(v, 10, 64); err == nil {
			from = time.UnixMilli(ms)
		}
	}
	if v := r.URL.Query().Get("to"); v != "" {
		if ms, err := strconv.ParseInt(v, 10, 64); err == nil {
			to = time.UnixMilli(ms)
		}
	}
	return from, to
}

func (s *Server) handleTrades(w http.ResponseWriter, r *http.Request) {
	if s.tradeStore == nil {
		writeJSON(w, http.StatusOK, []any{})
		return
	}
	from, to := parseWindow(r)
	trades, err := s.tradeStore.TradesByWindow(r.Context(), from, to)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query trades failed"})
		return
	}
	out := make([]TradeDTO, 0, len(trades))
	for _, t := range trades {
		out = append(out, tradeToDTO(t))
	}
	writeJSON(w, http.StatusOK, out)
}

// handleIntents lists every order intent (pending/filled/rejected/etc.)
// inside the time window. The response uses the OrderIntentDTO shape so
// the order-history view can render intent rows with full context.
func (s *Server) handleIntents(w http.ResponseWriter, r *http.Request) {
	if s.intentReader == nil {
		writeJSON(w, http.StatusOK, []any{})
		return
	}
	from, to := parseWindow(r)
	intents, err := s.intentReader.IntentsByWindow(r.Context(), from, to)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query intents failed"})
		return
	}
	out := make([]OrderIntentDTO, 0, len(intents))
	for _, i := range intents {
		out = append(out, orderIntentToDTO(i))
	}
	writeJSON(w, http.StatusOK, out)
}

// handleHistory returns both intents and closed trades for a single window
// so the dashboard tab can render the order history in one round-trip.
// The trades and intents are returned in two separate arrays (newest-first
// for intents, oldest-first for trades) and the dashboard merges them.
func (s *Server) handleHistory(w http.ResponseWriter, r *http.Request) {
	type historyResp struct {
		Intents []OrderIntentDTO `json:"intents"`
		Trades  []TradeDTO       `json:"trades"`
	}
	resp := historyResp{Intents: []OrderIntentDTO{}, Trades: []TradeDTO{}}
	from, to := parseWindow(r)
	if s.tradeStore != nil {
		trades, err := s.tradeStore.TradesByWindow(r.Context(), from, to)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query trades failed"})
			return
		}
		resp.Trades = make([]TradeDTO, 0, len(trades))
		for _, t := range trades {
			resp.Trades = append(resp.Trades, tradeToDTO(t))
		}
	}
	if s.intentReader != nil {
		intents, err := s.intentReader.IntentsByWindow(r.Context(), from, to)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query intents failed"})
			return
		}
		resp.Intents = make([]OrderIntentDTO, 0, len(intents))
		for _, i := range intents {
			resp.Intents = append(resp.Intents, orderIntentToDTO(i))
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

// commandRequest is the POST /api/command body.
type commandRequest struct {
	Command string `json:"command"`
}

func (s *Server) handleCommand(w http.ResponseWriter, r *http.Request) {
	if s.dispatcher == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "chatops dispatcher not configured"})
		return
	}
	var req commandRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Command == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid command body"})
		return
	}
	reply := s.dispatcher.Dispatch(r.Context(), webActorID, req.Command)
	writeJSON(w, http.StatusOK, map[string]string{"reply": reply})
}

// handleStrategiesRaw returns the RAW (pre-aggressiveness-scaling) editable
// strategy config used to pre-fill the dashboard edit form.
func (s *Server) handleStrategiesRaw(w http.ResponseWriter, _ *http.Request) {
	if s.editor == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "strategy editor not configured"})
		return
	}
	raw, err := s.editor.RawView()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, raw)
}

// handleUpdateStrategies validates and applies edited strategy config, then
// hot-reloads the engine. On a validation failure the on-disk files and the
// live engine are left unchanged and a 400 carries the validator message.
func (s *Server) handleUpdateStrategies(w http.ResponseWriter, r *http.Request) {
	if s.editor == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "strategy editor not configured"})
		return
	}
	var req EditRequestDTO
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	if req.Aggressiveness == "" && len(req.Strategies) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "no edits provided"})
		return
	}
	dto, err := s.editor.ApplyEdits(r.Context(), req)
	if err != nil {
		// Edit was rejected (validation/patch failure). Files + engine unchanged.
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	// Push the new config to all live clients so panels refresh without refetch.
	s.broadcast("config", dto)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleProposalAction(w http.ResponseWriter, r *http.Request) {
	if s.proposalController == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "proposals not configured"})
		return
	}
	id := r.PathValue("id")
	if id == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing proposal id"})
		return
	}
	reject := strings.HasSuffix(r.URL.Path, "/reject")
	var err error
	if reject {
		err = s.proposalController.Reject(id)
	} else {
		err = s.proposalController.Confirm(r.Context(), id)
	}
	if err != nil {
		if errors.Is(err, positionproposal.ErrUnknownProposal) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleConfigList(w http.ResponseWriter, _ *http.Request) {
	if s.configEditor == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "config editor not configured"})
		return
	}
	names := s.configEditor.List()
	writeJSON(w, http.StatusOK, map[string]any{"files": names})
}

func (s *Server) handleConfigGet(w http.ResponseWriter, r *http.Request) {
	if s.configEditor == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "config editor not configured"})
		return
	}
	name := r.PathValue("name")
	if name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing config file name"})
		return
	}
	dto, err := s.configEditor.Read(name)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, dto)
}
func (s *Server) handleConfigPut(w http.ResponseWriter, r *http.Request) {
	if s.configEditor == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "config editor not configured"})
		return
	}
	name := r.PathValue("name")
	if name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing config file name"})
		return
	}

	var req ConfigFileSaveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	dto, err := s.configEditor.Save(r.Context(), name, req.Content)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	s.broadcast("config", dto)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleConfigFormSchema(w http.ResponseWriter, r *http.Request) {
	if s.configEditor == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "config editor not configured"})
		return
	}
	name := r.PathValue("name")
	if name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing config file name"})
		return
	}
	schema, err := s.configEditor.FormSchema(name)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, schema)
}

func (s *Server) handleConfigFormRead(w http.ResponseWriter, r *http.Request) {
	if s.configEditor == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "config editor not configured"})
		return
	}
	name := r.PathValue("name")
	if name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing config file name"})
		return
	}
	values, err := s.configEditor.FormRead(name)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, values)
}

func (s *Server) handleConfigFormPut(w http.ResponseWriter, r *http.Request) {
	if s.configEditor == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "config editor not configured"})
		return
	}
	name := r.PathValue("name")
	if name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing config file name"})
		return
	}

	var fields map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&fields); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	dto, err := s.configEditor.FormWrite(r.Context(), name, fields)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	s.broadcast("config", dto)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// wsUpgrader is configured per-server so the Origin check honours the
// configured allowlist.
func (s *Server) wsUpgrader() websocket.Upgrader {
	return websocket.Upgrader{
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
		CheckOrigin:     s.checkOrigin,
	}
}

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	up := s.wsUpgrader()
	conn, err := up.Upgrade(w, r, nil)
	if err != nil {
		slog.Debug("web: ws upgrade failed", "error", err)
		return
	}
	c := &client{send: make(chan []byte, 256)}

	// Replay the full snapshot so the client renders immediately.
	if payload, err := json.Marshal(envelope{Type: "snapshot", Data: s.Snapshot()}); err == nil {
		c.send <- payload
	}

	s.addClient(c)
	go s.writePump(conn, c)
	s.readPump(conn, c)
}

// writePump drains the client's send queue to the socket.
func (s *Server) writePump(conn *websocket.Conn, c *client) {
	ping := time.NewTicker(30 * time.Second)
	defer func() {
		ping.Stop()
		_ = conn.Close()
	}()
	for {
		select {
		case msg, ok := <-c.send:
			if !ok {
				_ = conn.WriteMessage(websocket.CloseMessage, nil)
				return
			}
			if err := conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				return
			}
		case <-ping.C:
			if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

// readPump discards inbound frames (the dashboard is push-only) and detects
// disconnect so the client can be cleaned up.
func (s *Server) readPump(conn *websocket.Conn, c *client) {
	defer s.removeClient(c)
	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// frontendFS returns the embedded frontend as a sub-filesystem, or ok=false
// when the export hasn't been built into the binary. The presence of
// index.html is the build marker — the dist/.gitkeep placeholder alone (which
// `//go:embed all:dist` would otherwise count) does not qualify.
func frontendFS() (fs.FS, bool) {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		return nil, false
	}
	if _, err := fs.Stat(sub, "index.html"); err != nil {
		return nil, false
	}
	return sub, true
}

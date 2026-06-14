package web

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"github.com/azhar/cerebro/internal/domain"
	"github.com/azhar/cerebro/internal/marketdata"
	"github.com/azhar/cerebro/internal/uistate"
)

// maxLogLines caps the in-memory log ring buffer mirrored to new clients.
const defaultMaxLogLines = 500

// envelope is the WebSocket message frame. Type selects the client-side
// reducer; Data is one of the *DTO types defined in state.go.
type envelope struct {
	Type string `json:"type"`
	Data any    `json:"data"`
}

// Dispatcher is the subset of chatops.Dispatcher the web command endpoint
// needs. Declared as an interface so tests can stub it.
type Dispatcher interface {
	Dispatch(ctx context.Context, actorID, raw string) string
}

// client is a single connected browser, with a buffered outbound queue so a
// slow socket never blocks the engine. On overflow the client is dropped.
type client struct {
	send chan []byte
}

// Server is the HTTP + WebSocket dashboard adapter. It implements
// uistate.Sink (and observability.LogSink via SendSysLog), holding the latest
// snapshot of every panel and broadcasting deltas to connected clients.
type Server struct {
	cfg        Config
	dispatcher Dispatcher
	tradeStore TradeReader
	hub        *marketdata.Hub

	mu        sync.RWMutex
	quotes    map[string]QuoteDTO
	positions []PositionDTO
	logs      []LogDTO
	agentRuns map[string]AgentRunDTO
	runOrder  []string
	bias      map[string]BiasDTO
	biasOrder []string
	macro     MacroDTO
	macroSet  bool
	news      []NewsItemDTO
	budget    BudgetDTO
	budgetSet bool
	heartbeat string

	maxLogLines int

	clientsMu sync.Mutex
	clients   map[*client]struct{}
}

// Config is the web server's runtime configuration.
type Config struct {
	ListenAddr     string
	AuthToken      string
	AllowedOrigins []string
}

// TradeReader is the read-only slice of port.TradeStore the web layer needs.
type TradeReader interface {
	TradesByWindow(ctx context.Context, from, to time.Time) ([]domain.Trade, error)
}

// NewServer builds a web dashboard server. hub may be nil (no live quote feed);
// dispatcher may be nil and set later via SetDispatcher (the chatops dispatcher
// is constructed after the UI sink in the composition root).
func NewServer(cfg Config, hub *marketdata.Hub, dispatcher Dispatcher, trades TradeReader, maxLogLines int) *Server {
	if maxLogLines <= 0 {
		maxLogLines = defaultMaxLogLines
	}
	return &Server{
		cfg:         cfg,
		dispatcher:  dispatcher,
		tradeStore:  trades,
		hub:         hub,
		quotes:      make(map[string]QuoteDTO),
		agentRuns:   make(map[string]AgentRunDTO),
		bias:        make(map[string]BiasDTO),
		clients:     make(map[*client]struct{}),
		maxLogLines: maxLogLines,
	}
}

// SetDispatcher injects the ChatOps dispatcher used by POST /api/command. Call
// once during wiring before Run; concurrent calls are not supported.
func (s *Server) SetDispatcher(d Dispatcher) {
	s.dispatcher = d
}

// SetTradeStore injects the trade reader used by GET /api/trades.
func (s *Server) SetTradeStore(t TradeReader) {
	s.tradeStore = t
}

// ─── uistate.Sink implementation ──────────────────────────────────────────────

// SendPositions replaces the open-position snapshot.
func (s *Server) SendPositions(positions []domain.Position) {
	dtos := make([]PositionDTO, 0, len(positions))
	for _, p := range positions {
		dtos = append(dtos, positionToDTO(p))
	}
	s.mu.Lock()
	s.positions = dtos
	s.mu.Unlock()
	s.broadcast("positions", dtos)
}

// SendBias records a fresh directional read.
func (s *Server) SendBias(b domain.BiasResult) {
	dto := biasToDTO(b)
	s.mu.Lock()
	if _, ok := s.bias[dto.Symbol]; !ok {
		s.biasOrder = append(s.biasOrder, dto.Symbol)
	}
	s.bias[dto.Symbol] = dto
	s.mu.Unlock()
	s.broadcast("bias", dto)
}

// SendMacro records the latest macro snapshot.
func (s *Server) SendMacro(m uistate.MacroSnapshot) {
	dto := macroToDTO(m)
	s.mu.Lock()
	s.macro = dto
	s.macroSet = true
	s.mu.Unlock()
	s.broadcast("macro", dto)
}

// SendNews records the latest headlines.
func (s *Server) SendNews(n uistate.NewsSnapshot) {
	dtos := make([]NewsItemDTO, 0, len(n.Items))
	for _, item := range n.Items {
		dtos = append(dtos, NewsItemDTO{
			Title:     item.Title,
			Source:    item.Source,
			Domain:    item.Domain,
			URL:       item.URL,
			Sentiment: item.Sentiment,
		})
	}
	s.mu.Lock()
	s.news = dtos
	s.mu.Unlock()
	s.broadcast("news", dtos)
}

// SendBudget records the LLM daily-spend snapshot.
func (s *Server) SendBudget(b uistate.BudgetSnapshot) {
	dto := budgetToDTO(b)
	s.mu.Lock()
	s.budget = dto
	s.budgetSet = true
	s.mu.Unlock()
	s.broadcast("budget", dto)
}

// SendHeartbeat records the status-bar heartbeat line.
func (s *Server) SendHeartbeat(line string) {
	s.mu.Lock()
	s.heartbeat = line
	s.mu.Unlock()
	s.broadcast("heartbeat", line)
}

// SendAgentState records a live agent step transition.
func (s *Server) SendAgentState(st uistate.AgentState) {
	dto := agentStateToDTO(st)
	s.mu.Lock()
	if _, ok := s.agentRuns[dto.RunID]; !ok {
		s.runOrder = append(s.runOrder, dto.RunID)
	}
	s.agentRuns[dto.RunID] = dto
	s.mu.Unlock()
	s.broadcast("agent", dto)
}

// SendAgentLog appends an agent reasoning line to the log.
func (s *Server) SendAgentLog(line string) {
	s.appendLog(LogDTO{Level: "AGENT", Text: line, At: time.Now().UnixMilli()})
}

// SendOrderLog appends an order-lifecycle line to the log.
func (s *Server) SendOrderLog(line string) {
	s.appendLog(LogDTO{Level: "ORDER", Text: line, At: time.Now().UnixMilli()})
}

// SendSysLog appends a system (slog) line. Satisfies observability.LogSink.
func (s *Server) SendSysLog(level, line string) {
	s.appendLog(LogDTO{Level: level, Text: line, At: time.Now().UnixMilli()})
}

func (s *Server) appendLog(e LogDTO) {
	s.mu.Lock()
	s.logs = append(s.logs, e)
	if len(s.logs) > s.maxLogLines {
		s.logs = s.logs[len(s.logs)-s.maxLogLines:]
	}
	s.mu.Unlock()
	s.broadcast("log", e)
}

// ─── snapshot + broadcast ─────────────────────────────────────────────────────

// snapshot is the full initial state sent to a freshly connected client.
type snapshot struct {
	Quotes    []QuoteDTO    `json:"quotes"`
	Positions []PositionDTO `json:"positions"`
	Logs      []LogDTO      `json:"logs"`
	AgentRuns []AgentRunDTO `json:"agentRuns"`
	Bias      []BiasDTO     `json:"bias"`
	Macro     *MacroDTO     `json:"macro"`
	News      []NewsItemDTO `json:"news"`
	Budget    *BudgetDTO    `json:"budget"`
	Heartbeat string        `json:"heartbeat"`
}

// Snapshot returns a deep-ish copy of the current state for /api/state and the
// initial WebSocket replay.
func (s *Server) Snapshot() snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()

	quotes := make([]QuoteDTO, 0, len(s.quotes))
	for _, q := range s.quotes {
		quotes = append(quotes, q)
	}
	runs := make([]AgentRunDTO, 0, len(s.runOrder))
	for _, id := range s.runOrder {
		runs = append(runs, s.agentRuns[id])
	}
	bias := make([]BiasDTO, 0, len(s.biasOrder))
	for _, sym := range s.biasOrder {
		bias = append(bias, s.bias[sym])
	}
	snap := snapshot{
		Quotes:    quotes,
		Positions: append([]PositionDTO(nil), s.positions...),
		Logs:      append([]LogDTO(nil), s.logs...),
		AgentRuns: runs,
		Bias:      bias,
		News:      append([]NewsItemDTO(nil), s.news...),
		Heartbeat: s.heartbeat,
	}
	if s.macroSet {
		m := s.macro
		snap.Macro = &m
	}
	if s.budgetSet {
		b := s.budget
		snap.Budget = &b
	}
	return snap
}

// broadcast marshals an envelope and queues it to every connected client.
// Non-blocking per client: a full queue means the client is dropped.
func (s *Server) broadcast(typ string, data any) {
	payload, err := json.Marshal(envelope{Type: typ, Data: data})
	if err != nil {
		slog.Error("web: marshal broadcast failed", "type", typ, "error", err)
		return
	}
	s.clientsMu.Lock()
	for c := range s.clients {
		select {
		case c.send <- payload:
		default:
			// Slow client: drop it. The reconnecting frontend will refetch
			// the full snapshot on reconnect.
			close(c.send)
			delete(s.clients, c)
		}
	}
	s.clientsMu.Unlock()
}

func (s *Server) addClient(c *client) {
	s.clientsMu.Lock()
	s.clients[c] = struct{}{}
	s.clientsMu.Unlock()
}

func (s *Server) removeClient(c *client) {
	s.clientsMu.Lock()
	if _, ok := s.clients[c]; ok {
		close(c.send)
		delete(s.clients, c)
	}
	s.clientsMu.Unlock()
}

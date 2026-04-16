package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/azhar/cerebro/internal/marketdata"
	"github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ─── Message types sent to the TUI model ─────────────────────────────────────

// QuoteMsg carries a market data quote update.
type QuoteMsg marketdata.QuoteEvent

// CandleMsg carries a new closed candle.
type CandleMsg marketdata.CandleEvent

// AgentLogMsg is a line of agent reasoning to display.
type AgentLogMsg struct{ Line string }

// SysLogMsg is a system log line (from slog) forwarded to the TUI panel.
type SysLogMsg struct {
	Level string // "ERROR", "WARN", "INFO", "DEBUG"
	Line  string
	At    time.Time
}

// HeartbeatMsg carries the latest heartbeat summary for the status bar.
type HeartbeatMsg struct {
	Line string
	At   time.Time
}

// OrderMsg is an order lifecycle event (placed, filled, cancelled).
type OrderMsg struct{ Line string }

// ─── Lipgloss styles ─────────────────────────────────────────────────────────

var (
	// Panels
	headerStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12"))
	dimStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	borderStyle  = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1)
	inputStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("14"))

	// Log level badges
	errStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("9"))
	warnStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("11"))
	infoStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("12"))
	debugStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	agentStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("13")) // magenta

	// Ticker
	priceStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	symStyle      = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("15"))

	// Status bar (heartbeat)
	heartbeatStyle = lipgloss.NewStyle().
			Background(lipgloss.Color("235")).
			Foreground(lipgloss.Color("7")).
			Padding(0, 1)
	heartbeatTsStyle = lipgloss.NewStyle().
				Background(lipgloss.Color("235")).
				Foreground(lipgloss.Color("8")).
				Padding(0, 1)
)

// ─── logEntry: a single line in the combined log panel ───────────────────────

type logEntry struct {
	ts    time.Time
	level string // "ERROR"|"WARN"|"INFO"|"DEBUG"|"AGENT"|"ORDER"
	text  string
}

func (e logEntry) render() string {
	ts := dimStyle.Render(e.ts.Format("15:04:05"))
	var badge string
	switch e.level {
	case "ERROR":
		badge = errStyle.Render("ERR")
	case "WARN":
		badge = warnStyle.Render("WRN")
	case "INFO":
		badge = infoStyle.Render("INF")
	case "DEBUG":
		badge = debugStyle.Render("DBG")
	case "AGENT":
		badge = agentStyle.Render("AGT")
	case "ORDER":
		badge = priceStyle.Render("ORD")
	default:
		badge = dimStyle.Render("   ")
	}
	return fmt.Sprintf("%s %s %s", ts, badge, e.text)
}

// ─── Model ────────────────────────────────────────────────────────────────────

// Model is the root Bubble Tea model.
type Model struct {
	width  int
	height int

	// Ticker tape: latest quote per symbol.
	quotes map[string]quoteState

	// Combined log (system + agent + orders), bounded.
	logs        []logEntry
	maxLogLines int

	// Latest heartbeat for the status bar.
	heartbeat    string
	heartbeatAt  time.Time

	// Input field for /ask.
	input        string
	inputActive  bool
	lastResponse string
}

type quoteState struct {
	symbol string
	mid    float64
	bid    float64
	ask    float64
	prevMid float64 // to show up/down colour
}

// New creates a fresh TUI model.
func New(maxLogLines int) Model {
	if maxLogLines <= 0 {
		maxLogLines = 100
	}
	return Model{
		quotes:      make(map[string]quoteState),
		maxLogLines: maxLogLines,
	}
}

// Init satisfies the tea.Model interface.
func (m Model) Init() tea.Cmd { return nil }

// Update processes messages and updates the model.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

	case QuoteMsg:
		sym := string(msg.Quote.Symbol)
		prev := m.quotes[sym]
		m.quotes[sym] = quoteState{
			symbol:  sym,
			mid:     msg.Quote.Mid.InexactFloat64(),
			bid:     msg.Quote.Bid.InexactFloat64(),
			ask:     msg.Quote.Ask.InexactFloat64(),
			prevMid: prev.mid,
		}

	case SysLogMsg:
		m.appendLog(logEntry{ts: msg.At, level: msg.Level, text: msg.Line})

	case AgentLogMsg:
		m.appendLog(logEntry{ts: time.Now(), level: "AGENT", text: msg.Line})

	case OrderMsg:
		m.appendLog(logEntry{ts: time.Now(), level: "ORDER", text: msg.Line})

	case HeartbeatMsg:
		m.heartbeat = msg.Line
		m.heartbeatAt = msg.At

	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyCtrlC, tea.KeyEsc:
			return m, tea.Quit
		case tea.KeyEnter:
			if m.inputActive && m.input != "" {
				m.lastResponse = "(Copilot not yet wired — Phase 6)"
				m.input = ""
				m.inputActive = false
			}
		case tea.KeyBackspace:
			if len(m.input) > 0 {
				m.input = m.input[:len(m.input)-1]
			}
		default:
			m.input += msg.String()
			m.inputActive = true
		}
	}
	return m, nil
}

func (m *Model) appendLog(e logEntry) {
	m.logs = append(m.logs, e)
	if len(m.logs) > m.maxLogLines {
		m.logs = m.logs[len(m.logs)-m.maxLogLines:]
	}
}

// View renders the full TUI layout.
//
// ┌─────────────────────────────────────────────────────┐
// │ BTCUSDT: 84,000.12  bid=...  ask=...  |  ETHUSDT:... │  ← ticker tape
// ├──────────────────────┬──────────────────────────────┤
// │ Active Positions     │  System & Agent Log          │  ← split panel
// │  ...                 │  15:04:05 ERR message...     │
// │                      │  15:04:08 AGT reasoning...   │
// ├──────────────────────┴──────────────────────────────┤
// │ ♥ 15:04:05  state=running  positions=2  candles=12  │  ← heartbeat bar
// │ /ask > _                                             │  ← input
// └─────────────────────────────────────────────────────┘
func (m Model) View() string {
	if m.width == 0 {
		return "Loading..."
	}

	ticker    := m.renderTicker()
	middle    := m.renderMiddle()
	statusBar := m.renderStatusBar()
	inputBar  := m.renderInput()

	return lipgloss.JoinVertical(lipgloss.Left,
		ticker, middle, statusBar, inputBar)
}

// ─── Panel renderers ─────────────────────────────────────────────────────────

func (m Model) renderTicker() string {
	if len(m.quotes) == 0 {
		return dimStyle.Render("  Waiting for market data…")
	}
	parts := make([]string, 0, len(m.quotes))
	for _, q := range m.quotes {
		mid := q.mid
		midStr := fmt.Sprintf("%.4f", mid)
		var midStyled string
		if q.prevMid > 0 && mid > q.prevMid {
			midStyled = priceStyle.Render("▲ " + midStr)
		} else if q.prevMid > 0 && mid < q.prevMid {
			midStyled = errStyle.Render("▼ " + midStr)
		} else {
			midStyled = midStr
		}
		parts = append(parts, fmt.Sprintf("%s %s  %s  %s",
			symStyle.Render(q.symbol),
			midStyled,
			dimStyle.Render(fmt.Sprintf("bid=%.4f", q.bid)),
			dimStyle.Render(fmt.Sprintf("ask=%.4f", q.ask)),
		))
	}
	return "  " + strings.Join(parts, "   │   ")
}

func (m Model) renderMiddle() string {
	posW := m.width / 3
	logW := m.width - posW - 2

	positions := borderStyle.Width(posW - 2).Render(m.renderPositions())
	logPanel  := borderStyle.Width(logW - 2).Render(m.renderLogPanel())

	return lipgloss.JoinHorizontal(lipgloss.Top, positions, logPanel)
}

func (m Model) renderPositions() string {
	header := headerStyle.Render("Active Positions")
	// TODO Phase 4: populate from broker.Positions()
	return header + "\n" + dimStyle.Render("  No open positions")
}

func (m Model) renderLogPanel() string {
	header := headerStyle.Render("System & Agent Log")
	if len(m.logs) == 0 {
		return header + "\n" + dimStyle.Render("  Waiting for activity…")
	}

	// Determine how many lines fit inside the border panel.
	// Leave 3 lines: header + border top/bottom.
	maxLines := m.height - 8
	if maxLines < 5 {
		maxLines = 5
	}
	if maxLines > 40 {
		maxLines = 40
	}

	visible := m.logs
	if len(visible) > maxLines {
		visible = visible[len(visible)-maxLines:]
	}

	rendered := make([]string, len(visible))
	for i, e := range visible {
		rendered[i] = e.render()
	}
	return header + "\n" + strings.Join(rendered, "\n")
}

func (m Model) renderStatusBar() string {
	if m.heartbeat == "" {
		return heartbeatStyle.Render("  ♥ waiting for first heartbeat…")
	}
	ts := heartbeatTsStyle.Render(m.heartbeatAt.Format("15:04:05"))
	body := heartbeatStyle.Render("  ♥  " + m.heartbeat)
	return lipgloss.JoinHorizontal(lipgloss.Left, ts, body)
}

func (m Model) renderInput() string {
	prompt := inputStyle.Render("/ask > ")
	cursor := "▌"
	if !m.inputActive {
		cursor = dimStyle.Render("▌")
	}
	line := prompt + m.input + cursor
	if m.lastResponse != "" {
		line += "\n" + dimStyle.Render("  → "+m.lastResponse)
	}
	return line
}

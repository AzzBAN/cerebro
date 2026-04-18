package tui

import (
	"context"
	"time"

	"github.com/azhar/cerebro/internal/domain"
	tea "github.com/charmbracelet/bubbletea"
)

// Model is the root Bubble Tea model.
type Model struct {
	width  int
	height int

	// Live clock
	now time.Time

	// Market watch
	quotes            map[string]quoteState
	watchScrollOffset int
	watchPageSize     int

	// Positions
	positionRows []domain.Position

	// Scrollable log
	logs        []logEntry
	maxLogLines int
	logScrollY  int // 0 = bottom (latest), increases = scroll up

	// Agent panel
	agentRuns     map[string]*agentRunState
	agentRunOrder []string
	spinnerFrame  int

	// Latest heartbeat for the status bar.
	heartbeat   string
	heartbeatAt time.Time

	// Input field for /ask.
	input        string
	inputActive  bool
	lastResponse string

	// Copilot /ask integration
	copilotFn   func(ctx context.Context, query string) (string, error)
	askLoading  bool
	askResponse string
	askQuery    string
}

type quoteState struct {
	symbol             string
	last               float64
	bid                float64
	ask                float64
	priceChange        float64
	priceChangePercent float64
	volume24h          float64
}

// New creates a fresh TUI model.
func New(maxLogLines int) Model {
	if maxLogLines <= 0 {
		maxLogLines = 500
	}
	return Model{
		quotes:      make(map[string]quoteState),
		agentRuns:   make(map[string]*agentRunState),
		maxLogLines: maxLogLines,
		logScrollY:  0,
		now:         time.Now(),
	}
}

// SetCopilotFn injects the copilot function for /ask queries.
func (m *Model) SetCopilotFn(fn func(ctx context.Context, query string) (string, error)) {
	m.copilotFn = fn
}

// Init satisfies the tea.Model interface.
func (m Model) Init() tea.Cmd {
	return tea.Batch(clockTick(), watchTick())
}

// Update processes messages and updates the model.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.recalculateLayout()

	case clockTickMsg:
		m.now = time.Now()
		m.spinnerFrame = (m.spinnerFrame + 1) % len(spinnerFrames)
		return m, clockTick()

	case watchTickMsg:
		symCount := len(m.quotes)
		if symCount > m.watchPageSize && m.watchPageSize > 0 {
			m.watchScrollOffset = (m.watchScrollOffset + m.watchPageSize) % symCount
		}
		return m, watchTick()

	case QuoteMsg:
		sym := string(msg.Quote.Symbol)
		existing := m.quotes[sym]
		qs := quoteState{
			symbol:             sym,
			last:               existing.last,
			bid:                existing.bid,
			ask:                existing.ask,
			priceChange:        existing.priceChange,
			priceChangePercent: existing.priceChangePercent,
			volume24h:          existing.volume24h,
		}
		if msg.Quote.Last.IsPositive() {
			qs.last = msg.Quote.Last.InexactFloat64()
		}
		if msg.Quote.Bid.IsPositive() {
			qs.bid = msg.Quote.Bid.InexactFloat64()
		}
		if msg.Quote.Ask.IsPositive() {
			qs.ask = msg.Quote.Ask.InexactFloat64()
		}
		if !msg.Quote.PriceChange.IsZero() {
			qs.priceChange = msg.Quote.PriceChange.InexactFloat64()
		}
		if !msg.Quote.PriceChangePercent.IsZero() {
			qs.priceChangePercent = msg.Quote.PriceChangePercent.InexactFloat64()
		}
		if !msg.Quote.Volume24h.IsZero() {
			qs.volume24h = msg.Quote.Volume24h.InexactFloat64()
		}
		m.quotes[sym] = qs
		m.recalculateLayout()

	case SysLogMsg:
		m.appendLog(logEntry{ts: msg.At, level: msg.Level, text: msg.Line})
		m.logScrollY = 0

	case AgentLogMsg:
		m.appendLog(logEntry{ts: time.Now(), level: "AGENT", text: msg.Line})
		m.logScrollY = 0

	case OrderMsg:
		m.appendLog(logEntry{ts: time.Now(), level: "ORDER", text: msg.Line})
		m.logScrollY = 0

	case PositionsMsg:
		m.positionRows = append([]domain.Position(nil), msg.Positions...)

	case AgentStateMsg:
		run, exists := m.agentRuns[msg.RunID]
		if !exists {
			run = &agentRunState{
				agent:    msg.Agent,
				runID:    msg.RunID,
				provider: msg.Provider,
				model:    msg.Model,
				symbol:   msg.Symbol,
				maxSteps: msg.MaxSteps,
				started:  msg.At,
			}
			m.agentRuns[msg.RunID] = run
			m.agentRunOrder = append(m.agentRunOrder, msg.RunID)
		}
		run.step = msg.Step
		run.toolName = msg.ToolName
		run.stepNum = msg.StepNum
		run.maxSteps = msg.MaxSteps
		if msg.Step == StepComplete || msg.Step == StepError {
			run.content = msg.Content
			run.finished = msg.At
			if msg.Step == StepError {
				run.err = msg.Content
			}
		}

	case HeartbeatMsg:
		m.heartbeat = msg.Line
		m.heartbeatAt = msg.At

	case AskResponseMsg:
		m.askLoading = false
		if msg.Err != nil {
			m.askResponse = "Error: " + msg.Err.Error()
		} else {
			m.askResponse = msg.Response
		}

	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyCtrlC:
			return m, tea.Quit
		case tea.KeyEsc:
			if m.askResponse != "" {
				m.askResponse = ""
				m.askQuery = ""
				return m, nil
			}
			return m, tea.Quit
		case tea.KeyEnter:
			if m.inputActive && m.input != "" {
				query := m.input
				if m.copilotFn != nil {
					m.askLoading = true
					m.askResponse = ""
					m.askQuery = query
					m.input = ""
					m.inputActive = false
					return m, newAskCmd(query, m.copilotFn)
				}
				m.lastResponse = "(Copilot not available — no LLM configured)"
				m.input = ""
				m.inputActive = false
			}
		case tea.KeyBackspace:
			if m.inputActive && len(m.input) > 0 {
				m.input = m.input[:len(m.input)-1]
			}
		case tea.KeyUp:
			if !m.inputActive {
				m.scrollLogUp(1)
			}
		case tea.KeyDown:
			if !m.inputActive {
				m.scrollLogDown(1)
			}
		case tea.KeyPgUp:
			if !m.inputActive {
				m.scrollLogUp(m.visibleLogLines())
			}
		case tea.KeyPgDown:
			if !m.inputActive {
				m.scrollLogDown(m.visibleLogLines())
			}
		default:
			if !m.inputActive && len(msg.String()) == 1 {
				m.inputActive = true
			}
			if m.inputActive {
				m.input += msg.String()
			}
		}
	}
	return m, nil
}

// ─── Tick commands ───────────────────────────────────────────────────────────

func clockTick() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg {
		return clockTickMsg(t)
	})
}

func watchTick() tea.Cmd {
	return tea.Tick(5*time.Second, func(t time.Time) tea.Msg {
		return watchTickMsg(t)
	})
}

// ─── Layout calculations ─────────────────────────────────────────────────────

// recalculateLayout computes derived sizes from the current terminal dimensions.
func (m *Model) recalculateLayout() {
	if m.height == 0 || m.width == 0 {
		return
	}

	symCount := len(m.quotes)
	watchRows := symCount + 1 // +1 for header row
	if watchRows == 1 {
		watchRows = 2 // minimum: header + 1 empty row
	}
	if watchRows > maxWatchLines+1 {
		watchRows = maxWatchLines + 1
	}
	m.watchPageSize = watchRows - 1 // symbols per page (excluding header)

	m.clampLogScroll()
}

// visibleLogLines returns how many log lines fit in the log viewport.
func (m *Model) visibleLogLines() int {
	h := m.logContentHeight()
	if h < 1 {
		h = 1
	}
	return h
}

// logContentHeight returns the number of log lines that fit inside the bordered log panel.
func (m *Model) logContentHeight() int {
	middleH := m.middleHeight()
	h := middleH - 2 - 1
	if h < 1 {
		h = 1
	}
	return h
}

func (m *Model) clampLogScroll() {
	max := len(m.logs)
	visible := m.visibleLogLines()
	if max <= visible {
		m.logScrollY = 0
		return
	}
	maxScroll := max - visible
	if m.logScrollY > maxScroll {
		m.logScrollY = maxScroll
	}
	if m.logScrollY < 0 {
		m.logScrollY = 0
	}
}

func (m *Model) scrollLogUp(n int) {
	m.logScrollY += n
	m.clampLogScroll()
}

func (m *Model) scrollLogDown(n int) {
	m.logScrollY -= n
	m.clampLogScroll()
}

func (m *Model) appendLog(e logEntry) {
	m.logs = append(m.logs, e)
	if len(m.logs) > m.maxLogLines {
		m.logs = m.logs[len(m.logs)-m.maxLogLines:]
	}
}

// computedWatchH returns the outer height of the watch panel.
func (m *Model) computedWatchH() int {
	symCount := len(m.quotes)
	rows := symCount + 1
	if rows < 2 {
		rows = 2
	}
	if rows > maxWatchLines+1 {
		rows = maxWatchLines + 1
	}
	return 2 + 1 + rows // border(2) + panel header(1) + content rows
}

// computedAgentPanelH returns the dynamic outer height of the agent panel.
func (m *Model) computedAgentPanelH() int {
	activeCount := 0
	hasCompleted := false
	for _, id := range m.agentRunOrder {
		run := m.agentRuns[id]
		if run.step == StepComplete || run.step == StepError {
			hasCompleted = true
		} else {
			activeCount++
		}
	}

	contentLines := activeCount
	if hasCompleted {
		contentLines += 2 // summary line + at least 1 result line
	}
	if contentLines == 0 {
		contentLines = 1 // "No agent activity"
	}

	h := 2 + 1 + contentLines // border(2) + header(1) + content
	if h < minAgentPanelH {
		h = minAgentPanelH
	}
	if h > maxAgentPanelH {
		h = maxAgentPanelH
	}
	return h
}

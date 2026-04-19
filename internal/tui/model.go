package tui

import (
	"context"
	"strings"
	"time"

	"github.com/azhar/cerebro/internal/domain"
	tea "github.com/charmbracelet/bubbletea"
)

// panelFocus tracks which panel receives scroll input.
type panelFocus int

const (
	focusNone  panelFocus = iota
	focusWatch
	focusLog
)

// Model is the root Bubble Tea model.
type Model struct {
	width  int
	height int

	// Live clock
	now time.Time

	// Market watch
	quotes       map[string]quoteState
	watchScrollY int        // vertical scroll offset into sorted symbol list
	watchScrollX int        // horizontal scroll offset for column overflow
	focusedPanel panelFocus // which panel receives scroll input

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
	askScrollY  int // 0 = bottom, increases = scroll up
	askLines    int // total rendered lines of current response
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
	return clockTick()
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
		if msg.Description != "" {
			run.description = msg.Description
		}
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
		m.askScrollY = 0
		if msg.Err != nil {
			m.askResponse = "Error: " + msg.Err.Error()
		} else {
			m.askResponse = msg.Response
		}
		m.askLines = countRenderedLines(m.askResponse, max(20, m.width-borderH))

	case tea.KeyMsg:
		if m.askResponse != "" {
			switch msg.Type {
			case tea.KeyCtrlC:
				return m, tea.Quit
			case tea.KeyEsc:
				m.askResponse = ""
				m.askQuery = ""
				m.askScrollY = 0
				m.askLines = 0
				return m, nil
			case tea.KeyUp:
				m.scrollAskUp(1)
				return m, nil
			case tea.KeyDown:
				m.scrollAskDown(1)
				return m, nil
			case tea.KeyPgUp:
				m.scrollAskUp(maxAskResponseLines)
				return m, nil
			case tea.KeyPgDown:
				m.scrollAskDown(maxAskResponseLines)
				return m, nil
			}
		}
		switch msg.Type {
		case tea.KeyCtrlC:
			return m, tea.Quit
		case tea.KeyEsc:
			return m, tea.Quit
		case tea.KeyTab:
			m.focusedPanel = (m.focusedPanel + 1) % 3
			return m, nil
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
				if m.focusedPanel == focusWatch {
					m.scrollWatchUp(1)
				} else {
					m.scrollLogUp(1)
				}
			}
		case tea.KeyDown:
			if !m.inputActive {
				if m.focusedPanel == focusWatch {
					m.scrollWatchDown(1)
				} else {
					m.scrollLogDown(1)
				}
			}
		case tea.KeyPgUp:
			if !m.inputActive {
				if m.focusedPanel == focusWatch {
					m.scrollWatchUp(maxWatchLines)
				} else {
					m.scrollLogUp(m.visibleLogLines())
				}
			}
		case tea.KeyPgDown:
			if !m.inputActive {
				if m.focusedPanel == focusWatch {
					m.scrollWatchDown(maxWatchLines)
				} else {
					m.scrollLogDown(m.visibleLogLines())
				}
			}
		case tea.KeyLeft:
			if !m.inputActive && m.focusedPanel == focusWatch {
				m.scrollWatchLeft(watchScrollXStep)
			}
		case tea.KeyRight:
			if !m.inputActive && m.focusedPanel == focusWatch {
				m.scrollWatchRight(watchScrollXStep)
			}
		default:
			if !m.inputActive && len(msg.String()) == 1 {
				m.inputActive = true
			}
			if m.inputActive {
				m.input += msg.String()
			}
		}

	case tea.MouseMsg:
		switch msg.Type {
		case tea.MouseLeft:
			m.handleMouseClick(msg.X, msg.Y)
		case tea.MouseWheelUp:
			if m.focusedPanel == focusWatch {
				m.scrollWatchUp(1)
			} else {
				m.scrollLogUp(3)
			}
		case tea.MouseWheelDown:
			if m.focusedPanel == focusWatch {
				m.scrollWatchDown(1)
			} else {
				m.scrollLogDown(3)
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

// ─── Layout calculations ─────────────────────────────────────────────────────

// recalculateLayout computes derived sizes from the current terminal dimensions.
func (m *Model) recalculateLayout() {
	if m.height == 0 || m.width == 0 {
		return
	}

	m.clampWatchScrollY()
	m.clampWatchScrollX()
	m.clampLogScroll()
	m.clampAskScroll()
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

// ─── Watch panel scroll helpers ──────────────────────────────────────────────

func (m *Model) clampWatchScrollY() {
	symCount := len(m.quotes)
	if symCount <= maxWatchLines {
		m.watchScrollY = 0
		return
	}
	maxScroll := symCount - maxWatchLines
	if m.watchScrollY > maxScroll {
		m.watchScrollY = maxScroll
	}
	if m.watchScrollY < 0 {
		m.watchScrollY = 0
	}
}

func (m *Model) clampWatchScrollX() {
	availW := m.watchContentWidth()
	totalW := watchTotalContentWidth()
	if totalW <= availW {
		m.watchScrollX = 0
		return
	}
	maxScroll := totalW - availW
	if m.watchScrollX > maxScroll {
		m.watchScrollX = maxScroll
	}
	if m.watchScrollX < 0 {
		m.watchScrollX = 0
	}
}

func (m *Model) scrollWatchUp(n int) {
	m.watchScrollY += n
	m.clampWatchScrollY()
}

func (m *Model) scrollWatchDown(n int) {
	m.watchScrollY -= n
	m.clampWatchScrollY()
}

func (m *Model) scrollWatchLeft(n int) {
	m.watchScrollX -= n
	m.clampWatchScrollX()
}

func (m *Model) scrollWatchRight(n int) {
	m.watchScrollX += n
	m.clampWatchScrollX()
}

// watchContentWidth returns the available content width inside the watch panel border.
func (m *Model) watchContentWidth() int {
	return m.width - borderH
}

// watchTotalContentWidth returns the total width of all watch columns combined.
func watchTotalContentWidth() int {
	return colSymbol + colLast + colChg + colBidAsk + colSpread + colVol
}

// handleMouseClick sets panel focus based on where the user clicked.
func (m *Model) handleMouseClick(x, y int) {
	// Layout: header(1) | watch | middle | agent | status(1) | input(1)
	headerH := 1
	watchH := m.computedWatchH()
	agentH := m.computedAgentPanelH()
	statusH := 1
	inputH := 1
	askH := 0
	if m.askResponse != "" {
		askH = askResponseH
		maxAskH := m.height * 2 / 5
		if askH > maxAskH && maxAskH > 5 {
			askH = maxAskH
		}
	}
	middleH := m.height - headerH - watchH - agentH - statusH - inputH - askH

	watchStart := headerH
	watchEnd := watchStart + watchH
	middleStart := watchEnd
	middleEnd := middleStart + middleH

	if y >= watchStart && y < watchEnd {
		m.focusedPanel = focusWatch
	} else if y >= middleStart && y < middleEnd {
		m.focusedPanel = focusLog
	}
}

// scrollAskUp scrolls the ask response panel up by n lines.
func (m *Model) scrollAskUp(n int) {
	m.askScrollY += n
	m.clampAskScroll()
}

// scrollAskDown scrolls the ask response panel down by n lines.
func (m *Model) scrollAskDown(n int) {
	m.askScrollY -= n
	m.clampAskScroll()
}

func (m *Model) clampAskScroll() {
	if m.askLines <= maxAskResponseLines {
		m.askScrollY = 0
		return
	}
	maxScroll := m.askLines - maxAskResponseLines
	if m.askScrollY > maxScroll {
		m.askScrollY = maxScroll
	}
	if m.askScrollY < 0 {
		m.askScrollY = 0
	}
}

func (m *Model) appendLog(e logEntry) {
	m.logs = append(m.logs, e)
	if len(m.logs) > m.maxLogLines {
		m.logs = m.logs[len(m.logs)-m.maxLogLines:]
	}
}

// computedWatchH returns the outer height of the watch panel.
// Always uses maxWatchLines to keep a stable viewport height.
func (m *Model) computedWatchH() int {
	return 2 + 1 + 1 + maxWatchLines // border(2) + panel header(1) + table header(1) + data rows
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

	contentLines := activeCount * 2
	if hasCompleted {
		contentLines += 2
	}
	if contentLines == 0 {
		contentLines = 1
	}

	h := 2 + 1 + contentLines
	if h < minAgentPanelH {
		h = minAgentPanelH
	}
	if h > maxAgentPanelH {
		h = maxAgentPanelH
	}
	return h
}

// countRenderedLines estimates how many terminal lines a markdown string will
// occupy after glamour rendering, given the available content width.
func countRenderedLines(s string, width int) int {
	if s == "" {
		return 0
	}
	lines := strings.Split(s, "\n")
	count := 0
	for _, line := range lines {
		if width > 0 && len(line) > width {
			count += (len(line) + width - 1) / width
		} else {
			count++
		}
	}
	return count
}

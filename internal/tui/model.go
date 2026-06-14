package tui

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/azhar/cerebro/internal/domain"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/shopspring/decimal"
)

// panelFocus tracks which panel receives scroll input.
type panelFocus int

const (
	focusNone panelFocus = iota
	focusWatch
	focusLog
	focusPositions
	focusBias
	focusAgentRuns
	focusAgentActivity
	focusMacro
)

// inputMaxLines caps how tall the copilot input box grows as the user types
// or pastes multi-line content. Beyond this the textarea scrolls internally.
const inputMaxLines = 6

// Model is the root Bubble Tea model.
type Model struct {
	width  int
	height int

	// Live clock
	now time.Time

	// Main tab navigation (Dashboard, Market, Logs, Agents).
	activeTab mainTab

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
	agentRuns            map[string]*agentRunState
	agentRunOrder        []string
	spinnerFrame         int
	agentRunsScrollY     int // scroll offset for the Agent Runs panel
	agentActivityScrollY int // scroll offset for the Agent Activity panel

	// Scroll offsets for additional panels.
	posScrollY  int // scroll offset for Positions panel
	biasScrollY int // scroll offset for Bias panel

	// Latest heartbeat for the status bar.
	heartbeat   string
	heartbeatAt time.Time

	// Input field for /ask, backed by bubbles/textarea for full editing
	// support (paste, word-delete, ctrl+u/ctrl+w, cursor movement, newlines).
	ta           textarea.Model
	lastResponse string

	// Copilot /ask integration
	copilotFn   func(ctx context.Context, query string) (string, error)
	askLoading  bool
	askResponse string
	askQuery    string
	askScrollY  int // 0 = bottom, increases = scroll up
	askLines    int // total rendered lines of current response

	// Mouse mode toggle: enabled = scroll wheel captured by TUI;
	// disabled = terminal handles mouse (allows text selection to copy).
	mouseEnabled bool

	// Drag-to-copy selection state. Active only when mouseEnabled and a
	// left-button press lands inside a panel. selRect is the bounding box
	// of the locked panel; selStart and selEnd are inclusive cell
	// coordinates clamped to that rect.
	selecting bool
	selStart  point
	selEnd    point
	selRect   rect

	// Last clipboard copy notice for the status bar.
	copyNotice   string
	copyNoticeAt time.Time

	// Bias / Signals panel state (MD breakpoint and up).
	biasResults map[domain.Symbol]domain.BiasResult
	biasOrder   []domain.Symbol

	// Macro panel state (MD breakpoint and up).
	macro    MacroSnapshot
	macroSet bool

	// News panel state (LG breakpoint and up). Populated from CryptoPanic.
	news    NewsSnapshot
	newsSet bool

	// Latest LLM daily-budget snapshot for the status bar chip.
	budget    BudgetSnapshot
	budgetSet bool

	// XS layout: which panel is currently visible. Cycled via number keys.
	xsTab int
}

type quoteState struct {
	symbol             string
	last               decimal.Decimal
	bid                decimal.Decimal
	ask                decimal.Decimal
	priceChange        decimal.Decimal
	priceChangePercent decimal.Decimal
	volume24h          decimal.Decimal
}

// New creates a fresh TUI model.
func New(maxLogLines int) Model {
	if maxLogLines <= 0 {
		maxLogLines = 500
	}

	return Model{
		quotes:       make(map[string]quoteState),
		agentRuns:    make(map[string]*agentRunState),
		biasResults:  make(map[domain.Symbol]domain.BiasResult),
		maxLogLines:  maxLogLines,
		logScrollY:   0,
		now:          time.Now(),
		mouseEnabled: true,
		ta:           newInputTextarea(),
	}
}

// newInputTextarea builds the copilot input field. Enter submits the query;
// newlines are inserted with ctrl+j / alt+enter, which terminals deliver
// reliably (shift+enter is not portable).
func newInputTextarea() textarea.Model {
	ta := textarea.New()
	ta.Placeholder = "Ask the copilot..."
	ta.Prompt = "❯ "
	ta.ShowLineNumbers = false
	ta.CharLimit = 0
	ta.MaxHeight = inputMaxLines
	ta.SetHeight(1)
	ta.KeyMap.InsertNewline = key.NewBinding(
		key.WithKeys("ctrl+j", "alt+enter"),
		key.WithHelp("ctrl+j", "newline"),
	)
	ta.FocusedStyle.CursorLine = lipgloss.NewStyle()
	ta.BlurredStyle.CursorLine = lipgloss.NewStyle()
	return ta
}

// SetCopilotFn injects the copilot function for /ask queries.
func (m *Model) SetCopilotFn(fn func(ctx context.Context, query string) (string, error)) {
	m.copilotFn = fn
}

// Init satisfies the tea.Model interface.
func (m Model) Init() tea.Cmd {
	return tea.Batch(clockTick(), tea.EnableMouseCellMotion)
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
			qs.last = msg.Quote.Last
		}
		if msg.Quote.Bid.IsPositive() {
			qs.bid = msg.Quote.Bid
		}
		if msg.Quote.Ask.IsPositive() {
			qs.ask = msg.Quote.Ask
		}
		if !msg.Quote.PriceChange.IsZero() {
			qs.priceChange = msg.Quote.PriceChange
		}
		if !msg.Quote.PriceChangePercent.IsZero() {
			qs.priceChangePercent = msg.Quote.PriceChangePercent
		}
		if !msg.Quote.Volume24h.IsZero() {
			qs.volume24h = msg.Quote.Volume24h
		}
		m.quotes[sym] = qs

		// Real-time refresh of CurrentPrice on any open position matching this
		// symbol so the Active Positions panel (price + unrealized PnL/ROI)
		// updates on every WS tick instead of waiting for the 10s heartbeat.
		// Prefer Last; fall back to Mid when Last is missing.
		if msg.Quote.Last.IsPositive() || msg.Quote.Mid.IsPositive() {
			price := msg.Quote.Last
			if !price.IsPositive() {
				price = msg.Quote.Mid
			}
			for i := range m.positionRows {
				if m.positionRows[i].Symbol == msg.Quote.Symbol {
					m.positionRows[i].CurrentPrice = price
				}
			}
		}

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

	case BiasUpdatedMsg:
		sym := msg.Result.Symbol
		if _, exists := m.biasResults[sym]; !exists {
			m.biasOrder = append(m.biasOrder, sym)
		}
		m.biasResults[sym] = msg.Result

	case MacroSnapshotMsg:
		m.macro = msg.Snapshot
		m.macroSet = true

	case NewsSnapshotMsg:
		m.news = msg.Snapshot
		m.newsSet = true

	case BudgetSnapshotMsg:
		m.budget = msg.Snapshot
		m.budgetSet = true

	case AskResponseMsg:
		m.askLoading = false
		m.askScrollY = 0
		if msg.Err != nil {
			m.askResponse = "Error: " + msg.Err.Error()
		} else {
			m.askResponse = msg.Response
		}
		m.askLines = countRenderedLines(m.askResponse, max(20, m.width-frameH))

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
		}

		// When the input is focused, the textarea owns keystrokes — typing,
		// paste, ctrl+u, ctrl+w / alt-backspace word delete, ctrl+a/ctrl+e,
		// and cursor movement all work natively. Only Esc (blur) and Enter
		// (submit) are intercepted; ctrl+j / alt+enter insert a newline.
		if m.ta.Focused() {
			switch msg.Type {
			case tea.KeyEsc:
				m.ta.Blur()
				m.ta.Reset()
				m.syncInputHeight()
				return m, nil
			case tea.KeyTab:
				// Leave chat mode and advance to the next tab. The draft is
				// kept (only blurred) so the user can return and resume typing.
				m.ta.Blur()
				m.cycleTab(true)
				return m, nil
			case tea.KeyShiftTab:
				m.ta.Blur()
				m.cycleTab(false)
				return m, nil
			case tea.KeyEnter:
				query := strings.TrimSpace(m.ta.Value())
				if query == "" {
					return m, nil
				}
				if m.copilotFn != nil {
					m.askLoading = true
					m.askResponse = ""
					m.askQuery = query
				} else {
					m.lastResponse = "(Copilot not available — no LLM configured)"
				}
				m.ta.Reset()
				m.ta.Blur()
				m.syncInputHeight()
				if m.copilotFn != nil {
					return m, newAskCmd(query, m.copilotFn)
				}
				return m, nil
			default:
				var cmd tea.Cmd
				m.ta, cmd = m.ta.Update(msg)
				m.syncInputHeight()
				return m, cmd
			}
		}

		// Input not focused: keys drive navigation and panel scrolling.
		switch msg.Type {
		case tea.KeyEsc:
			return m, tea.Quit
		case tea.KeyTab:
			m.cycleTab(true)
			return m, nil
		case tea.KeyShiftTab:
			m.cycleTab(false)
			return m, nil
		case tea.KeyCtrlO:
			m.mouseEnabled = !m.mouseEnabled
			if m.mouseEnabled {
				return m, tea.EnableMouseCellMotion
			}
			// Disabling mouse mode also cancels any in-flight selection.
			m.selecting = false
			return m, tea.DisableMouse
		case tea.KeyUp:
			if m.focusedPanel == focusWatch {
				m.scrollWatchUp(1)
			} else {
				m.scrollLogUp(1)
			}
		case tea.KeyDown:
			if m.focusedPanel == focusWatch {
				m.scrollWatchDown(1)
			} else {
				m.scrollLogDown(1)
			}
		case tea.KeyPgUp:
			if m.focusedPanel == focusWatch {
				m.scrollWatchUp(maxWatchLines)
			} else {
				m.scrollLogUp(m.visibleLogLines())
			}
		case tea.KeyPgDown:
			if m.focusedPanel == focusWatch {
				m.scrollWatchDown(maxWatchLines)
			} else {
				m.scrollLogDown(m.visibleLogLines())
			}
		case tea.KeyLeft:
			if m.focusedPanel == focusWatch {
				m.scrollWatchLeft(watchScrollXStep)
			}
		case tea.KeyRight:
			if m.focusedPanel == focusWatch {
				m.scrollWatchRight(watchScrollXStep)
			}
		default:
			// Number keys 1-4 switch tabs without entering input mode.
			if m.breakpoint() != bpXS {
				ch := msg.String()
				if len(ch) == 1 && ch[0] >= '1' && ch[0] <= '4' {
					m.activeTab = mainTab(ch[0] - '1')
					return m, nil
				}
			}
			// Any other printable key focuses the input and forwards the
			// keystroke so the first character is not dropped.
			if len(msg.String()) == 1 {
				focusCmd := m.ta.Focus()
				var taCmd tea.Cmd
				m.ta, taCmd = m.ta.Update(msg)
				m.syncInputHeight()
				return m, tea.Batch(focusCmd, taCmd)
			}
		}

	case tea.MouseMsg:
		// Disambiguate left-click press vs. drag motion. Bubble Tea reports
		// drag as Type=MouseLeft + Action=MouseActionMotion.
		if msg.Type == tea.MouseLeft && msg.Action == tea.MouseActionMotion {
			if m.selecting {
				p := clampToRect(point{msg.X, msg.Y}, m.selRect)
				m.selEnd = p
			}
			return m, nil
		}

		panel := m.panelAtPosition(msg.X, msg.Y)
		switch msg.Type {
		case tea.MouseLeft:
			m.focusedPanel = panel
			// Begin a panel-scoped selection when mouse mode is on and the
			// click landed inside a real panel. Single clicks (no drag) end
			// up with selStart == selEnd and produce no copy on release.
			if m.mouseEnabled && panel != focusNone {
				r := m.panelRect(panel)
				if !r.empty() {
					p := clampToRect(point{msg.X, msg.Y}, r)
					m.selecting = true
					m.selStart = p
					m.selEnd = p
					m.selRect = r
				}
			}
		case tea.MouseRelease:
			if m.selecting {
				m.selecting = false
				if m.selStart != m.selEnd {
					// Render the base view (without overlay) to extract clean
					// text from the selection rectangle.
					base := m.View()
					x0, y0, x1, y1 := normalizeSelection(m.selStart, m.selEnd)
					text := extractRectFromView(base, x0, y0, x1, y1)
					m.selStart, m.selEnd = point{}, point{}
					if cmd := copyToClipboardCmd(text); cmd != nil {
						return m, cmd
					}
				}
				m.selStart, m.selEnd = point{}, point{}
			}
		case tea.MouseWheelUp:
			m.scrollPanelUp(panel, 3)
		case tea.MouseWheelDown:
			m.scrollPanelDown(panel, 3)
		}

	case clipboardCopiedMsg:
		if msg.err != nil {
			m.copyNotice = "copy failed"
		} else {
			m.copyNotice = formatCopyNotice(msg.chars)
		}
		m.copyNoticeAt = time.Now()
	}
	return m, nil
}

// formatCopyNotice formats a brief "copied N chars" message for the status bar.
func formatCopyNotice(n int) string {
	if n == 1 {
		return "copied 1 char"
	}
	return "copied " + strconv.Itoa(n) + " chars"
}

// ─── Tick commands ───────────────────────────────────────────────────────────

func clockTick() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg {
		return clockTickMsg(t)
	})
}

// cycleTab advances the active tab forward (true) or backward (false),
// honoring the XS breakpoint's separate tab set. Shared by the focused-input
// and navigation key paths so Tab behaves identically in both.
func (m *Model) cycleTab(forward bool) {
	if m.breakpoint() == bpXS {
		if forward {
			m.xsTab = (m.xsTab + 1) % xsTabCount
		} else {
			m.xsTab = (m.xsTab + xsTabCount - 1) % xsTabCount
		}
		return
	}
	if forward {
		m.activeTab = (m.activeTab + 1) % tabCount
	} else {
		m.activeTab = (m.activeTab + tabCount - 1) % tabCount
	}
}

// ─── Layout calculations ─────────────────────────────────────────────────────

// recalculateLayout computes derived sizes from the current terminal dimensions.
func (m *Model) recalculateLayout() {
	if m.height == 0 || m.width == 0 {
		return
	}

	// Keep the input textarea sized to the terminal (border + padding budget).
	m.ta.SetWidth(m.width - frameH)
	m.syncInputHeight()

	m.clampWatchScrollY()
	m.clampWatchScrollX()
	m.clampLogScroll()
	m.clampAskScroll()
}

// syncInputHeight grows the input box with its content up to inputMaxLines,
// after which the textarea scrolls internally.
func (m *Model) syncInputHeight() {
	h := m.ta.LineCount()
	if h < 1 {
		h = 1
	}
	if h > inputMaxLines {
		h = inputMaxLines
	}
	m.ta.SetHeight(h)
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

// watchContentWidth returns the available content area inside the watch
// panel — the actual cell width available for text after subtracting the
// full horizontal frame (border + padding) of the bordered panel.
func (m *Model) watchContentWidth() int {
	return m.width - frameH
}

// watchTotalContentWidth returns the total width of all watch columns combined.
func watchTotalContentWidth() int {
	return colSymbol + colLast + colChg + colBidAsk + colSpread + colVol + colBias
}

// panelAtPosition returns which panel the mouse cursor is over, based on the
// current layout structure.
func (m *Model) panelAtPosition(x, y int) panelFocus {
	if m.activeTab != tabDashboard || m.breakpoint() == bpXS {
		// For non-dashboard tabs or XS, route everything to the dominant panel.
		switch m.activeTab {
		case tabMarket:
			return focusWatch
		case tabLogs:
			return focusLog
		case tabAgents:
			return focusAgentActivity
		}
		return focusLog
	}

	// Dashboard layout: header(1) | tabBar(1) | watch | middle | agent | status(1) | input(1)
	headerH := 1
	tabBarH := 1
	watchH := m.computedWatchH()
	agentH := m.computedAgentPanelH()
	bodyH := m.bodyHeight()
	middleH := bodyH - watchH - agentH
	if middleH < 0 {
		middleH = 0
	}

	watchStart := headerH + tabBarH
	watchEnd := watchStart + watchH
	middleStart := watchEnd
	middleEnd := middleStart + middleH
	agentStart := middleEnd
	agentEnd := agentStart + agentH

	if y >= watchStart && y < watchEnd {
		return focusWatch
	}
	if y >= agentStart && y < agentEnd {
		return focusAgentActivity
	}
	if y >= middleStart && y < middleEnd {
		// Determine which column the cursor is in.
		return m.middleColumnAtX(x)
	}
	return focusNone
}

// middleColumnAtX maps an X coordinate to the panel inside the middle section.
// Column widths must match the layout functions (renderMiddleSM/MD/LG/XL).
func (m *Model) middleColumnAtX(x int) panelFocus {
	bp := m.breakpoint()
	switch bp {
	case bpSM:
		total := m.width - 2*borderH
		c1Outer := total/3 + borderH
		if x < c1Outer {
			return focusPositions
		}
		return focusLog

	case bpMD:
		total := m.width - 3*borderH
		c1Outer := total*22/100 + borderH
		c2Outer := total*28/100 + borderH
		if x < c1Outer {
			return focusPositions
		}
		if x < c1Outer+c2Outer {
			return focusBias
		}
		return focusLog

	case bpLG:
		total := m.width - 4*borderH
		c1Outer := total*20/100 + borderH
		c2Outer := total*22/100 + borderH
		c4Outer := total*22/100 + borderH
		c3Outer := m.width - c1Outer - c2Outer - c4Outer
		if x < c1Outer {
			return focusPositions
		}
		if x < c1Outer+c2Outer {
			return focusBias
		}
		if x < c1Outer+c2Outer+c3Outer {
			return focusLog
		}
		return focusMacro

	case bpXL:
		total := m.width - 5*borderH
		c1Outer := total*16/100 + borderH
		c2Outer := total*20/100 + borderH
		c4Outer := total*20/100 + borderH
		c5Outer := total*16/100 + borderH
		c3Outer := m.width - c1Outer - c2Outer - c4Outer - c5Outer
		if x < c1Outer {
			return focusPositions
		}
		if x < c1Outer+c2Outer {
			return focusBias
		}
		if x < c1Outer+c2Outer+c3Outer {
			return focusLog
		}
		if x < c1Outer+c2Outer+c3Outer+c4Outer {
			return focusMacro
		}
		return focusNone
	}
	return focusLog
}

// scrollPanelUp scrolls the given panel up by n lines.
func (m *Model) scrollPanelUp(panel panelFocus, n int) {
	switch panel {
	case focusWatch:
		m.scrollWatchUp(1)
	case focusLog:
		m.scrollLogUp(n)
	case focusPositions:
		m.posScrollY += n
		m.clampPosScroll()
	case focusBias:
		m.biasScrollY += n
		m.clampBiasScroll()
	case focusAgentRuns:
		m.agentRunsScrollY += n
		m.clampAgentRunsScroll()
	case focusAgentActivity:
		m.agentActivityScrollY += n
		m.clampAgentActivityScroll()
	case focusMacro:
		// Macro panel is too small to scroll meaningfully.
	}
}

// scrollPanelDown scrolls the given panel down by n lines.
func (m *Model) scrollPanelDown(panel panelFocus, n int) {
	switch panel {
	case focusWatch:
		m.scrollWatchDown(1)
	case focusLog:
		m.scrollLogDown(n)
	case focusPositions:
		m.posScrollY -= n
		m.clampPosScroll()
	case focusBias:
		m.biasScrollY -= n
		m.clampBiasScroll()
	case focusAgentRuns:
		m.agentRunsScrollY -= n
		m.clampAgentRunsScroll()
	case focusAgentActivity:
		m.agentActivityScrollY -= n
		m.clampAgentActivityScroll()
	case focusMacro:
		// Macro panel is too small to scroll meaningfully.
	}
}

func (m *Model) clampPosScroll() {
	total := len(m.positionRows) * 6 // ~6 lines per position
	visible := 8
	if total <= visible {
		m.posScrollY = 0
		return
	}
	maxS := total - visible
	if m.posScrollY > maxS {
		m.posScrollY = maxS
	}
	if m.posScrollY < 0 {
		m.posScrollY = 0
	}
}

func (m *Model) clampBiasScroll() {
	total := len(m.biasOrder)
	visible := maxBiasRows
	if total <= visible {
		m.biasScrollY = 0
		return
	}
	maxS := total - visible
	if m.biasScrollY > maxS {
		m.biasScrollY = maxS
	}
	if m.biasScrollY < 0 {
		m.biasScrollY = 0
	}
}

func (m *Model) clampAgentRunsScroll() {
	total := len(m.agentRunOrder)
	visible := maxAgentRunRows
	if total <= visible {
		m.agentRunsScrollY = 0
		return
	}
	maxS := total - visible
	if m.agentRunsScrollY > maxS {
		m.agentRunsScrollY = maxS
	}
	if m.agentRunsScrollY < 0 {
		m.agentRunsScrollY = 0
	}
}

func (m *Model) clampAgentActivityScroll() {
	total := 0
	for _, id := range m.agentRunOrder {
		run := m.agentRuns[id]
		if run.step == StepComplete || run.step == StepError {
			total += 2
		} else {
			total += 2
		}
	}
	visible := 6
	if total <= visible {
		m.agentActivityScrollY = 0
		return
	}
	maxS := total - visible
	if m.agentActivityScrollY > maxS {
		m.agentActivityScrollY = maxS
	}
	if m.agentActivityScrollY < 0 {
		m.agentActivityScrollY = 0
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

// breakpoint returns the active layout breakpoint based on current dimensions.
// The check is monotonic: each tier requires both width AND height to clear its
// minimums; otherwise we fall back to the next smaller tier.
func (m *Model) breakpoint() breakpoint {
	switch {
	case m.width >= bpXLMinWidth && m.height >= bpXLMinHeight:
		return bpXL
	case m.width >= bpLGMinWidth && m.height >= bpLGMinHeight:
		return bpLG
	case m.width >= bpMDMinWidth && m.height >= bpMDMinHeight:
		return bpMD
	case m.width >= bpSMMinWidth && m.height >= bpSMMinHeight:
		return bpSM
	default:
		return bpXS
	}
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

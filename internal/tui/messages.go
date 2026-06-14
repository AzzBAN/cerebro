package tui

import (
	"context"
	"time"

	"github.com/azhar/cerebro/internal/domain"
	"github.com/azhar/cerebro/internal/marketdata"
	"github.com/azhar/cerebro/internal/uistate"
	tea "github.com/charmbracelet/bubbletea"
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

// PositionsMsg carries the latest open-position snapshot.
type PositionsMsg struct{ Positions []domain.Position }

// watchTickMsg is fired every 5 seconds to rotate the market watch panel.
// Removed: replaced with manual scrolling.

// clockTickMsg is fired every second to update the live clock.
type clockTickMsg time.Time

// AgentStep represents the current step in an agent's ReAct loop.
// Aliased to the neutral uistate type so the TUI and web surfaces share one
// definition.
type AgentStep = uistate.AgentStep

const (
	StepThinking  = uistate.StepThinking
	StepTool      = uistate.StepTool
	StepObserving = uistate.StepObserving
	StepStreaming = uistate.StepStreaming
	StepComplete  = uistate.StepComplete
	StepError     = uistate.StepError
)

// AgentStateMsg updates the live state of a running agent in the agent panel.
// It is an alias of uistate.AgentState so *tui.Runner satisfies uistate.Sink
// and the same value doubles as a tea.Msg in the Update loop.
type AgentStateMsg = uistate.AgentState

// ─── Main tab identifiers ────────────────────────────────────────────────────

// mainTab identifies which top-level tab the user is viewing.
type mainTab int

const (
	tabDashboard mainTab = iota
	tabMarket
	tabLogs
	tabAgents
	tabCount // sentinel — must be last
)

// mainTabLabels are the display labels for each tab.
var mainTabLabels = []string{"Dashboard", "Market", "Logs", "Agents"}

// mainTabIcons provides a visual icon prefix for each tab.
var mainTabIcons = []string{"◈", "◉", "▤", "⚙"}

// ─── Lipgloss styles ─────────────────────────────────────────────────────────

// Color palette — subtle, professional dark theme.
var (
	colorBg       = lipgloss.Color("235") // dark grey background
	colorBgAlt    = lipgloss.Color("236") // slightly lighter for contrast
	colorFg       = lipgloss.Color("252") // light grey text
	colorFgDim    = lipgloss.Color("242") // dimmed text
	colorAccent   = lipgloss.Color("75")  // cyan-blue accent
	colorAccentDim = lipgloss.Color("67") // muted accent
	colorGreen    = lipgloss.Color("114") // green for positive values
	colorRed      = lipgloss.Color("203") // red for errors / negative
	colorYellow   = lipgloss.Color("221") // yellow for warnings
	colorMagenta  = lipgloss.Color("176") // magenta for agents
	colorWhite    = lipgloss.Color("255") // bright white
	colorBorder   = lipgloss.Color("240") // subtle border
	colorBorderFocus = lipgloss.Color("75") // focused border
)

var (
	// Panels
	headerStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorAccent).
			PaddingLeft(1)
	dimStyle = lipgloss.NewStyle().Foreground(colorFgDim)
	borderStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorBorder).
			Padding(0, 1)
	focusedBorderStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(colorBorderFocus).
				Padding(0, 1)
	inputStyle = lipgloss.NewStyle().Foreground(colorAccent)

	// Log level badges
	errStyle   = lipgloss.NewStyle().Bold(true).Foreground(colorRed)
	warnStyle  = lipgloss.NewStyle().Foreground(colorYellow)
	infoStyle  = lipgloss.NewStyle().Foreground(colorAccent)
	debugStyle = lipgloss.NewStyle().Foreground(colorFgDim)
	agentStyle = lipgloss.NewStyle().Foreground(colorMagenta)

	// Ticker
	priceStyle = lipgloss.NewStyle().Foreground(colorGreen)
	symStyle   = lipgloss.NewStyle().Bold(true).Foreground(colorWhite)

	// Status bar (heartbeat)
	heartbeatStyle = lipgloss.NewStyle().
			Background(colorBg).
			Foreground(colorFg).
			Padding(0, 1)
	heartbeatTsStyle = lipgloss.NewStyle().
				Background(colorBg).
				Foreground(colorFgDim).
				Padding(0, 1)

	// Header bar
	appHeaderStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorAccent)
	clockStyle = lipgloss.NewStyle().
			Foreground(colorFgDim)

	closeBtnStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorRed)

	// Tab bar styles
	tabActiveStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorWhite).
			Background(lipgloss.Color("238")).
			Padding(0, 2)
	tabInactiveStyle = lipgloss.NewStyle().
				Foreground(colorFgDim).
				Padding(0, 2)
	tabBarStyle = lipgloss.NewStyle().
			Background(lipgloss.Color("234"))

	// Panel header styles — per panel type
	panelHeaderMarket = lipgloss.NewStyle().
				Bold(true).
				Foreground(colorAccent).
				PaddingLeft(1)
	panelHeaderPositions = lipgloss.NewStyle().
				Bold(true).
				Foreground(colorGreen).
				PaddingLeft(1)
	panelHeaderLog = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorYellow).
			PaddingLeft(1)
	panelHeaderBias = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorMagenta).
			PaddingLeft(1)
	panelHeaderMacro = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("116")).
				PaddingLeft(1)
	panelHeaderAgent = lipgloss.NewStyle().
				Bold(true).
				Foreground(colorMagenta).
				PaddingLeft(1)
	panelHeaderAccount = lipgloss.NewStyle().
				Bold(true).
				Foreground(colorGreen).
				PaddingLeft(1)
	panelHeaderCalendar = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("116")).
				PaddingLeft(1)
	panelHeaderHealth = lipgloss.NewStyle().
				Bold(true).
				Foreground(colorAccent).
				PaddingLeft(1)

	// Separator / divider
	separatorStyle = lipgloss.NewStyle().Foreground(colorBorder)
)

// ─── logEntry: a single line in the combined log panel ───────────────────────

type logEntry struct {
	ts    time.Time
	level string // "ERROR"|"WARN"|"INFO"|"DEBUG"|"AGENT"|"ORDER"
	text  string
}

// ─── Agent run state ──────────────────────────────────────────────────────────

type agentRunState struct {
	agent       string
	runID       string
	step        AgentStep
	toolName    string
	provider    string
	model       string
	content     string // completed result (markdown)
	description string // human-readable context
	symbol      string
	stepNum     int // 1-based step counter in the ReAct loop
	maxSteps    int // configured turn limit
	started     time.Time
	finished    time.Time
	err         string
}

// AskResponseMsg carries the copilot's response back to the TUI model.
type AskResponseMsg struct {
	Response string
	Query    string
	Err      error
}

// BiasUpdatedMsg pushes a fresh BiasResult into the TUI's Bias panel.
type BiasUpdatedMsg struct {
	Result domain.BiasResult
}

// MacroSnapshot bundles cross-market macro indicators displayed in the Macro
// panel. Aliased to the neutral uistate type shared with the web surface.
type MacroSnapshot = uistate.MacroSnapshot

// MacroSnapshotMsg pushes a fresh MacroSnapshot into the TUI's Macro panel.
type MacroSnapshotMsg struct {
	Snapshot MacroSnapshot
}

// NewsSnapshot holds the latest headlines pulled by the news ingest
// goroutine. Aliased to the neutral uistate type shared with the web surface.
type NewsSnapshot = uistate.NewsSnapshot

// NewsSnapshotMsg pushes a fresh NewsSnapshot into the TUI's News panel.
type NewsSnapshotMsg struct {
	Snapshot NewsSnapshot
}

// BudgetProviderUsage is the per-provider breakdown of today's LLM spend.
type BudgetProviderUsage = uistate.BudgetProviderUsage

// BudgetSnapshot is a point-in-time view of the current day's LLM token and
// cost usage. Aliased to the neutral uistate type shared with the web surface.
type BudgetSnapshot = uistate.BudgetSnapshot

// BudgetSnapshotMsg pushes a fresh BudgetSnapshot into the TUI's status bar.
type BudgetSnapshotMsg struct {
	Snapshot BudgetSnapshot
}

// newAskCmd returns a tea.Cmd that calls the copilot function asynchronously.
//
// We deliberately do NOT impose a wall-clock deadline here. The agent runtime
// already enforces `agent.timeout_total_seconds` (and a per-turn budget) via
// Runtime.Invoke, so adding a shorter outer deadline would silently shadow the
// configured value and starve slow reasoning models (minimax-m2.5,
// deepseek-r1, …) that legitimately need >30s for a single turn over
// OpenRouter.
func newAskCmd(query string, fn func(ctx context.Context, query string) (string, error)) tea.Cmd {
	return func() tea.Msg {
		resp, err := fn(context.Background(), query)
		if err != nil {
			return AskResponseMsg{Query: query, Err: err}
		}
		return AskResponseMsg{Query: query, Response: resp}
	}
}

// ─── Constants ────────────────────────────────────────────────────────────────

// maxWatchLines caps how many symbols the watch panel shows at once.
const maxWatchLines = 6

// watchScrollXStep is the number of characters to scroll horizontally per key press.
const watchScrollXStep = 5

// Watch panel column widths.
const (
	colSymbol = 14
	colLast   = 13
	colChg    = 22
	colBidAsk = 25
	colSpread = 9
	colVol    = 10
	// colBias holds the screening agent's directional read so operators
	// see Market Watch quotes and current bias on the same row. Width
	// fits "Neutral" (7) plus a leading space.
	colBias = 9
)

// minAgentPanelH is the minimum outer height of the agent panel (border 2 + header 1 + min content 3).
const minAgentPanelH = 6

// maxAgentPanelH caps the agent panel height so it doesn't consume too much space.
const maxAgentPanelH = 12

// maxAskResponseLines is the maximum number of visible lines for the /ask response display.
const maxAskResponseLines = 15

// askResponseH is the outer height of the ask response panel when visible.
const askResponseH = 2 + 1 + maxAskResponseLines

// spinnerFrames are the braille spinner characters for active agents.
var spinnerFrames = []string{"⣾", "⣽", "⣻", "⢿"}

// borderH is the horizontal overhead added by lipgloss's `Border` (1 cell on
// each side for the rounded border characters). It is the value to subtract
// from the desired OUTER VISIBLE width when calling `borderStyle.Width(W)`,
// because lipgloss `Width` sets the BLOCK width (border excluded, padding
// included).
const borderH = 2

// frameH is the total horizontal frame size of `borderStyle`: 1+1 border plus
// 1+1 padding (`Padding(0, 1)`). It is the value to subtract from a panel's
// OUTER VISIBLE width to get the actual CONTENT AREA available for text.
const frameH = 4

// ─── Responsive layout breakpoints ────────────────────────────────────────────

// breakpoint classifies the terminal size for adaptive layout, similar to a
// CSS media query. Each tier shows progressively more panels.
type breakpoint int

const (
	// bpXS is the single-panel tabbed layout for very small terminals
	// (mobile SSH, narrow splits). Tab key cycles between panels.
	bpXS breakpoint = iota
	// bpSM is the compact two-column layout (Positions | Log).
	// This is the default and matches the historical Cerebro layout.
	bpSM
	// bpMD is the three-column dashboard
	// (Positions/Macro | Bias/AgentRuns | Log).
	bpMD
	// bpLG is the four-column layout adding (Macro/Calendar | …)
	// and an Account panel under Positions.
	bpLG
	// bpXL is the five-column command center adding a Health panel.
	bpXL
)

// Breakpoint thresholds. A breakpoint is selected only if BOTH the width and
// height satisfy its minimums; otherwise we fall back to the next smaller tier.
const (
	bpSMMinWidth  = 60
	bpSMMinHeight = 18

	bpMDMinWidth  = 140
	bpMDMinHeight = 35

	bpLGMinWidth  = 200
	bpLGMinHeight = 50

	bpXLMinWidth  = 260
	bpXLMinHeight = 65
)

// maxBiasRows caps how many bias entries the Bias panel renders.
const maxBiasRows = 8

// maxAgentRunRows caps how many recent agent runs the Agent Runs panel renders.
const maxAgentRunRows = 8

// xsTabCount is the number of tabs available in the XS single-panel layout.
const xsTabCount = 6

// xsTabLabels are the full tab names used in the XS layout. Indexes here align
// with the xsTab field in the model and the per-index switch in renderMiddleXS.
var xsTabLabels = []string{"Market", "Positions", "Log", "Bias", "Macro", "Agents"}

// xsTabShort are abbreviated labels used when the terminal is too narrow for
// the full names.
var xsTabShort = []string{"Mkt", "Pos", "Log", "Bias", "Mac", "Agt"}

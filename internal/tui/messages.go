package tui

import (
	"context"
	"time"

	"github.com/azhar/cerebro/internal/domain"
	"github.com/azhar/cerebro/internal/marketdata"
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
type AgentStep string

const (
	StepThinking  AgentStep = "THINKING"
	StepTool      AgentStep = "TOOL"
	StepObserving AgentStep = "OBSERVING"
	StepStreaming AgentStep = "STREAMING"
	StepComplete  AgentStep = "COMPLETE"
	StepError     AgentStep = "ERROR"
)

// AgentStateMsg updates the live state of a running agent in the agent panel.
type AgentStateMsg struct {
	Agent       string
	RunID       string
	Step        AgentStep
	ToolName    string
	Provider    string
	Model       string
	Content     string // markdown result on COMPLETE, error message on ERROR
	Description string // human-readable context, e.g. "Analyzing BTCUSDT market conditions"
	Symbol      string
	StepNum     int // 1-based step counter in the ReAct loop
	MaxSteps    int // configured turn limit
	At          time.Time
}

// ─── Lipgloss styles ─────────────────────────────────────────────────────────

var (
	// Panels
	headerStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12"))
	dimStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	borderStyle = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1)
	focusedBorderStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(lipgloss.Color("14")).
				Padding(0, 1)
	inputStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("14"))

	// Log level badges
	errStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("9"))
	warnStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("11"))
	infoStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("12"))
	debugStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	agentStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("13")) // magenta

	// Ticker
	priceStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	symStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("15"))

	// Status bar (heartbeat)
	heartbeatStyle = lipgloss.NewStyle().
			Background(lipgloss.Color("235")).
			Foreground(lipgloss.Color("7")).
			Padding(0, 1)
	heartbeatTsStyle = lipgloss.NewStyle().
				Background(lipgloss.Color("235")).
				Foreground(lipgloss.Color("8")).
				Padding(0, 1)

	// Header bar
	appHeaderStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("12"))
	clockStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("7"))

		closeBtnStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("9"))
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

// newAskCmd returns a tea.Cmd that calls the copilot function asynchronously.
func newAskCmd(query string, fn func(ctx context.Context, query string) (string, error)) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		resp, err := fn(ctx, query)
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

// borderH is the horizontal overhead of borderStyle per box.
const borderH = 4

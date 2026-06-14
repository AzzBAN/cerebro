// Package uistate defines neutral UI event DTOs shared by every live UI
// surface (the Bubble Tea TUI and the web dashboard).
//
// These types were originally declared inside internal/tui. They were lifted
// here so that internal/adapter/web can consume the same event shapes without
// importing the tui package (adapters must not import sibling UI packages).
// Both the tui Runner and the web Server translate these DTOs into their own
// render/transport representation.
package uistate

import (
	"time"

	"github.com/azhar/cerebro/internal/domain"
	"github.com/azhar/cerebro/internal/port"
)

// AgentStep mirrors agent.AgentStep / the ReAct loop phase of a running agent.
// Declared as a plain string so neither UI surface needs to import the agent
// package.
type AgentStep string

const (
	StepThinking  AgentStep = "THINKING"
	StepTool      AgentStep = "TOOL"
	StepObserving AgentStep = "OBSERVING"
	StepStreaming AgentStep = "STREAMING"
	StepComplete  AgentStep = "COMPLETE"
	StepError     AgentStep = "ERROR"
)

// AgentState is a single live agent step transition.
type AgentState struct {
	Agent       string
	RunID       string
	Step        AgentStep
	ToolName    string
	Provider    string
	Model       string
	Content     string // markdown result on COMPLETE, error message on ERROR
	Description string // human-readable context
	Symbol      string
	StepNum     int
	MaxSteps    int
	At          time.Time
}

// MacroSnapshot bundles the cross-market macro indicators shown in the Macro
// panel. Zero-valued fields render as "—".
type MacroSnapshot struct {
	FearGreed       domain.FearGreedIndex
	BTCFundingRate  domain.FundingRate
	BTCOpenInterest domain.OpenInterest
	BTCLongShort    domain.LongShortRatio
	UpdatedAt       time.Time
}

// NewsSnapshot holds the latest headlines, newest-first and capped by the
// producer.
type NewsSnapshot struct {
	Items     []port.NewsItem
	UpdatedAt time.Time
}

// BudgetProviderUsage is the per-provider breakdown of today's LLM spend.
type BudgetProviderUsage struct {
	Tokens  int64
	CostUSD float64
}

// BudgetSnapshot is a point-in-time view of the current day's LLM token and
// cost usage. Budgets of 0 mean "disabled".
type BudgetSnapshot struct {
	Date          string
	TokensUsed    int64
	CostUSD       float64
	TokenBudget   int
	CostBudgetUSD float64
	PerProvider   map[string]BudgetProviderUsage
	At            time.Time
}

// Sink is the event surface every live UI implements. The composition root
// fans out each engine event to all registered sinks (TUI, web, …).
//
// Implementations must be safe to call from any goroutine and must never
// block the caller — engine hot paths invoke these.
type Sink interface {
	SendPositions(positions []domain.Position)
	SendBias(b domain.BiasResult)
	SendMacro(s MacroSnapshot)
	SendNews(s NewsSnapshot)
	SendBudget(s BudgetSnapshot)
	SendHeartbeat(line string)
	SendAgentState(s AgentState)
	SendAgentLog(line string)
	// SendOrderLog appends an order-lifecycle line (placed/filled/cancelled).
	SendOrderLog(line string)
	// SendSysLog matches observability.LogSink so a Sink can also receive
	// forwarded slog lines.
	SendSysLog(level, line string)
}

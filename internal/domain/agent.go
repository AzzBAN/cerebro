package domain

import "time"

// AgentRun is a single invocation record for any agent.
// Stored in the agent_runs Postgres table.
type AgentRun struct {
	ID            string
	Agent         AgentRole
	Model         string
	Provider      string
	InputTokens   int
	OutputTokens  int
	CostUSDCents  int
	LatencyMS     int
	Outcome       string // bias_score | approved | rejected | reviewer_recommendation | etc.
	Error         string
	CreatedAt     time.Time
}

// AgentMessage is a single turn in an agent's LLM conversation.
// Stored in the agent_messages Postgres table.
// Raw API keys must never appear in Content.
type AgentMessage struct {
	ID        string
	RunID     string
	Role      string // system | user | assistant | tool
	Content   string
	ToolName  string
	CreatedAt time.Time
}

// EconomicEvent is a parsed calendar entry from Myfxbook / ForexFactory.
type EconomicEvent struct {
	Title       string
	Impact      string // low | medium | high
	Currency    string
	ScheduledAt time.Time
}

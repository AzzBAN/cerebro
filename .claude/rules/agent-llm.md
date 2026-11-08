# Agent / LLM Layer

## Eight-Agent Model

| Agent | Role | Port | Critical Path? |
|-------|------|------|----------------|
| **Screening** | Multi-phase market bias scoring (Phase 1: per-symbol, Phase 2: opportunities, Phase 3: summary) | `port.LLM` | Yes |
| **Risk** | Evaluates signals before execution, fails closed on any error | `port.LLM` | Yes |
| **Copilot** | Q&A with conversation history, truncation, fail-closed | `port.LLM` | Yes |
| **Reviewer** | Weekly post-trade analysis, lesson extraction (async, off critical path) | `port.LLM` | No |
| **Position Manager** | Reviews open positions, returns actions (HOLD/TIGHTEN/CLOSE/FLIP) | `port.LLM` | Yes |
| **Planner** | Generates trade plans from discovery candidates | `port.LLM` | No |
| **Discovery** | Scans universe, ranks candidates, caches results | `port.LLM` | No |
| **Screening Publisher** | Publishes screening results to Telegram | `port.LLM` | No |

Each agent is an independent struct that depends on `port.LLM`, never on a concrete provider.

## Agent Tools (24 tools)

All tools in `internal/agent/tools/`:

| Tool | File | Purpose |
|------|------|---------|
| `analyze` | `analyze.go` | Technical analysis (RSI, BB, MACD, etc.) |
| `balance` | `balance_tools.go` | Account balance queries |
| `calendar` | `calendar.go` | Economic calendar events |
| `candle` | `candle_tools.go` | Historical candle data retrieval |
| `discovery` | `discovery_tools.go` | Universe scanning and ranking |
| `macro` | `macro_tools.go` | Macro economic indicators |
| `market_bias` | `market_bias_tools.go` | Market bias assessment |
| `market_summary` | `market_summary_tools.go` | Market summary generation |
| `news` | `news_tools.go` | News aggregation and sentiment |
| `order` | `order_tools.go` | Order status and history |
| `position` | `position_tools.go` | Open position management |
| `proposal` | `proposal_tools.go` | Trade proposal operations |
| `screening` | `screening_tools.go` | Screening pipeline helpers |
| `sentiment` | `sentiment_tools.go` | Market sentiment analysis |
| `signal` | `signal_tools.go` | Signal generation and validation |
| `strategy` | `strategy_tools.go` | Strategy parameter access |
| `symbol_filter` | `symbol_filter_tools.go` | Exchange symbol filters |
| `timeframe` | `timeframe_tools.go` | Timeframe conversions |
| `trade` | `trade_tools.go` | Trade history queries |
| `volatility` | `volatility_tools.go` | Volatility calculations |
| `volume` | `volume_tools.go` | Volume analysis |
| `watchlist` | `watchlist_tools.go` | Watchlist management |
| `weather` | `weather_tools.go` | Weather data (commodity correlation) |
| `web_search` | `web_search_tools.go` | Web search for research |

### Tool Registration

```go
// internal/agent/tools/registry.go
type Tool struct {
    Name        string
    Description string
    Handler     ToolHandler
}

type ToolHandler func(ctx context.Context, input json.RawMessage) (json.RawMessage, error)

// Register tools per agent based on agent type
```

## Provider Abstraction

All LLM calls go through `port.LLM`. Concrete providers in `internal/adapter/llm/`:

```go
// internal/port/llm.go
type LLM interface {
    Complete(ctx context.Context, req LLMRequest) (LLMResponse, error)
}

type ToolHandler func(ctx context.Context, input json.RawMessage) (json.RawMessage, error)
```

### Provider Fallback Chain

Providers are tried in order with circuit breaker:
1. **Primary**: Configured provider (e.g., Gemini Pro)
2. **Fallback 1**: Secondary provider (e.g., Anthropic Claude)
3. **Fallback 2**: Tertiary provider (e.g., OpenAI GPT-4)
4. **Final**: Error/hold action (configurable)

### Circuit Breaker

Error-rate-based provider disabling:
- **Window**: 5 minutes
- **Threshold**: 50% error rate
- **Recovery**: Half-open after cooldown, full recovery after N successes

To add a new provider (e.g., Gemini Flash), implement `port.LLM` in `internal/adapter/llm/gemini/` with zero changes to agent code.

## Cost Tracking

All agent calls tracked with per-model pricing:

```go
// internal/agent/cost.go
type CostTracker struct {
    Model       string
    Provider    string
    InputTokens int
    OutputTokens int
    CostUSD     decimal.Decimal
    Latency     time.Duration
}
```

### Budget Controls

- **Daily budget limit**: Configurable in `app.yaml` under `agent.budget_daily_usd`
- **Per-call limit**: Configurable in `agent.max_cost_per_call_usd`
- **Alert threshold**: Warns at 80% of daily budget
- **Hard stop**: Rejects new calls when budget exceeded

## Prompt Templates

Prompts live in `internal/agent/prompts/*.tmpl` (Go `text/template`):

```go
// GOOD: load once at startup, execute per request
var screenerTmpl = template.Must(template.ParseFiles("internal/agent/prompts/screener.tmpl"))
```

**Never hardcode prompt strings** in source files.
Template data structs defined in same `agent` sub-package that uses them.
System prompts and user prompts kept in **separate template files**.

### Template Structure

```
internal/agent/prompts/
├── screening/
│   ├── system.tmpl
│   ├── phase1_bias.tmpl
│   ├── phase2_opportunities.tmpl
│   └── phase3_summary.tmpl
├── risk/
│   ├── system.tmpl
│   └── evaluate.tmpl
├── copilot/
│   ├── system.tmpl
│   └── chat.tmpl
├── reviewer/
│   ├── system.tmpl
│   └── weekly_review.tmpl
├── position_manager/
│   ├── system.tmpl
│   └── review_positions.tmpl
├── planner/
│   ├── system.tmpl
│   └── generate_plan.tmpl
├── discovery/
│   ├── system.tmpl
│   └── scan_universe.tmpl
└── publisher/
    ├── system.tmpl
    └── format_telegram.tmpl
```

## Safety Rules

1. Agents are **advisory only** — they produce suggestions, never execute trades directly.
2. Every agent output must pass through **risk gate** before being acted upon.
3. Agent calls must respect `ctx` (timeout & cancellation): set deadline context before calling `LLM.Complete`.

```go
ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
defer cancel()
resp, err := a.llm.Complete(ctx, req)
```

4. **Fail-closed on LLM failure**: When LLM unavailable, risk agent rejects new exposure.
   - Configurable action: `tighten_breakeven`, `hold`, `skip_signal`
   - Set in `app.yaml` under `agent.llm_failure_action`

## Logging Agent Decisions

All agent decisions (input, provider used, output, latency, cost) must be written to `port.AgentLogStore` for auditability.

**Never log raw API keys or user secrets.**

### Agent Run Schema

```sql
-- agent_runs table
- id (UUID)
- agent_type (screening|risk|copilot|reviewer|position_manager|planner|discovery|publisher)
- model (e.g., "gemini-pro", "claude-3-opus")
- provider (e.g., "gemini", "anthropic", "openai")
- input_tokens (int)
- output_tokens (int)
- cost_usd (NUMERIC)
- latency_ms (int)
- outcome (success|error|timeout)
- error (text, nullable)
- created_at (TIMESTAMPTZ)

-- agent_messages table (CASCADE on agent_runs)
- id (UUID)
- run_id (UUID FK)
- role (system|user|assistant|tool)
- content (text)
- tool_name (text, nullable)
- tool_input (jsonb, nullable)
- tool_output (jsonb, nullable)
- created_at (TIMESTAMPTZ)
```

**Note**: Content field may contain raw API keys during execution — ensure proper access controls.

## Agent Lifecycle (`AgentStep` enum)

```go
type AgentStep string
const (
    AgentStepThinking  AgentStep = "THINKING"
    AgentStepTool      AgentStep = "TOOL"
    AgentStepObserving AgentStep = "OBSERVING"
    AgentStepStreaming AgentStep = "STREAMING"
    AgentStepComplete  AgentStep = "COMPLETE"
    AgentStepError     AgentStep = "ERROR"
)
```

Each step emitted to `port.AgentLogStore` for observability.

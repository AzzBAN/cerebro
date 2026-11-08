# Agent & LLM System

## 8 LLM Agents

| Agent | Purpose | Tools Used |
|-------|---------|------------|
| `screening` | 5-phase market screening pipeline | 6 tools |
| `technical_analysis` | Technical indicator analysis | 4 tools |
| `sentiment` | News/sentiment analysis | 3 tools |
| `risk_management` | Risk assessment (fail-closed) | 2 tools |
| `position_proposal` | Generate trade proposals | 5 tools |
| `execution` | Order execution decisions | 3 tools |
| `portfolio` | Portfolio rebalancing | 4 tools |
| `research` | General market research | 7 tools |

## 24 Agent Tools

All tools in `internal/agent/tools/`:

| Tool | File | Purpose |
|------|------|---------|
| `analyze_candles` | `analyze.go` | OHLCV candle pattern analysis |
| `get_balance` | `balance_tools.go` | Fetch account balances |
| `get_positions` | `balance_tools.go` | Fetch open positions |
| `get_orders` | `balance_tools.go` | Fetch order history |
| `fetch_news` | `news_tools.go` | Fetch latest news/sentiment |
| `calculate_indicators` | `indicator_tools.go` | RSI, MACD, BB, etc. |
| `submit_order` | `order_tools.go` | Submit order intent |
| `cancel_order` | `order_tools.go` | Cancel pending order |
| `get_symbol_info` | `symbol_tools.go` | Exchange symbol filters |
| `get_candle_history` | `candle_tools.go` | Historical candle data |
| `check_risk` | `risk_tools.go` | Pre-trade risk validation |
| `propose_position` | `proposal_tools.go` | Create trade proposal |
| `approve_position` | `proposal_tools.go` | Approve/reject proposal |
| `get_screening_results` | `screening_tools.go` | Fetch screening outputs |
| `publish_telegram` | `screening_tools.go` | Publish to Telegram |
| `run_backtest` | `backtest_tools.go` | Historical simulation |
| `get_market_data` | `market_tools.go` | Real-time market data |
| `list_symbols` | `symbol_tools.go` | Available trading pairs |
| `get_portfolio` | `portfolio_tools.go` | Portfolio composition |
| `rebalance` | `portfolio_tools.go` | Rebalance allocations |
| `search_trades` | `trade_tools.go` | Query trade history |
| `get_audit_log` | `audit_tools.go` | Audit event history |
| `calculate_pnl` | `pnl_tools.go` | PnL calculations |
| `get_risk_metrics` | `risk_tools.go` | Risk exposure metrics |

## Provider Fallback Chain

LLM providers configured in `internal/adapter/llm/`:

1. **OpenAI** (primary) - `go-openai` client
2. **Anthropic** (fallback) - Custom HTTP client
3. **Gemini** (fallback) - Custom HTTP client

Failover logic:
- Primary provider timeout/error → try next in chain
- All providers fail → `ErrAgentTimeout` (fail-closed)
- Circuit breaker opens after 3 consecutive failures

## Cost Tracking

All LLM calls track cost in `internal/agent/cost.go`:

```go
type CallCost struct {
    Provider    string          // openai, anthropic, gemini
    Model       string          // gpt-4, claude-3-opus, etc.
    InputTokens int
    OutputTokens int
    CostUSD     decimal.Decimal
    Duration    time.Duration
    Success     bool
}
```

Budget enforcement:
- `agent.budget_daily_usd`: Daily spend limit
- `agent.max_cost_per_call_usd`: Per-call cap
- Exceeding budget → `ErrBudgetExceeded` (fail-closed)

## Agent Execution Flow

```
User command / screening tick
  → App routes to agent
    → Agent calls LLM with tool definitions
      → LLM returns tool calls
        → Agent executes tools via ports
          → Tools return results
            → LLM synthesizes response
              → Agent logs run to AgentLogStore
                → Return result
```

## Tool Handler Pattern

All tools implement `port.ToolHandler`:

```go
type ToolHandler interface {
    Name() string
    Description() string
    Parameters() json.RawMessage
    Execute(ctx context.Context, args json.RawMessage) (json.RawMessage, error)
}
```

Register tools in agent constructor:

```go
agent := &ScreeningAgent{
    tools: []port.ToolHandler{
        tools.NewAnalyzeCandles(candleProvider),
        tools.NewCalculateIndicators(candleProvider),
        // ...
    },
}
```

## LLM Failure Actions

Configured via `agent.llm_failure_action` in `app.yaml`:

| Action | Behavior |
|--------|----------|
| `hold` | Hold current positions, no new trades |
| `tighten_breakeven` | Move stops to breakeven |
| `skip_signal` | Skip signal generation, continue monitoring |

Default: `hold` (safest)

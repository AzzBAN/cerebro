# Cerebro Project Overview

**Cerebro** is a CLI-first automated trading system for Binance (spot & futures including XAUUSDT/Gold) with paper-trade-first safety, multi-agent LLM orchestration (8 specialized agents), terminal TUI, and web dashboard.

## Tech Stack

| Concern | Choice |
|---------|--------|
| Language | Go 1.23 (module: `github.com/azhar/cerebro`) |
| CLI | `cobra` |
| TUI | `bubbletea` + `lipgloss` |
| Web | Next.js 14 (App Router), React 18, TypeScript, Tailwind CSS |
| Exchange | `go-binance/v2` |
| DB | `pgx/v5` (PostgreSQL) |
| Cache | `go-redis/v9` |
| Config | `yaml.v3` + `godotenv` |
| LLM | `go-openai` + custom Anthropic/Gemini HTTP clients |
| ChatOps | `telegram-bot-api/v5`, `discordgo` |
| Numerics | `shopspring/decimal` - **never use `float64` for money** |
| IDs | `google/uuid` |
| Concurrency | `golang.org/x/sync/errgroup` |

## Architecture

Strict **hexagonal (ports-and-adapters)**:

```
cmd/cerebro/main.go
  -> internal/cli (cobra commands)
       -> internal/app (composition root + lifecycle)
            -> internal/domain (pure types, zero deps)
            -> internal/port (interfaces only - 13 ports)
            -> internal/adapter/ (binance, postgres, redis, llm, telegram, discord, web)
            -> internal/agent/ (8 LLM agents + 24 tools + cost tracking)
            -> internal/execution/ (order execution engine)
            -> internal/risk/ (risk gate - safety-critical, fail-closed)
            -> internal/screening/ (5-phase market screening pipeline)
            -> internal/strategy/ (signal generation)
            -> internal/positionproposal/ (operator workflow for trade proposals)
            -> internal/backtest/ (historical simulation framework)
            -> internal/marketdata/ (real-time data hub + 4 ingest sources)
            -> internal/chatops/ (Telegram/Discord bots - 12 commands)
            -> internal/tui/ (Bubble Tea terminal)
            -> internal/uistate/ (UI state management + event contracts)
            -> internal/watchdog/ (startup validation)
```

## Key Directories

| Path | Purpose |
|------|---------|
| `internal/domain/` | Pure business types, enums, value objects, sentinel errors (zero imports) |
| `internal/port/` | 13 port interfaces (Broker, Store, Cache, LLM, Notifier, AgentLogStore, etc.) |
| `internal/adapter/` | External system implementations (binance, postgres, redis, llm, telegram, discord, web) |
| `internal/app/runtime.go` | Composition root - wires ports/adapters, launches goroutines via errgroup |
| `internal/agent/` | 8 LLM agents + 24 tools |
| `internal/backtest/` | Historical strategy simulation with data caching |
| `internal/chatops/` | Telegram/Discord bot integrations (12 commands with permission levels) |
| `internal/execution/` | Order execution engine with bracket management |
| `internal/ingest/` | Market data ingestion (4 sources: REST, WS, file cache, scraper) |
| `internal/marketdata/` | Real-time market data hub with candle aggregation |
| `internal/observability/` | Structured logging (`slog`), metrics (build-tagged Prometheus) |
| `internal/positionproposal/` | Trade proposal workflow (pending -> approved -> executed) |
| `internal/resilience/` | Circuit breaker, retry with exponential backoff + jitter |
| `internal/risk/` | Risk gate (safety-critical, fail-closed on any error) |
| `internal/screening/` | 5-phase screening pipeline |
| `internal/strategy/` | Signal generation from technical indicators |
| `internal/tui/` | Bubble Tea terminal UI |
| `internal/uistate/` | UI state management and event contracts |
| `internal/watchdog/` | Startup validation and health checks |
| `web/` | Next.js frontend (5 tabs: Dashboard, Market, Logs, Agents, Config) |
| `configs/` | `app.yaml`, `markets.yaml`, `strategies.yaml`, `secrets.env` |
| `scripts/migrations/` | SQL migrations (`golang-migrate`) - 5 migrations, 10 tables |
| `deploy/` | `Dockerfile`, `docker-compose.yaml` |

## Safety Invariants

1. **Paper mode is default and mandatory** until live path fully implemented and audited.
2. **Triple agreement required for live**: `ENVIRONMENT=production` in secrets.env + `environment: production` in app.yaml + `--live` CLI flag.
3. **Kill-switch** (`engine.kill_switch: true`) halts all execution immediately.
4. **Never bypass risk gate** (`internal/risk`) on any execution path - fail-closed on any error.
5. **Agents are advisory only** - they produce suggestions, never execute trades directly.
6. **All agent outputs must pass through risk gate** before being acted upon.
7. **LLM failures fail-closed**: on timeout/error, risk agent rejects new exposure.

## Core Features

- **Multi-Agent LLM Orchestration**: 8 specialized agents with 24 tools, provider fallback chain, circuit breaker, cost tracking
- **5-Phase Screening Pipeline**: Bias scoring -> symbol analysis -> opportunity detection -> summary -> Telegram publish
- **Position Proposal Workflow**: Operator reviews AI-generated trade proposals (pending -> approved/rejected -> executed)
- **Real-Time Market Data**: WebSocket feeds + REST polling + file cache + scraper fallback
- **Order Execution**: Bracket orders (entry + stop-loss + take-profit) with OCO support
- **Risk Management**: Pre-trade validation, position limits, drawdown protection, fail-closed errors
- **Backtesting Framework**: Historical simulation with deterministic seeds and data caching
- **ChatOps**: 12 Telegram/Discord commands with operator permission levels
- **Web Dashboard**: Next.js trading terminal with live WebSocket updates, 5-tab layout
- **Terminal TUI**: Bubble Tea interface for local monitoring

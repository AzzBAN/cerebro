# Cerebro — Project Overview

**Cerebro** is a Go CLI automated trading system for Binance (spot + futures) with paper-trade-first safety, a multi-agent LLM layer, and a terminal TUI.

## Tech Stack

| Concern | Choice |
|---|---|
| Language | Go 1.26 (module: `github.com/azhar/cerebro`) |
| CLI | `cobra` |
| TUI | `bubbletea` + `lipgloss` |
| Exchange | `go-binance/v2` |
| DB | `pgx/v5` (PostgreSQL) |
| Cache | `go-redis/v9` |
| Config | `yaml.v3` + `godotenv` |
| LLM | `go-openai` + custom Anthropic/Gemini HTTP clients |
| ChatOps | `telegram-bot-api/v5`, `discordgo` |
| Numerics | `shopspring/decimal` — **never `float64` for money** |
| IDs | `google/uuid` |
| Concurrency | `golang.org/x/sync/errgroup` |

## Architecture

Strict **hexagonal (ports-and-adapters)**:

```
cmd/cerebro/main.go → internal/cli → internal/app (composition root)
   ├─ internal/domain     (pure types, no deps)
   ├─ internal/port       (interfaces only)
   ├─ internal/adapter    (implementations: binance, postgres, redis, llm, bots)
   ├─ internal/config     (load + validate)
   └─ internal/app        (wiring + lifecycle via errgroup)
```

## Safety Invariants

- Paper mode is **default and mandatory** until live path is fully implemented.
- Triple agreement required to go live: `ENVIRONMENT=production` in secrets, `environment: production` in `app.yaml`, and `--live` flag.
- Kill-switch (`engine.kill_switch: true`) halts all execution immediately.
- **Never bypass the risk gate** (`internal/risk`) in execution paths.

## Key Directories

- `internal/domain/` — enums, value types, no outward imports
- `internal/port/` — Go interfaces (ports)
- `internal/adapter/` — external system implementations
- `internal/app/runtime.go` — composition root / goroutine wiring
- `internal/agent/` — LLM agent logic (screener, copilot, reviewer)
- `internal/backtest/` — historical strategy simulation
- `internal/chatops/` — Telegram/Discord bot integrations
- `internal/execution/` — order execution logic
- `internal/ingest/` — market data ingestion
- `internal/marketdata/` — market data types and helpers
- `internal/observability/` — structured logging setup
- `internal/resilience/` — retry/circuit-breaker patterns
- `internal/risk/` — risk gate (safety-critical)
- `internal/strategy/` — signal generation
- `internal/tui/` — Bubble Tea terminal UI
- `internal/watchdog/` — health monitoring
- `configs/` — `app.yaml`, `markets.yaml`, `strategies.yaml`, `secrets.env`
- `scripts/migrations/` — SQL migrations (`golang-migrate`)
- `deploy/` — `Dockerfile`, `docker-compose.yaml`

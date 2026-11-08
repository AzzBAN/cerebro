# Cerebro Project Rules

@.claude/rules/project-overview.md
@.claude/rules/hexagonal-architecture.md
@.claude/rules/go-standards.md
@.claude/rules/config-conventions.md
@.claude/rules/agent-llm.md
@.claude/rules/runtime-wiring.md
@.claude/rules/migrations.md
@.claude/rules/testing-standards.md
@.claude/rules/web-frontend.md
@.claude/rules/chatops.md

## Quick Commands

```bash
make build         # Build binary
make test          # Run unit tests
make test-int      # Run integration tests (requires DB)
make lint          # golangci-lint
make tidy          # mod tidy
make migrate-up    # Apply pending migrations
make migrate-down  # Rollback one migration
make check         # Dry-run config validation
```

## Local Development

1. Copy `configs/secrets.env.example` to `configs/secrets.env` and fill values.
2. Ensure `DATABASE_URL` is set in environment or `.env` file.
3. `make build && ./cerebro run --config-dir=configs`

## Supabase Project

- **Project Name:** Cerebro
- **Project ID:** `azzplqrjsmeueedehmpm`
- **Region:** ap-southeast-1

Migrations are applied via `make migrate-up` using `golang-migrate`.

## Architecture Summary

**Hexagonal (Ports & Adapters)** with strict layer separation:
- **Domain**: Pure types, zero imports (`internal/domain`)
- **Ports**: 13 interfaces (`internal/port`)
- **Adapters**: External implementations (`internal/adapter`)
- **App**: Composition root only (`internal/app`)

## Key Features

- **8 LLM Agents**: Screening, Risk, Copilot, Reviewer, Position Manager, Planner, Discovery, Publisher
- **24 Agent Tools**: Technical analysis, market data, news, sentiment, etc.
- **5-Phase Screening**: Bias → Symbols → Opportunities → Summary → Publish
- **Position Proposals**: Operator workflow for trade approval
- **Risk Management**: Fail-closed, pre-trade validation, drawdown protection
- **ChatOps**: 12 Telegram/Discord commands with permissions
- **Web Dashboard**: Next.js terminal UI with 5 tabs
- **Backtesting**: Historical simulation framework

## Safety Invariants

1. Paper mode is **default and mandatory**
2. Triple agreement for live: ENV + YAML + CLI flag
3. Kill-switch halts all execution
4. **Never bypass risk gate**
5. Agents are **advisory only**
6. LLM failures **fail-closed**

## Known Gaps & TODOs

| Gap | Status |
|-----|--------|
| Live broker not implemented | Explicit guard remains |
| Migration tool inconsistency | Makefile uses golang-migrate, docker-compose uses goose |
| Pending XAU/USDT-PERP | Signal received, markets.yaml entry needed |
| No formal runbook | Who's on-call, incident response |
| Target p95 latency | Undocumented, measure after baseline |
| Max capital/position limits | Not defined for V1 |
| chromedp production | Headless Chrome on VPS, prefer official APIs |
| Prometheus metrics | Build-tagged, default off in V1 |
| Backtest determinism | Needs fixed seeds and cached data |

## graphify

This project has a knowledge graph at graphify-out/ with god nodes, community structure, and cross-file relationships.

- **graphify** (`.claude/skills/graphify/SKILL.md`) - any input to knowledge graph. Trigger: `/graphify`
When the user types `/graphify`, use the installed graphify skill or instructions before doing anything else.

Rules:
- For codebase questions, first run `graphify query "<question>"` when graphify-out/graph.json exists. Use `graphify path "<A>" "<B>"` for relationships and `graphify explain "<concept>"` for focused concepts. These return a scoped subgraph, usually much smaller than GRAPH_REPORT.md or raw grep output.
- If graphify-out/wiki/index.md exists, use it for broad navigation instead of raw source browsing.
- Read graphify-out/GRAPH_REPORT.md only for broad architecture review or when query/path/explain do not surface enough context.
- After modifying code, run `graphify update .` to keep the graph current (AST-only, no API cost).

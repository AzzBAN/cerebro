---
name: bot-review
description: "End-to-end audit of the Cerebro bot: services, account/positions, LLM agents, ingest/news, data, postgres, redis, risk, execution, strategies, TUI. Usage: /bot-review [scope]"
---

# Bot Review Skill

Comprehensive, top-to-bottom audit of the Cerebro trading bot. Launches the
**senior-go-engineer** agent to review each subsystem for architectural
compliance (hexagonal), safety invariants (paper-first, kill-switch, risk
gate), concurrency, decimal handling, error/context propagation, and test
coverage — then verifies compilation, lint and tests.

## Scope Map

The bot is organised as hexagonal ports-and-adapters. Each subsystem below is
reviewed as an independent slice, then cross-cutting concerns are audited.

### 1. Composition root & lifecycle — `internal/app/`
- `runtime.go`, `app.go`, `lifecycle.go`, `brokers.go`, `ingest.go`,
  `strategies.go`, `memory.go`, `source.go`, `universe.go`,
  `quote_fallback.go`
- Verify: ports wired to correct adapters; `errgroup` supervision; graceful
  shutdown via context; no adapter types leaking into non-app packages.

### 2. Domain & ports — `internal/domain/`, `internal/port/`
- Ports: `broker.go`, `derivatives.go`, `ingest.go`, `llm.go`, `notifier.go`,
  `storage.go`, `strategy.go`, `cache.go`, `symbol_source.go`, `universe.go`
- Verify: domain has zero outward imports; every adapter implements a port;
  money/prices use `shopspring/decimal` (never `float64`).

### 3. Exchange adapter (account / positions / orders) — `internal/adapter/binance/`
- `client.go`, `rate_limiter.go`, `spot/`, `futures/`
- Verify: account + position + order endpoints use `decimal`; rate-limiter
  covers all REST/WebSocket paths; error mapping into domain errors;
  reconnection & heartbeat for user-data streams.

### 4. Execution & risk — `internal/execution/`, `internal/risk/`
- Execution: `router.go`, `worker.go`, `monitor.go`, `idempotency.go`, `paper/`
- Risk: `gate.go`, `sizing.go`, `calendar.go`
- Verify: **risk gate is never bypassed**; paper mode is default; kill-switch
  halts immediately; idempotency keys are stable; order state machine safe
  under retries.

### 5. Strategies & market data — `internal/strategy/`, `internal/marketdata/`
- Strategies: `registry.go`, `dedup.go`, `mean_reversion.go`,
  `trend_following.go`, `volatility_breakout.go`, `indicators/`
- Market data: `hub.go`, `candle.go`, `replay.go`
- Verify: signals are deterministic under replay; dedup windows sized per
  strategy; indicators use decimal arithmetic; backtest parity.

### 6. LLM agents — `internal/agent/`, `internal/adapter/llm/`
- Agents: `runtime.go`, `copilot.go`, `discovery.go`, `screening.go`,
  `reviewer.go`, `risk_agent.go`, `pricing.go`, `cost.go`, `usage.go`,
  `tools/`, `prompts/`
- Providers: `openai.go`, `anthropic.go`, `gemini.go`, `fallback.go`,
  `circuit.go`, `errors.go`
- Verify: tool allow-lists per agent; cost/usage accounting; circuit breaker
  + fallback chain; prompts versioned; no secrets in logs; Anthropic prompt
  cache hit-rate sane.

### 7. Ingest & news — `internal/ingest/`
- `news/cryptopanic/` (RE + headless fallback + circuit — see AGENTS.md),
  `calendar/`, `coinglass/`, `scrape/`
- Verify: CryptoPanic AES key/bundle hash current; fallback cooldown wired;
  Redis cache keys (`news:latest`, `news:by_asset:<CODE>`) + TTLs; agent
  `fetch_latest_news` reads cache first.

### 8. Persistence — `internal/adapter/postgres/`, migrations
- `pool.go`, `trade_store.go`, `agent_log_store.go`, `audit_store.go`,
  `log_archiver.go`, `scripts/migrations/`
- Verify: NUMERIC for money (never FLOAT); named constraints; up/down
  migrations both present and idempotent; pgx pool sized & bounded; no
  cross-package SQL leakage.

### 9. Cache — `internal/adapter/redis/`, `internal/adapter/redisx/`
- Verify: key naming is namespaced; TTLs everywhere; context cancellation
  honoured; no unbounded key growth; cache-miss paths degrade safely.

### 10. ChatOps, TUI, CLI — `internal/chatops/`, `internal/tui/`, `internal/cli/`
- Verify: commands gated by mode (paper vs live); TUI does not call adapters
  directly (goes through app layer); bots handle retry + rate limits.

### 11. Cross-cutting — `internal/observability/`, `internal/resilience/`, `internal/watchdog/`, `internal/config/`, `internal/backtest/`
- Verify: structured logging via `slog`; no `log.Printf` or `fmt.Println` in
  hot paths; config load + validate runs before wiring; watchdog escalates
  to kill-switch on repeated faults; secrets only read from env, never YAML.

## Instructions

When the user invokes `/bot-review`, follow these steps:

### Step 1 — Resolve scope
- No arg or `all`: review every subsystem in the **Scope Map** above.
- Named subsystem (e.g. `/bot-review llm`, `/bot-review ingest`,
  `/bot-review risk`, `/bot-review postgres`, `/bot-review execution`):
  review only that slice plus its direct ports.
- Path (e.g. `/bot-review internal/risk/gate.go`): review that file/package.
- `changes`: review uncommitted diff via `git status` + `git diff`.

### Step 2 — Snapshot the tree
Before spawning the agent, gather a fresh context snapshot:
- `git status` and `git log -5 --oneline`
- `ls internal/` and the targeted subsystem directory
- Read `CLAUDE.md`, `AGENTS.md`, and relevant `.claude/rules/*.md`

### Step 3 — Launch `senior-go-engineer`
Give it a prompt containing:
- The exact files/packages in scope (resolved from Step 1 + Step 2)
- The **Scope Map** checklist above as review criteria
- Safety invariants to enforce:
  - paper-first + triple-agreement for live
  - kill-switch halts execution
  - risk gate never bypassed
  - `decimal` everywhere for money
  - hexagonal boundaries intact
- Request findings grouped by **severity** (critical / important / minor)
  with `file:line` refs and a one-line remediation hint per finding.

### Step 4 — Compile & verify
After the agent returns, run the project's verification pipeline in
parallel (these come from `AGENTS.md`):

```bash
make build      # compile binary
make lint       # golangci-lint
make test       # unit tests
make check      # dry-run config validation
```

Optional, when the change set touches those areas:
```bash
make test-int                                   # integration (needs DB)
go test -tags=live ./internal/ingest/news/cryptopanic -run TestLive -v
```

Report any build/lint/test failures as **critical** findings.

### Step 5 — Present report
Summarise to the user as:
1. One-line status per subsystem (✅ / ⚠ / ❌)
2. Critical findings (must-fix before live)
3. Important findings (pre-merge)
4. Minor findings / nits
5. Verification results (build, lint, test, check)
6. Suggested next actions — typically `/fix critical` or `/test <pkg>`.

## Examples

- `/bot-review` — full bot audit across every subsystem
- `/bot-review llm` — agents + LLM adapters only
- `/bot-review ingest` — news, calendar, coinglass, scrape
- `/bot-review risk` — risk gate, sizing, calendar
- `/bot-review execution` — router, worker, monitor, paper, idempotency
- `/bot-review postgres` — pool, stores, migrations
- `/bot-review redis` — cache adapter + key usage across the codebase
- `/bot-review account` — binance client + account/position paths
- `/bot-review changes` — only uncommitted diff
- `/bot-review internal/agent/screening.go` — single file

## Notes

- This skill is **read-only**. It never applies fixes. Use `/fix` to apply
  remediations to findings, and `/test <pkg>` to focus test runs.
- Keep the agent prompt concise: point to the Scope Map section by name
  rather than pasting it inline.
- If `make build` fails, stop and surface the compiler error first — further
  review is noise until the tree compiles.

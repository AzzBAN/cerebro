# Project Cerebro

A high-performance, Binance-native automated trading system powered by Go, featuring a real-time CLI/TUI dashboard, remote ChatOps (Telegram/Discord), and a Multi-Agent LLM architecture for decision-making and risk management.

---

## Architecture at a Glance

```
WebSocket feeds (Binance Spot · Binance Futures · XAUUSDT Gold)
        │
        ▼
  Market Data Hub  ──► Strategy Engine ──► Signal
        │                                      │
        │              (Dedup Window)           │
        │                                      ▼
        │                              Risk Gate (Go)
        │                                      │
        │               (bias cache read)      │
        │                Redis ◄───────── Screening Agent (off-path)
        │                                      │
        │                              Risk Agent (on-path)
        │                                      │
        ▼                                      ▼
  Order Monitor ◄────────────── Execution Router ──► Paper/Live Broker
  (Trailing SL + TP)                                        │
                                                            ▼
                                                       PostgreSQL
```

## Quick Start

```bash
# 1. Copy config templates
cp configs/app.yaml.example configs/app.yaml
cp configs/markets.yaml.example configs/markets.yaml
cp configs/strategies.yaml.example configs/strategies.yaml
cp configs/secrets.env.example configs/secrets.env

# 2. Fill in your API keys in configs/secrets.env

# 3. Validate config and connectivity
make check
# or: go run ./cmd/cerebro check --dry-run

# 4. Start in paper trading mode
go run ./cmd/cerebro run --paper

# 5. Run a backtest
go run ./cmd/cerebro backtest \
  --strategy=trend_following \
  --data=testdata/fixtures/btc_1m.csv \
  --from=2024-01-01 \
  --to=2024-12-31
```

## Docker (Paper Mode)

```bash
# Run database migrations first
docker compose -f deploy/docker-compose.yaml --profile migrate run migrate

# Start all services
docker compose -f deploy/docker-compose.yaml up -d

# View logs
docker compose -f deploy/docker-compose.yaml logs -f cerebro
```

## Configuration

| File | Purpose |
|------|---------|
| `configs/secrets.env` | API keys and credentials (NEVER commit) |
| `configs/app.yaml` | Environment, risk limits, agent settings |
| `configs/markets.yaml` | Exchange venues, symbols, lot sizes |
| `configs/strategies.yaml` | Strategy presets, indicators, SL/TP |

### Environment Triple-Agreement

The system enforces that **all three sources agree** before starting:
1. `ENVIRONMENT` environment variable in `secrets.env`
2. `environment:` field in `app.yaml`
3. `--paper` or `--live` CLI flag

Any mismatch is a **fatal error**. This prevents accidental live trading.

## ChatOps Commands

Available via Telegram (allowlisted users) and Discord:

| Command | Description |
|---------|-------------|
| `/status` | System status, halt mode, open position count |
| `/positions` | All open positions with unrealised PnL |
| `/bias <symbol>` | Latest Screening Agent bias from Redis cache |
| `/ask <question>` | Ask the Copilot agent anything |
| `/pause` | Halt new entries (keep open positions) |
| `/resume` | Resume trading |
| `/flatten` | Close all positions (requires confirmation) |

## Multi-Agent Architecture

```
Screening Agent (off hot-path)
  ├── Scheduled every N minutes
  ├── Calls: get_derivatives_data, fetch_latest_news, get_economic_events
  └── Writes BiasResult to Redis (TTL: bias_ttl_minutes)

Risk Agent (on hot-path, per-signal)
  ├── Invoked by risk/gate after all numeric checks pass
  ├── Can call: get_current_drawdown, calculate_position_size
  ├── Must call exactly one of: approve_and_route_order OR reject_signal
  └── Fail closed: any timeout → signal rejected

Copilot Agent (on-demand via /ask)
  ├── Read-only tools only
  ├── Tools: get_active_positions, query_agent_logs
  └── Denied: approve_and_route_order, reject_signal, force_halt_trading

Reviewer Agent (weekly, async)
  ├── Analyses trade history (lookback_days)
  └── Produces YAML suggestions (advisory only, not auto-applied)
```

## Safety Invariants

- **Paper isolation**: `ENVIRONMENT=LIVE` requires both env var AND CLI flag; mismatch = hard exit
- **Fail closed on AI**: Any LLM timeout → signal skipped (no new risk)
- **One writer per venue**: Single `executionWorker` goroutine per broker; no concurrent order submissions
- **Bias is precomputed**: Screening Agent runs off hot path; Risk Gate reads Redis cache only
- **IP ban recovery**: On Binance HTTP 418, all operations halt immediately

## Development

```bash
make build    # compile binary
make test     # run unit tests
make lint     # run golangci-lint
make check    # validate config + connectivity

# Database migrations (requires $DATABASE_URL)
make migrate-up
make migrate-down
```

## Project Structure

```
cerebro/
├── cmd/cerebro/main.go          # Entry point
├── internal/
│   ├── domain/                  # Value types (no dependencies)
│   ├── port/                    # Interface contracts
│   ├── config/                  # Config loading + validation
│   ├── app/                     # Composition root + lifecycle
│   ├── marketdata/              # Hub, candle buffer, replay
│   ├── strategy/                # 3 strategies + indicators + dedup
│   ├── risk/                    # Gate, sizing, calendar blackout
│   ├── execution/               # Router, worker, paper matcher
│   ├── agent/                   # LLM agents + prompts + tools
│   ├── ingest/                  # CoinGlass, news, calendar, scraper
│   ├── watchdog/                # Boot reconciliation
│   ├── chatops/                 # Unified command dispatcher
│   ├── tui/                     # Bubble Tea terminal UI
│   ├── backtest/                # CSV loader, sim clock, reporter
│   ├── observability/           # Structured logging, metrics
│   └── adapter/
│       ├── binance/             # Spot + Futures WS + REST + rate limiter
│       ├── redis/               # Cache adapter
│       ├── postgres/            # Trade, agent log, audit stores
│       ├── llm/                 # OpenAI, Anthropic, Gemini + fallback
│       ├── telegram/            # Bot + allowlist
│       └── discord/             # Bot
├── scripts/migrations/          # Goose SQL migrations
├── configs/                     # Config files (secrets.env gitignored)
├── testdata/                    # Backtest fixtures + replays
└── deploy/                      # Dockerfile + docker-compose
```

---

*Built with Go 1.25+. Hexagonal architecture. Safety-first.*

# Project Cerebro

A high-performance, Binance-native automated trading system powered by Go, featuring a real-time CLI/TUI dashboard, remote ChatOps (Telegram/Discord), and a Multi-Agent LLM architecture for decision-making and risk management.

---

## Architecture

```
WebSocket feeds (Binance Spot · Binance Futures)
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
  Order Monitor ◄────────────── Execution Router ──► Paper/Demo/Live Broker
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

# 4. Start in paper trading mode (offline, no API keys required)
go run ./cmd/cerebro run --paper

# 5. Start in demo mode (real prices, virtual execution)
go run ./cmd/cerebro run --demo

# 6. Run a backtest
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

## CLI Commands

| Command | Description |
|---------|-------------|
| `cerebro run --paper` | Offline paper trading with synthetic market data |
| `cerebro run --demo` | Binance Demo Trading — real prices, virtual execution |
| `cerebro run --live` | Live trading with real funds on Binance mainnet |
| `cerebro check --dry-run` | Validate config, credentials, and connectivity |
| `cerebro backtest` | Run a strategy against historical CSV data |

All commands accept `--config-dir` (default: `configs`).

## Trading Modes

Cerebro supports three operating modes, each with a different level of real-world exposure:

| Mode | Market Data | Execution | API Keys | Risk |
|------|-------------|-----------|----------|------|
| **Paper** (`--paper`) | Synthetic random-walk | In-memory matcher | None required | Zero |
| **Demo** (`--demo`) | Real Binance mainnet WebSocket | Virtual (demo.binance.com) | `BINANCE_DEMO_*` | Zero |
| **Live** (`--live`) | Real Binance mainnet WebSocket | Real orders on mainnet | `BINANCE_*` | Real funds |

### Environment Triple-Agreement

The system enforces that **all three sources agree** before starting:

1. `ENVIRONMENT` environment variable in `secrets.env`
2. `environment:` field in `app.yaml`
3. `--paper`, `--demo`, or `--live` CLI flag

Any mismatch is a **fatal error**. This prevents accidental live trading.

## Strategies

Three built-in strategies, each with configurable indicators, session filters, and multi-timeframe confirmation:

### Mean Reversion
- **Indicators**: RSI + Bollinger Bands
- **Logic**: Buys on oversold + lower band breach; sells on overbought + upper band breach
- **Filter**: Optional trend EMA alignment (skip entries against the higher-timeframe trend)

### Trend Following
- **Indicators**: EMA crossover (configurable fast/slow) + long-trend EMA + ATR
- **Logic**: Golden cross (fast EMA crosses above slow) → BUY; death cross → SELL
- **Filter**: Optional trend-timeframe alignment

### Volatility Breakout
- **Indicators**: ATR + Bollinger Bands
- **Logic**: Fires stop-limit orders above/below the consolidation range during session opens (NY, Asian, overlap)
- **Filter**: Session-based (only trades during configured session windows)

### Common Strategy Features

| Feature | Description |
|---------|-------------|
| Multi-timeframe | Primary + trend timeframe with alignment filter |
| Session filter | Trade only during `ny_open`, `asian_open`, `overlap`, or `all` |
| Signal dedup | Configurable dedup window prevents duplicate signals |
| Warmup period | Strategies wait for N candles before emitting signals |
| Bias alignment | Optional: only trade when Screening Agent bias matches signal direction |
| Derivatives filter | Filter by funding rate, OI divergence, long/short ratio, taker delta, liquidation zones, fear & greed |
| News blackout | Block entries N minutes before/after high-impact economic events |
| Max spread filter | Reject signals when bid-ask spread exceeds threshold |
| Take-profit levels | Configurable multi-level TP with scale-out percentages and breakeven SL move |
| Trailing stop | Configurable trigger % and step % for trailing stop-loss |

## Risk Management

The **Risk Gate** is a pure-Go, non-AI safety layer that always works, even when LLM providers are down:

| Check | Description |
|-------|-------------|
| Global halt | `/pause` or `force_halt_trading` blocks all new entries |
| Kill switch | `engine.kill_switch: true` in config halts all execution immediately |
| Max drawdown | Session drawdown percentage limit |
| Daily loss | Daily PnL percentage limit |
| Max open positions | Global, per-venue, and per-symbol limits |
| Calendar blackout | Blocks entries around high-impact economic events |
| Bias cache read | Reads Screening Agent bias from Redis (falls back to Neutral if unavailable) |

### Position Sizing

Risk-based position sizing using `risk_pct_per_trade` and stop-loss distance, with lot size, min/max notional, and tick size constraints from `markets.yaml`.

## Multi-Agent LLM Architecture

```
Screening Agent (off hot-path)
  ├── Scheduled every N minutes (configurable)
  ├── Calls: get_market_data, get_derivatives_data, fetch_latest_news, get_economic_events
  ├── Writes BiasResult (Bearish/Neutral/Bullish + reasoning) to Redis (TTL: bias_ttl_minutes)
  └── Tools denied: approve_and_route_order, reject_signal, force_halt_trading

Risk Agent (on hot-path, per-signal)
  ├── Invoked after all numeric Risk Gate checks pass
  ├── Can call: get_current_drawdown, calculate_position_size
  ├── Must call exactly one of: approve_and_route_order OR reject_signal
  └── Fail closed: any timeout → signal rejected

Copilot Agent (on-demand via /ask)
  ├── Available from TUI (type a query) and ChatOps (/ask)
  ├── Read-only tools: get_active_positions, query_agent_logs, get_market_data
  └── Denied: approve_and_route_order, reject_signal, force_halt_trading

Reviewer Agent (weekly, async)
  ├── Analyses trade history over configurable lookback window
  ├── Produces advisory YAML suggestions (not auto-applied)
  └── Sends recommendations to operators via Telegram/Discord
```

### LLM Providers

Cerebro supports multiple LLM providers with automatic fallback:

| Provider | Config Key | Notes |
|----------|------------|-------|
| OpenAI / Compatible | `OPENAI_API_KEY`, `OPENAI_BASE_URL` | GPT models, Ollama, LM Studio |
| Anthropic | `ANTHROPIC_API_KEY`, `ANTHROPIC_BASE_URL` | Claude models |
| Gemini | `GEMINI_API_KEY` | Google Gemini models |

The **Fallback Chain** tries providers in order. On timeout, rate limit, or budget errors, it falls back to the next provider. If all providers fail, the risk gate **fails closed** (signal rejected).

### Cost Management

- Daily token budget and USD cost budget (configurable)
- Per-provider tracking in Redis
- Budget alerts at configurable percentage threshold
- Circuit breaker on error rate

### Agent Tools

| Tool | Available To | Description |
|------|-------------|-------------|
| `get_market_data` | All | Real-time price, bid/ask, 24h change, volume from WebSocket feed |
| `get_derivatives_data` | Screening | OI, funding rate, liquidations, long/short ratio, taker delta, CVD, fear & greed, basis |
| `fetch_latest_news` | Screening | CryptoPanic news search by keyword |
| `get_economic_events` | Screening | Finnhub economic calendar (GDP, CPI, NFP, FOMC) |
| `get_active_positions` | Copilot | All open positions across all venues |
| `query_agent_logs` | Copilot | Past agent invocation logs within a time window |
| `get_current_drawdown` | Risk | Current session drawdown and halt status |
| `calculate_position_size` | Risk | Position size from risk parameters |
| `approve_and_route_order` | Risk only | Approve a signal and route it to execution |
| `reject_signal` | Risk only | Reject a signal with a reason |
| `force_halt_trading` | Risk only | Halt all trading immediately (pause/flatten/notify) |

## Market Data & Data Sources

### Binance WebSocket Feeds
- **Spot**: Book ticker + 24hr ticker per symbol
- **Futures**: Book ticker + 24hr ticker per symbol
- Automatic reconnection with exponential backoff
- IP ban detection (HTTP 418 → immediate halt)

### External Data Sources

| Source | Data | API Key Required |
|--------|------|-----------------|
| CoinGlass | Open interest, funding rate, long/short ratio, liquidations, taker delta, CVD, fear & greed, basis | Yes |
| CryptoPanic | Crypto news headlines with keyword search | Yes |
| Finnhub | Economic calendar events (GDP, CPI, NFP, FOMC) | Yes |
| FinancialJuice | Real-time financial news squawks (scraped via HTTP) | No |
| Myfxbook | Economic calendar (alternative source) | No |

## ChatOps

Available via Telegram (allowlisted users) and Discord:

| Command | Description |
|---------|-------------|
| `/status` | System status, halt mode, open position count |
| `/positions` | All open positions with unrealised PnL |
| `/bias <symbol>` | Latest Screening Agent bias from Redis cache |
| `/ask <question>` | Ask the Copilot agent anything |
| `/pause` | Halt new entries (keep open positions) |
| `/resume` | Resume trading |
| `/flatten` | Close all positions (requires confirmation within timeout) |

All commands are audit-logged and operator-allowlisted.

## TUI Dashboard

Real-time terminal UI built with Bubble Tea + Lipgloss:

- **Market Watch**: Live prices, bid/ask, 24h change and volume for all configured symbols
- **Positions Panel**: Open positions with unrealised PnL
- **Log Viewer**: Scrollable system logs with Up/Down/PgUp/PgDown navigation
- **Agent Panel**: Live agent execution state with spinner, tool calls, and results
- **Status Bar**: Heartbeat indicator, environment, and clock
- **Interactive `/ask`**: Type a query to ask the Copilot agent directly from the TUI

## Execution Engine

- **Execution Router**: Single `executionWorker` goroutine per broker — no concurrent order submissions
- **Paper Matcher**: In-memory order matching engine with fill model, commission, and slippage
- **Order Monitor**: Subscribes to live quotes; manages stop-loss, take-profit, and trailing stops
- **Idempotency**: UUID-based order deduplication prevents double-submission
- **Boot Reconciliation (Watchdog)**: On startup, reconciles broker positions against Redis state; detects orphaned, vanished, and mismatched positions

## Backtesting

```bash
go run ./cmd/cerebro backtest \
  --strategy=trend_following \
  --data=testdata/fixtures/btc_1m.csv \
  --from=2024-01-01 \
  --to=2024-12-31 \
  --output=report.json
```

- Configurable fill model, commission, and slippage
- Simulated clock for deterministic replay
- LLM agents mocked using fixture files for reproducibility

## Configuration

| File | Purpose |
|------|---------|
| `configs/secrets.env` | API keys and credentials (NEVER commit) |
| `configs/app.yaml` | Environment, risk limits, agent settings, TUI, ingest |
| `configs/markets.yaml` | Exchange venues, symbols, lot sizes, timeframes |
| `configs/strategies.yaml` | Strategy presets, indicators, SL/TP, session filters |

### Strategy Config Highlights

Each strategy in `strategies.yaml` configures:

```yaml
name: mean_reversion
enabled: true
markets: [BTC/USDT, ETH/USDT]
primary_timeframe: 5m
trend_timeframe: 1h
warmup_candles: 100
order_type: market
risk_pct_per_trade: 0.5
max_position_size_pct: 10.0
session_filter: all
require_bias_alignment: true
require_trend_alignment: true
signal_dedup_window_seconds: 300
max_spread_pct: 0.1
news_blackout_before_minutes: 30
news_blackout_after_minutes: 15

stop_loss:
  type: atr
  atr_multiplier: 1.5
  min_distance_pips: 10

take_profit_levels:
  - rr_ratio: 1.5
    scale_out_pct: 50
    move_sl_to_breakeven: true
  - rr_ratio: 3.0
    scale_out_pct: 50

indicators:
  rsi: { period: 14, oversold: 30, overbought: 70 }
  ema: { fast: 9, slow: 21, trend: 50, long_trend: 200 }
  bollinger: { period: 20, std_dev: 2.0 }
  atr: { period: 14 }
  macd: { fast: 12, slow: 26, signal: 9 }
  volume: { min_volume_multiplier: 1.5, volume_avg_period: 20 }

# Derivatives filters (optional)
funding_rate_long_max_pct: 0.1
funding_rate_short_min_pct: -0.05
oi_divergence_filter: true
long_short_ratio_max_long: 3.0
require_positive_taker_delta: true
avoid_liquidation_zone_pct: 2.0
fear_greed_long_min: 25
fear_greed_short_max: 75
```

## Safety Invariants

- **Paper isolation**: `ENVIRONMENT=LIVE` requires env var AND CLI flag; mismatch = hard exit
- **Demo isolation**: `ENVIRONMENT=DEMO` requires `BINANCE_DEMO_*` API keys
- **Fail closed on AI**: Any LLM timeout → signal skipped (no new risk)
- **Fallback chain**: All providers fail → signal rejected (never approved by default)
- **One writer per venue**: Single `executionWorker` goroutine per broker; no concurrent order submissions
- **Bias is precomputed**: Screening Agent runs off hot path; Risk Gate reads Redis cache only
- **IP ban recovery**: On Binance HTTP 418, all operations halt immediately
- **Kill switch**: `engine.kill_switch: true` halts all execution at config level
- **Watchdog reconciliation**: Startup state sync prevents stale positions

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

## Tech Stack

| Concern | Choice |
|---------|--------|
| Language | Go 1.25 (module: `github.com/azhar/cerebro`) |
| CLI | Cobra |
| TUI | Bubble Tea + Lipgloss |
| Exchange | go-binance/v2 (Spot + Futures) |
| DB | pgx/v5 (PostgreSQL) |
| Cache | go-redis/v9 |
| Config | yaml.v3 + godotenv |
| LLM | go-openai + custom Anthropic/Gemini HTTP clients |
| ChatOps | telegram-bot-api/v5, discordgo |
| Numerics | shopspring/decimal — never float64 for money |
| IDs | google/uuid |
| Concurrency | golang.org/x/sync/errgroup |

## Project Structure

```
cerebro/
├── cmd/cerebro/main.go          # Entry point
├── internal/
│   ├── domain/                  # Value types, enums (no dependencies)
│   ├── port/                    # Interface contracts
│   ├── config/                  # Config loading + validation
│   ├── app/                     # Composition root + lifecycle
│   ├── cli/                     # Cobra commands (run, check, backtest)
│   ├── marketdata/              # Hub, candle buffer, quote streaming
│   ├── strategy/                # 3 strategies + indicators + dedup
│   │   └── indicators/          # RSI, EMA, Bollinger, ATR
│   ├── risk/                    # Gate, sizing, calendar blackout
│   ├── execution/               # Router, worker, idempotency
│   │   └── paper/               # In-memory matcher + order book
│   ├── agent/                   # LLM agents + prompts + tools
│   │   ├── prompts/             # Go text/template files
│   │   └── tools/               # Agent tool implementations
│   ├── ingest/                  # External data sources
│   │   ├── coinglass/           # Derivatives data (OI, funding, etc.)
│   │   ├── news/                # CryptoPanic news feed
│   │   ├── calendar/            # Finnhub economic calendar
│   │   └── scrape/              # FinancialJuice web scraper
│   ├── watchdog/                # Boot reconciliation
│   ├── chatops/                 # Unified command dispatcher + audit
│   ├── tui/                     # Bubble Tea terminal UI
│   ├── backtest/                # CSV loader, sim clock, reporter
│   ├── observability/           # Structured logging (slog), metrics
│   └── adapter/
│       ├── binance/             # Spot + Futures WS + REST + rate limiter
│       ├── redis/               # Cache adapter
│       ├── postgres/            # Trade, agent log, audit stores
│       ├── llm/                 # OpenAI, Anthropic, Gemini + fallback chain
│       ├── telegram/            # Bot + allowlist
│       └── discord/             # Bot
├── scripts/migrations/          # Goose SQL migrations
├── configs/                     # Config files (secrets.env gitignored)
├── testdata/                    # Backtest fixtures + replays
└── deploy/                      # Dockerfile + docker-compose
```

---

*Built with Go 1.25+. Hexagonal architecture. Safety-first.*

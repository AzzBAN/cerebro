# Config Conventions

## Four-File Model

| File | Contains | Tracked in Git? |
|------|----------|-----------------|
| `configs/app.yaml` | Engine, risk, TUI, ingest, agent settings (sanitised) | Yes |
| `configs/markets.yaml` | Venues, symbols, leverage, position sizing | Yes |
| `configs/strategies.yaml` | Strategy parameters, indicators, thresholds | Yes |
| `configs/secrets.env` | API keys, DATABASE_URL, bot tokens | **No** (gitignored) |

**Always edit `*.example` files alongside real files when adding new config fields.**

## Config File Structure

### `app.yaml` — Engine Configuration

```yaml
environment: paper  # paper | production

engine:
  kill_switch: false
  heartbeat_interval: 30s
  
risk:
  max_position_pct: 0.02          # 2% of account per position
  max_drawdown_pct: 0.10          # 10% daily drawdown limit
  max_open_positions: 10
  min_rr_ratio: 2.0               # Minimum risk/reward ratio
  
agent:
  llm_failure_action: hold        # hold | tighten_breakeven | skip_signal
  budget_daily_usd: 50.00
  max_cost_per_call_usd: 0.50
  timeout_seconds: 30
  
screening:
  enabled: true
  interval: 4h
  publish_telegram: true
  phases:
    - bias_scoring
    - symbol_analysis
    - opportunity_detection
    - summary
    - publish
    
execution:
  default_bracket:
    stop_loss_pct: 0.01           # 1% stop loss
    take_profit_pct: 0.02         # 2% take profit
  oco_enabled: true
  
ingest:
  sources:
    - type: websocket
      enabled: true
    - type: rest_poll
      enabled: true
      interval: 60s
    - type: file_cache
      enabled: true
    - type: scraper
      enabled: false
  
tui:
  refresh_rate: 1s
  show_pnl: true
  
web:
  enabled: true
  port: 8080
  auth_required: true
```

### `markets.yaml` — Venues & Symbols

```yaml
venues:
  - name: binance_spot
    type: spot
    symbols:
      - BTC/USDT
      - ETH/USDT
      - SOL/USDT
    leverage: 1
    
  - name: binance_futures
    type: futures
    symbols:
      - BTC/USDT
      - ETH/USDT
      - XAU/USDT          # Gold
    leverage: 10
    
position_sizing:
  method: fixed_pct         # fixed_pct | kelly | atr_based
  default_pct: 0.01         # 1% of account per trade
```

### `strategies.yaml` — Strategy Parameters

```yaml
strategies:
  - name: rsi_mean_reversion
    enabled: true
    params:
      rsi_period: 14
      rsi_oversold: 30
      rsi_overbought: 70
      bb_period: 20
      bb_width: 2.0
      
  - name: macd_crossover
    enabled: false
    params:
      fast_period: 12
      slow_period: 26
      signal_period: 9
```

### `secrets.env` — Sensitive Values

```bash
ENVIRONMENT=paper
DATABASE_URL=postgresql://user:pass@localhost:5432/cerebro
REDIS_URL=redis://localhost:6379

# Exchange
BINANCE_API_KEY=xxx
BINANCE_API_SECRET=xxx

# LLM Providers
OPENAI_API_KEY=sk-xxx
ANTHROPIC_API_KEY=sk-ant-xxx
GEMINI_API_KEY=xxx

# ChatOps
TELEGRAM_BOT_TOKEN=xxx
TELEGRAM_CHAT_ID=xxx
DISCORD_BOT_TOKEN=xxx
DISCORD_CHANNEL_ID=xxx
```

## Struct Conventions

All config structs live in `internal/config/`.

```go
// internal/config/config.go
type Config struct {
    Environment domain.Environment `yaml:"environment"`
    Engine      EngineConfig       `yaml:"engine"`
    Risk        RiskConfig         `yaml:"risk"`
    Agent       AgentConfig        `yaml:"agent"`
    Screening   ScreeningConfig    `yaml:"screening"`
    Execution   ExecutionConfig    `yaml:"execution"`
    Ingest      IngestConfig       `yaml:"ingest"`
    TUI         TUIConfig          `yaml:"tui"`
    Web         WebConfig          `yaml:"web"`
}

type EngineConfig struct {
    KillSwitch        bool          `yaml:"kill_switch"`
    HeartbeatInterval time.Duration `yaml:"heartbeat_interval"`
}

type RiskConfig struct {
    MaxPositionPct   decimal.Decimal `yaml:"max_position_pct"`
    MaxDrawdownPct   decimal.Decimal `yaml:"max_drawdown_pct"`
    MaxOpenPositions int             `yaml:"max_open_positions"`
    MinRRRatio       decimal.Decimal `yaml:"min_rr_ratio"`
}
```

Nested structs mirror YAML hierarchy (e.g. `Config.Engine.KillSwitch`).
Secret values loaded from env via `godotenv`; **never hardcode in YAML**.
Every new field needs a validation check in `config.Validate()`.

## Accessing Config

Config passed **once** through `internal/app.New(cfg)` and stored in `App` struct. Downstream packages receive **only the fields they need** — do not pass full `*config.Config` into deep domain logic.

```go
// BAD: passes whole config into strategy
strategy.New(cfg)

// GOOD: pass specific values
strategy.New(cfg.Strategy.RSIPeriod, cfg.Strategy.BBWidth)
```

## Environment Safety

`Environment` must be one of: `paper` or `production`.

CLI enforces **triple-agreement** at startup:

1. `ENVIRONMENT=production` in `secrets.env`
2. `environment: production` in `app.yaml`
3. `--live` flag passed to `run`

Any mismatch is a **fatal validation error** — do not weaken these checks.

```go
// internal/config/validate.go
func (c *Config) Validate() error {
    if c.Environment != domain.EnvironmentPaper && c.Environment != domain.EnvironmentProduction {
        return fmt.Errorf("environment must be 'paper' or 'production', got %q", c.Environment)
    }
    // ... triple-agreement check
}
```

## Makefile / Docker

`DATABASE_URL` and other secrets in `Makefile` must only ever be set via environment variables from local `.env` or override — never hardcoded, as the file gets committed.

```makefile
# GOOD
migrate-up:
	@DATABASE_URL=$${DATABASE_URL} migrate -path scripts/migrations -database "$${DATABASE_URL}" up

# BAD
migrate-up:
	@migrate -path scripts/migrations -database "postgresql://..." up
```

## Config Validation Checklist

When adding new config fields:

- [ ] Add field to YAML struct with `yaml:"..."` tag
- [ ] Add validation in `config.Validate()`
- [ ] Update `*.example` files
- [ ] Document in this file
- [ ] Add default value if applicable
- [ ] Ensure secrets use env vars, not YAML

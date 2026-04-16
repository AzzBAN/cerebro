# **Product Requirements Document (PRD): Project Cerebro**

**Version:** 2.3  
**Last updated:** 2026-04-12

**Objective:** Build a high-performance, Binance-native (Crypto Spot/Futures + Gold) automated trading bot powered by Golang, featuring a flawless real-time CLI dashboard, remote ChatOps (Telegram/Discord), and a Multi-Agent LLM architecture for decision-making and risk management.

**Single-broker ecosystem rationale:** Consolidating on Binance for both Crypto and Gold (XAUUSDT Futures Perpetual) eliminates cross-broker reconciliation, unifies WebSocket connection management, and keeps infrastructure free-tier viable — one API key pair, one WS connection pool, one rate-limit budget.

**North-star outcomes (how we know V1 succeeded):**

* **Safety:** No unintended live orders in paper/dry-run; kill-switch and drawdown halts are verified in tests.
* **Explainability:** Every material trade has a traceable chain: signal → agent decisions → execution (stored and queryable).
* **Operability:** Recover from restart/network blip without manual reconciliation in the common case (see §10.5).
* **Latency (targets, tune per asset class):** Market data fan-out and risk checks keep pace with the slowest required venue; document p95 targets once baseline infrastructure is chosen.

## **1\. Multi-Agent AI Architecture**

To prevent the bot from relying purely on rigid technical indicators, Cerebro utilizes a "Mixture of Agents" approach. Three distinct LLM APIs (e.g., Gemini, Claude, or local LLaMA models) run concurrently, each with a specific prompt and context boundary.

1. **The Screening Agent (The Analyst):**
   * **Role:** Processes macroeconomic news, economic calendars, and broad market conditions by ingesting real-time scraped text and API data.
   * **Action:** Generates a "Market Bias Score" (Bullish, Bearish, Neutral) every 1–4 hours. It filters out "noise" and identifies high-probability asset pairs to trade for the day.
   * **Data Pipeline:** Utilizes Go-based web scrapers (go-colly for static HTML, chromedp for JavaScript-heavy sites) and RSS parsers to feed formatted news headlines into the LLM context window before prompting it for a bias score.

2. **The Management / Risk Agent (The Guard):**
   * **Role:** Reviews technical trade signals against current portfolio health and the Screening Agent's bias.
   * **Action:** Approves or rejects signals. If the portfolio is in a 2% drawdown, this agent restricts trading or forces the bot into "Halt" mode to preserve capital. It also suggests position sizing.

3. **The General Copilot (The Communicator):**
   * **Role:** Acts as the conversational interface within the CLI and ChatOps platforms.
   * **Action:** Allows the user to type questions directly into the terminal or via chat apps (e.g., *"Why did you short BTC 10 minutes ago?"*). It reads from the local database to explain the logic of the other two agents in plain English.

### **1.1 Decision order & conflict resolution**

To avoid ambiguous or contradictory automation, agent outputs follow a **fixed precedence** (documented in code and config):

1. **Hard safety & user override:** Emergency halt, `ENVIRONMENT`, and explicit user commands from ChatOps/CLI win over all agents.
2. **Risk / Management Agent:** Can veto or resize any signal; never overridden by the Screening Agent.
3. **Screening Agent:** Bias and context inform *whether* strategies may fire; they do not bypass risk limits.
4. **Execution layer:** Only receives orders after the above gates pass.

**Timeouts & degradation:** If an LLM or tool call exceeds a configured deadline, the system defaults to **no new risk** (hold / skip signal) unless a documented fallback policy applies (e.g., "technical-only mode" with stricter size caps). All stalls and fallbacks are logged.

**Screening Agent bias is precomputed, not on-path.** The Screening Agent runs on its own schedule (every 1–4 h, configurable) and caches its `BiasResult` in Redis with a TTL. The Risk Gate reads this cache synchronously on the signal path — it never blocks the hot path waiting for LLM inference. If the bias key is stale or missing, the Risk Gate defaults to `Neutral` and logs the condition.

### **1.2 Agent Skills (Tool / Function Calling Framework)**

To allow the AI agents to interact with the Go backend, they are equipped with "Skills" (Tool Calling). Instead of just generating text, the LLM generates structured JSON requests that trigger specific Golang functions.

A **tool policy table** in `app.yaml` gates which agents may invoke which tools. The Copilot is denied write-path tools (`approve_and_route_order`, `reject_signal`, `force_halt_trading`) in production; this is enforced by the tool registry in code, not by prompt engineering.

**Screening Agent Skills (Read-Only):**

* `fetch_latest_news(asset string)`: Triggers the Go scraper to pull the last 10 headlines for a specific asset from FinancialJuice or Marketaux.
* `get_economic_events(timeframe string)`: Queries the parsed Myfxbook calendar to see if high-volatility events (like NFP or CPI) are happening soon.
* `get_social_sentiment(asset string)`: Pings the CryptoPanic API for recent sentiment metrics.
* `get_derivatives_data(symbol string)`: Queries the CoinGlass API for a snapshot of on-chain derivatives state — open interest, funding rate, long/short ratio, recent liquidations, and fear & greed index. Returns a structured `DerivativesSnapshot` that the Screening Agent uses to calibrate its bias score (e.g., extreme funding + crowded longs → bearish tilt even if price action looks bullish).

**Management & Risk Agent Skills (Read/Write):**

* `get_current_drawdown()`: Asks the Go engine to calculate the current session's PnL relative to the starting balance.
* `calculate_position_size(risk_pct float, stop_loss_distance float)`: An internal math function that returns the exact lot/coin size allowed.
* `reject_signal(reason string)`: A write-action that drops a pending technical indicator signal.
* `approve_and_route_order(symbol string, side string, size float)`: The ultimate execution skill that passes the approved order to the broker APIs.

**General Copilot Skills (Read/Control):**

* `get_active_positions()`: Returns a JSON list of all currently open trades to display to the user in chat.
* `query_agent_logs(time_window string)`: Fetches the internal reasoning logs of the Screening or Risk agents from the Supabase database.
* `force_halt_trading(mode string)`: Emergency control. **Modes** must be explicit: `pause` (cancel pending, keep positions), `flatten` (close all per broker rules), `pause_and_notify` — so operators never trigger full liquidation by accident.

## **2\. CLI Interface & Launch Modes**

The bot provides a rich, real-time interface when attached to a terminal. It will utilize Go's cobra CLI library to support various launch flags and operational modes, enabling rapid testing directly on the CLI before full deployments.

### **2.1 Launch Flags & Execution Modes**

* `cerebro run --live`: Launches the bot in production mode using real money and live WebSockets.
* `cerebro run --paper`: Launches the bot using live data but simulated execution (Paper Trading).
* `cerebro check --dry-run`: Performs a system-wide health check. Validates config.yaml syntax, tests API key connections, pings the LLM endpoints, tests database connectivity, and safely exits without trading.
* `cerebro backtest --strategy=trend --data=btc.csv --from=2024-01-01 --to=2024-12-31`: Bypasses live inputs and runs the strategy against historical data.

### **2.2 The Terminal TUI**

* **Framework:** Go-based Terminal UI library: **Charmbracelet's Bubble Tea**.
* **Layout:**
  * **Top Panel:** Real-time Ticker tape of monitored assets.
  * **Middle Left:** Active Positions (Asset, Entry Price, PnL%, Stop Loss).
  * **Middle Right:** Agent Log (Live feed of AI reasoning and news ingestion).
  * **Bottom Input:** Persistent command prompt for the Copilot agent (e.g., `/ask`).
* **Event bus:** The TUI subscribes to a typed in-memory event bus (`QuoteEvent`, `PositionEvent`, `AgentLogEvent`, `OrderEvent`) fed by the core engine. Slow TUI rendering never blocks market data ingestion.

## **3\. Market Integrations & Data Sources**

Cerebro is a **single-broker architecture** built entirely on Binance. This covers both asset classes — Crypto and Gold — through one unified API integration.

### **3.1 Broker Execution & Pricing APIs**

| Asset Class | Product | Protocol | Justification |
| :---- | :---- | :---- | :---- |
| **Crypto** | **Binance Spot** (BTC, ETH, SOL, …) | WebSocket (kline/trade streams) / REST | Industry standard; free public data; testnet for paper trading at `testnet.binance.vision`. |
| **Crypto Futures** | **Binance USDT-M Futures** (BTC, ETH, …) | WebSocket / REST | Perpetual contracts with leverage; same credentials as Spot; `dapi` / `fapi` endpoints. |
| **Gold** | **Binance XAUUSDT Futures Perpetual** | WebSocket / REST | Institutional gold pricing on the same API — no second broker needed. ~0.01 USD tick, 0.1 oz min lot, up to 20× leverage. |

> **Go client:** `github.com/adshao/go-binance/v2` — the de facto production standard (actively maintained, covers Spot **and** USDT-M Futures, built-in rate-limit header parsing). The `gjvr/binance-api` package (2019, 0 importers, Spot-only) was evaluated and rejected for production use.

### **3.2 News & Macro Data Sources (The Analyst's Feed)**

To provide the "Screening Agent" with context, the bot will pull data from the following sources:

| Source Type | Provider / Website | Integration Method | Purpose in Bot |
| :---- | :---- | :---- | :---- |
| **Global Breaking News** | **FinancialJuice** | Headless Scraping (chromedp via Go) | Up-to-the-second market squawks; catches sudden geopolitical or macro shifts. |
| **Economic Calendar** | **ForexFactory / Myfxbook** | RSS Feed / XML Parsing | High-impact macro events (CPI, NFP, FOMC). Parsed to know *when* not to trade. |
| **Crypto Sentiment** | **CryptoPanic API** | REST API (JSON) | Crypto-specific news aggregator; structured JSON of trending headlines fed to LLM. |
| **Derivatives Intelligence** | **CoinGlass API v4** | REST API (`CG-API-KEY` header) | The primary quantitative sentiment layer. Polled every 5 minutes; data cached in Redis with 5-min TTL. See §3.3 for full data taxonomy. |

### **3.3 CoinGlass — Derivatives Data Taxonomy**

CoinGlass ([coinglass.com](https://coinglass.com)) provides institutional-grade derivatives analytics via `https://open-api-v4.coinglass.com`. The following endpoints are integrated:

| Data Point | Endpoint | How it informs decisions |
| :---- | :---- | :---- |
| **Open Interest (OI)** | `/api/futures/open-interest/aggregated-history` | Rising OI + rising price = healthy trend. Divergence (price up, OI down) = weakening trend; skip trend-following entries. |
| **Funding Rate** | `/api/futures/funding-rate/oi-weight-history` | Extreme positive funding (>+0.05%/8h) = market too long → fades long bias. Extreme negative = fade short bias. Gold (XAUUSDT) perp funding is especially sensitive to USD macro events. |
| **Long/Short Ratio** | `/api/futures/global-long-short-account-ratio/history` + `/api/futures/top-long-short-account-ratio/history` | Measures retail vs top-trader positioning divergence. >1.5 ratio = crowded longs → bearish lean. |
| **Liquidation Data** | `/api/futures/liquidation/history` + `/api/futures/liquidation/coin-list` | Sudden large liquidations signal cascades. Large long liquidation = bearish momentum; large short liquidation = bullish momentum. |
| **Liquidation Heatmap** | `/api/futures/liquidation/heatmap/model1` | Identifies price levels with dense liquidation clusters. Strategy engine avoids placing entries within those zones (cascade risk). |
| **Taker Buy/Sell Volume** | `/api/futures/aggregated-taker-buy-sell-volume/history` | Taker-side aggression. Net buy delta confirms breakouts; net sell delta filters false breakout signals. |
| **CVD (Cumulative Volume Delta)** | `/api/futures/aggregated-cvd/history` | Tracks net buying vs selling pressure over time. Divergence from price signals distribution (smart money selling into strength). |
| **Fear & Greed Index** | `/api/index/fear-greed-history` | Macro sentiment context for Screening Agent. Extreme Fear (<20) → bullish lean on Crypto; Extreme Greed (>80) → tighten risk. |
| **Coinbase Premium Index** | `/api/coinbase-premium-index` | US institutional demand proxy. Positive premium = US buyers leading; negative = offshore selling. |
| **Futures Basis** | `/api/futures/basis/history` | Spot vs futures price spread. High basis = strong futures premium; basis collapse = position unwind risk. |

## **4\. Core Trading Strategies**

Cerebro's engine will combine traditional quantitative finance with AI filtering. The strategies are modular and can be toggled via config.

1. **Sentiment-Weighted Mean Reversion (Crypto/Gold):**
   * **Logic:** Identifies when an asset is overbought/oversold using RSI and Bollinger Bands.
   * **AI Filter:** The trade is *only* executed if the Screening Agent's sentiment aligns. (e.g., RSI oversold on XAUUSDT, but FinancialJuice reports a sudden Fed rate hawkishness; the Risk Agent uses `reject_signal` to cancel the "buy the dip").

2. **Multi-Timeframe Trend Following (Crypto/Gold):**
   * **Logic:** Looks for Golden Crosses/Death Crosses using Exponential Moving Averages (EMA 50 & EMA 200).
   * **Execution:** Uses a 1-hour chart to identify the overall trend, but executes the entry on a 5-minute chart to optimize the entry price and minimize the stop-loss distance.
   * **Gold note:** XAUUSDT trends strongly on USD macro events (CPI, NFP, FOMC). The Screening Agent is specifically prompted to assess USD-strength signals when evaluating gold bias.

3. **Volatility Breakout (Crypto/Gold — High-Activity Windows):**
   * **Logic:** Monitors ATR. At the New York open (12:00–14:00 UTC) and Asian open (00:00–02:00 UTC), it places pending stop-limit orders above and below the recent consolidation range, betting on a directional breakout. Both crypto and gold exhibit strong volatility spikes at these times.
   * **Time-window awareness:** The bot tracks UTC-based activity windows. Strategies that are time-sensitive read from a `domain.Session` type populated from config (no Forex-specific session names; windows are generic UTC ranges).

### **4.1 Signal deduplication**

If two strategies fire for the same symbol within the same candle period, only the first signal proceeds. A configurable deduplication window (default: 1 candle period) prevents double exposure. Rejected duplicates are counted in metrics and logged.

## **5\. Configuration Management (Separation of Concerns)**

To keep the application maintainable and easily adjustable without recompiling the Go binary, configurations are strictly separated across four files. **All four example files** (pre-filled with sensible defaults) ship in `configs/*.example` and can be copied and modified directly.

| File | Purpose | Committed to Git? |
| :---- | :---- | :---- |
| `configs/secrets.env` | API keys, tokens, DB URLs, LLM keys | **Never** |
| `configs/app.yaml` | Engine, agent, risk, websocket, TUI, backtest settings | Yes (no secrets) |
| `configs/markets.yaml` | Venues, symbols, leverage, timeframes, tick/lot sizes | Yes |
| `configs/strategies.yaml` | Strategy presets with full indicator and risk parameters | Yes |

**Environment safety:** `ENVIRONMENT=PAPER|LIVE` must agree with the CLI flag (`--paper` / `--live`) and the `app.yaml` field. Any mismatch is a fatal error at startup — no silent overrides.

### **5.1 `secrets.env` — attribute reference**

| Attribute | Description |
| :---- | :---- |
| `ENVIRONMENT` | `paper` or `live` (must match app.yaml + CLI flag) |
| `BINANCE_API_KEY / SECRET` | Binance Spot live credentials |
| `BINANCE_TESTNET_API_KEY / SECRET` | Binance Spot testnet (`testnet.binance.vision`) — paper mode |
| `BINANCE_FUTURES_API_KEY / SECRET` | Binance USDT-M Futures live credentials (needed for XAUUSDT + leveraged crypto perps) |
| `BINANCE_FUTURES_TESTNET_API_KEY / SECRET` | Binance Futures testnet — paper mode for futures/gold |
| `COINGLASS_API_KEY` | CoinGlass API v4 key (`CG-API-KEY` header); free tier covers all integrated endpoints |
| `DATABASE_URL` | PostgreSQL connection string |
| `REDIS_URL` | Redis connection string (supports TLS via `rediss://`) |
| `GEMINI_API_KEY` | Google Gemini LLM |
| `ANTHROPIC_API_KEY` | Anthropic Claude LLM |
| `OPENAI_API_KEY` | OpenAI or any compatible endpoint (Ollama, LM Studio) |
| `OPENAI_BASE_URL` | Override base URL for self-hosted LLMs |
| `TELEGRAM_BOT_TOKEN` | Telegram bot credentials |
| `TELEGRAM_ALLOWLIST_USER_IDS` | Comma-separated operator Telegram IDs |
| `DISCORD_BOT_TOKEN` | Discord bot credentials |
| `DISCORD_GUILD_ID` | Discord server ID |
| `DISCORD_TRADE_CHANNEL_ID` | `#trade-execution` channel ID |
| `DISCORD_AI_REASONING_CHANNEL_ID` | `#ai-reasoning` channel ID |
| `DISCORD_SYSTEM_ALERTS_CHANNEL_ID` | `#system-alerts` channel ID |

### **5.2 `app.yaml` — attribute reference**

| Section | Key | Description |
| :---- | :---- | :---- |
| root | `environment` | `paper` \| `live` |
| `log` | `level`, `format` | Log verbosity and output format |
| `engine` | `evaluation_interval_ms` | Strategy loop cadence |
| `engine` | `kill_switch` | Global trading halt without a ChatOps command |
| `risk` | `max_drawdown_pct` | Session drawdown limit; triggers halt |
| `risk` | `max_daily_loss_pct` | Daily loss limit; resets midnight UTC |
| `risk` | `max_exposure_pct` | Max total notional as % of equity |
| `risk` | `max_open_positions` | Cap across all venues |
| `risk` | `max_open_positions_per_venue` | Cap per venue |
| `risk` | `max_open_positions_per_symbol` | Cap per symbol |
| `risk` | `halt_mode_on_drawdown` | `pause` \| `pause_and_notify` \| `flatten` |
| `risk` | `resume_requires_confirmation` | Require `/resume` command after halt |
| `risk` | `min_equity_to_trade` | Safety floor on account equity |
| `agent` | `screening_interval_minutes` | Screening Agent run frequency |
| `agent` | `bias_ttl_minutes` | Redis TTL for cached bias score |
| `agent` | `max_turns` | Max LLM tool-call turns per invocation |
| `agent` | `timeout_per_turn_seconds` | Per-turn LLM deadline |
| `agent` | `timeout_total_seconds` | Total invocation deadline |
| `agent.llm` | `providers` | Ordered provider fallback list |
| `agent.llm` | `fallback_on` | Conditions that trigger provider fallback |
| `agent.llm` | `technical_only_fallback` | Trade without LLM if all providers fail |
| `agent.llm` | `technical_only_size_multiplier` | Reduce size in technical-only mode |
| `agent.llm.models.*` | `model_id`, `temperature`, `max_output_tokens` | Per-provider model settings |
| `agent.llm` | `daily_token_budget` | Total token cap across all providers |
| `agent.llm` | `daily_cost_budget_usd` | USD cost cap; trips circuit breaker |
| `agent.llm` | `alert_at_budget_pct` | ChatOps alert threshold |
| `agent.llm` | `circuit_breaker_*` | Error-rate-based provider circuit breaker |
| `agent.tool_policy.*` | `denied` | Tools each agent is forbidden from calling |
| `reviewer` | `enabled`, `schedule_cron`, `min_trades_required`, `lookback_days` | Reviewer Agent schedule |
| `websocket` | `reconnect_*`, `ping_interval_seconds`, `pong_timeout_seconds`, `alert_after_failures` | WS keepalive and reconnect |
| `chatops` | `flatten_confirmation_timeout_seconds` | Re-confirm window for `/flatten` |
| `tui` | `refresh_rate_ms`, `max_agent_log_lines` | TUI rendering |
| `ingest.*` | `enabled`, `interval_minutes`, `timeout_seconds` | Per-source scraper/feed schedule |
| `backtest` | `fill_model`, `commission_pct`, `slippage_pct` | Backtest simulation fidelity |

### **5.3 `markets.yaml` — attribute reference (per symbol)**

| Attribute | Description |
| :---- | :---- |
| `symbol` | Exchange-specific symbol identifier |
| `contract_type` | `spot` \| `futures_perpetual` \| `futures_delivery` |
| `leverage` | Leverage multiplier (1 = no leverage; max per venue rules) |
| `margin_type` | `isolated` \| `cross` (futures only) |
| `tick_size` | Minimum price increment |
| `lot_size` / `min_lot_units` | Minimum tradeable quantity |
| `min_notional` | Minimum order value in quote currency |
| `max_order_notional` | Maximum single order value |
| `max_position_size_pct` | Max position as % of account equity |
| `max_spread_pct` | Skip entry if spread (ask−bid)/mid exceeds this % |
| `timeframes` | List of candle timeframes to subscribe |
| `primary_timeframe` | Signal-generation timeframe |
| `trend_timeframe` | Higher-timeframe trend confirmation |
| `enabled` | `false` to disable a symbol without removing config |

### **5.4 `strategies.yaml` — attribute reference (per strategy)**

| Category | Attribute | Description |
| :---- | :---- | :---- |
| **General** | `enabled` | Enable/disable the strategy preset |
| **General** | `markets` | List of symbols this strategy monitors |
| **General** | `primary_timeframe` | Signal generation timeframe |
| **General** | `trend_timeframe` | Trend confirmation timeframe |
| **General** | `warmup_candles` | Minimum candles before signals fire |
| **Execution** | `order_type` | `market` \| `limit` \| `stop_limit` |
| **Execution** | `limit_offset_pips` | Limit order offset from current price |
| **Execution** | `time_in_force` | `gtc` \| `ioc` \| `fok` |
| **Execution** | `order_cancel_after_seconds` | Auto-cancel unfilled orders |
| **Execution** | `confirmation_candles` | Extra closed candles before entry |
| **Execution** | `signal_dedup_window_seconds` | Duplicate signal rejection window |
| **Risk** | `risk_pct_per_trade` | Account equity at risk per trade |
| **Risk** | `max_position_size_pct` | Hard position size cap |
| **Stop Loss** | `stop_loss_type` | `atr` \| `fixed_pips` \| `fixed_pct` |
| **Stop Loss** | `stop_loss_atr_multiplier` | SL = ATR × this value |
| **Stop Loss** | `stop_loss_fixed_pips` | Fixed pip stop (when type = fixed_pips) |
| **Stop Loss** | `stop_loss_fixed_pct` | Fixed % stop (when type = fixed_pct) |
| **Stop Loss** | `min_stop_distance_pips` | Minimum stop distance (spread guard) |
| **Take Profit** | `take_profit_levels[].rr_ratio` | TP at N× the stop distance (R:R) |
| **Take Profit** | `take_profit_levels[].scale_out_pct` | % of position to close at this TP |
| **Take Profit** | `take_profit_levels[].move_sl_to_breakeven` | Move SL to entry after TP hit |
| **Trailing** | `trail_trigger_pct` | Activate trailing after N% profit (0 = off) |
| **Trailing** | `trail_step_pct` | Trail increment per favourable move |
| **Indicators** | `rsi.period`, `rsi.oversold`, `rsi.overbought` | RSI configuration |
| **Indicators** | `ema.fast`, `ema.slow`, `ema.trend`, `ema.long_trend` | EMA periods |
| **Indicators** | `bollinger.period`, `bollinger.std_dev` | Bollinger Bands |
| **Indicators** | `atr.period` | ATR period (also used for SL/TP sizing) |
| **Indicators** | `macd.fast`, `macd.slow`, `macd.signal` | MACD configuration |
| **Indicators** | `volume.min_volume_multiplier`, `volume.volume_avg_period` | Volume filter |
| **Filters** | `session_filter` | `all` \| `ny_open` (12:00–14:00 UTC) \| `asian_open` (00:00–02:00 UTC) \| `overlap` (12:00–16:00 UTC) |
| **Filters** | `news_blackout_before_minutes` | Block entries before high-impact events |
| **Filters** | `news_blackout_after_minutes` | Block entries after high-impact events |
| **Filters** | `max_spread_pct` | Skip entry if spread exceeds this % of price |
| **Filters** | `require_bias_alignment` | Require Screening Agent to agree with direction |
| **Filters** | `require_trend_alignment` | Require price on correct side of long-trend EMA |
| **Derivatives** | `funding_rate_long_max_pct` | Skip long if funding rate exceeds this (crowded longs; 0 = disabled) |
| **Derivatives** | `funding_rate_short_min_pct` | Skip short if funding rate more negative than this (crowded shorts; 0 = disabled) |
| **Derivatives** | `oi_divergence_filter` | Reject trend-following entries when OI diverges from price (weakening trend) |
| **Derivatives** | `long_short_ratio_max_long` | Skip long if global L/S ratio > value (over-leveraged longs; 0 = disabled) |
| **Derivatives** | `long_short_ratio_min_short` | Skip short if global L/S ratio < value (over-leveraged shorts; 0 = disabled) |
| **Derivatives** | `require_positive_taker_delta` | Require net taker buy > sell over last N candles before long entry |
| **Derivatives** | `avoid_liquidation_zone_pct` | Skip entry if liquidation cluster within N% of price; 0 = disabled |
| **Derivatives** | `fear_greed_long_min` | Skip long if Fear & Greed Index below value; 0 = disabled |
| **Derivatives** | `fear_greed_short_max` | Skip short if Fear & Greed Index above value; 0 = disabled |

## **6\. Protocols & Execution Engine**

* **Market Data Ingestion:** Must strictly use **WebSockets (wss://)**. Polling via REST will hit free-tier rate limits immediately. WebSockets keep a continuous, low-latency stream of price ticks open.
* **Order Execution:** Uses **REST APIs (https://)** with cryptographic HMAC signing. Order payloads are sent via standard HTTP POST requests.
* **Concurrency (Goroutines):** The Data Ingestion pipeline runs in isolated goroutines, writing to thread-safe Go Channels. The Strategy Engine reads from these channels to ensure a slow Gold candle update never blocks a fast-moving crypto signal.
* **Idempotency:** Order submission uses client-generated `newClientOrderId` (Binance-supported) so retries after network failures do not duplicate exposure.
* **Reconnect policy:** WebSocket connectors use exponential backoff with jitter. After 5 consecutive failures, a ChatOps alert is sent and trading halts for the affected stream.

### **6.1 Binance Rate-Limit Compliance**

All adapter code must respect the following hard limits (queried live via `GET /api/v3/exchangeInfo`):

| Limit type | Value | Scope | Action on breach |
| :---- | :---- | :---- | :---- |
| `REQUEST_WEIGHT` | **6 000 / min** | Per IP | Track with Redis counter; back off on 429 (`retryAfter` header); send ChatOps alert at 80% usage |
| `ORDERS` | **50 / 10 s** | Per account | Track in Redis sliding window; block order submission if exceeded |
| `ORDERS` (daily) | **160 000 / day** | Per account | Track; ChatOps alert at 80%; halt new orders if cap hit |
| WS connections | **300 / 5 min** | Per IP | Pool and reuse connections; never open one-per-strategy |

Additional rules:
* Each new WebSocket connection costs **2 REQUEST_WEIGHT**. Reconnect loops must honour the weight budget.
* HTTP `429` → stop requests immediately; wait for `retryAfter`.
* HTTP `418` → IP ban (2 min to 3 days, scaling on repeat). Log `CRITICAL`; halt all operations until ban lifts; alert operator via Telegram and Discord `#system-alerts`.

## **7\. Remote Control & Notifications (ChatOps)**

To allow the user to monitor and interact with the bot without maintaining an active SSH connection to the VPS, Cerebro implements a "ChatOps" layer using Telegram and Discord.

All operator commands (CLI, Telegram, Discord) are routed through a **unified ChatOps dispatcher** that enforces the permission model and writes every command to an audit log with actor ID and timestamp.

**Supported commands:**
* `/status` — current PnL and open positions.
* `/pause` — halt new orders; keep existing positions.
* `/flatten` — close all positions (requires explicit re-confirmation within 30 s).
* `/resume` — clear halt mode (operator-confirmed).
* `/bias <symbol>` — show cached bias score and age.
* `/positions` — show all open positions.
* `/ask <query>` — routes to the General Copilot LLM.

* **Telegram Integration (go-telegram-bot-api):**
  * **Push Alerts:** Instant push notifications for critical, time-sensitive events (e.g., Trade Executed, Stop-Loss Hit, Daily Profit Summary).
  * **Security:** Whitelist authorized Telegram user IDs; store tokens only in `secrets.env`; rotate bot tokens on compromise; log all remote commands with actor ID and timestamp for audit.

* **Discord Integration (discordgo):**
  * **Organized Logging Hub:** Acts as an organized server for system observability, routing different event severities into specific channels:
    * `#trade-execution`: Logs buy/sell orders with size, entry prices, and timestamps.
    * `#ai-reasoning`: A live feed of the Screening and Risk Agents' logic, bias score changes, and macro news summaries pulled from FinancialJuice/CryptoPanic.
    * `#system-alerts`: Warnings regarding API rate limits, WebSocket disconnects, or VPS resource usage.
  * **Visual Embeds:** Utilizes Discord's rich embeds with color coding (Green for profit/longs, Red for losses/shorts) to create an at-a-glance portfolio dashboard.

## **8\. Infrastructure & Deployment Setup**

To ensure low latency, scalability, and zero initial cost, Cerebro's data layer and deployment environments are structured as follows.

### **8.1 Database & Caching (Free-Tier Native)**

* **Database (Supabase / PostgreSQL):** Used for long-term storage of all completed trades, system logs, AI reasoning history, and configuration states. We will utilize **Supabase** starting on its generous free tier (500 MB database, unlimited API requests) to keep initial overhead at $0 while gaining the power of robust PostgreSQL. Migrations are versioned SQL files managed by **goose**.
* **Cache & Real-Time State (Redis):** Used to store the current application state, open orders, screening bias cache (with TTL), sliding windows of WebSocket prices, and rate-limiting counters. We will use a free tier Redis option (like Upstash, Redis Cloud Essentials, or a local Docker instance) for lightning-fast memory access.

### **8.2 Deployment Strategy**

* **Testing & Development (CLI Direct):** For active development and testing, Cerebro is designed to run directly on the developer's local machine via the Go CLI (e.g., `go run cmd/cerebro/main.go`). The Terminal UI makes it incredibly easy to debug agents and monitor real-time WebSocket connections in a standard terminal.
* **Live Production (Docker):** When transitioning to real money (`--live`), the application will run securely and headlessly on a VPS (Virtual Private Server). We will provide a comprehensive `Dockerfile` (multi-stage, distroless, non-root user) and `docker-compose.yaml` to containerize the Go application alongside a local instance of Redis (if preferred over cloud Redis). This ensures the bot restarts automatically upon failure and operates without any terminal dependency.

## **9\. Future Roadmap: Web View Monitoring**

While V1 is CLI and Chat-based, the architecture guarantees a smooth transition to the Web.

* **Phase 2 (Web UI):** A lightweight Go Chi server will expose a local REST API over port 8080.
* **Frontend:** A React.js or Next.js dashboard will simply poll this local API or connect via WebSocket to render beautiful charts (using TradingView Lightweight Charts) and portfolio graphs in the browser, reading directly from the Supabase DB and Redis cache to remain completely decoupled from the core Go trading engine.
* **Phase 3 (Hardening):** Optional read-only cloud observability (metrics/traces via OpenTelemetry) for VPS deployments; playbook-driven incident responses; strategy versioning in DB (which config hash produced which trade).

## **10\. Advanced & Required System Features (To Be Implemented)**

To ensure the bot is robust, safe, and capable of long-term profitability, the following operational features must be built into the core Go engine:

### **10.1 Global Environment Toggle (Live vs. Paper)**

* **Description:** A strict global configuration flag (`ENVIRONMENT=PAPER` or `ENVIRONMENT=LIVE`).
* **Action:** When set to PAPER, the Execution layer completely bypasses the live Binance API and instead routes orders to the Binance Testnet (`testnet.binance.vision` for Spot; `testnet.binancefuture.com` for Futures/Gold) or a simulated local matching engine. This acts as our "Forward Testing" ground to prove out theories in real-time without capital risk.

### **10.2 The Backtesting Engine (Historical Simulation)**

* **Description:** A dedicated, isolated testing module designed to evaluate strategies against historical market data before deploying them.
* **Action:** It must ingest `.csv` files of 1m/5m/1H candle data and push them through the identical logic channels the live system uses (using a simulated clock to prevent lookahead bias). It must generate an end-of-test report detailing: Max Drawdown, Sharpe Ratio, Profit Factor, and Total Win/Loss ratio. The LLM agents will be mocked using cached fixture files during backtests to produce deterministic, reproducible results.

### **10.3 Dynamic Trailing Stop-Loss & Partial Take-Profits**

* **Description:** Hard stop-losses protect capital, but *trailing* stops secure profit.
* **Action:** Implement a dedicated Order Monitor goroutine that subscribes to position and price events. If an asset moves `trail_trigger_pct` in profit, the stop-loss is automatically moved to the break-even point. Support for "scaling out" (e.g., selling `scale_out_pct` of the position at Target 1, letting the rest run). The Order Monitor submits adjustments through the standard order router — it never bypasses risk or audit.

### **10.4 The "Reviewer" Agent (Self-Reflection Loop)**

* **Description:** Adding a 4th agent to the Mixture of Agents architecture that operates asynchronously on a weekly schedule.
* **Action:** This agent queries Supabase for the week's completed trades, analyzing wins vs. losses. It looks for patterns (e.g., *"I notice 80% of our losses happened during the London open when trading Crypto"*). It produces **reviewable** recommendations (formatted diff suggestion for `strategies.yaml`); applying changes automatically is out of scope for V1 unless explicitly enabled with strong guardrails. Output is published to Discord `#ai-reasoning` and stored in the `agent_runs` table.

### **10.5 State Recovery & "Watchdog" Process**

* **Description:** If the VPS reboots, encounters a network outage, or the Go binary panics, the bot must recover flawlessly.
* **Action:** On startup — before any market data connection is opened — Cerebro cross-references its local Redis state with the broker's actual open positions API. If there is a mismatch (e.g., the broker shows an open trade but Cerebro has no record of it), the bot immediately syncs state, recalculates the stop-loss, writes the discrepancy to `audit_events`, and hands the recovered position off to the Order Monitor before resuming the data feed.

## **11\. LLM Operations, Cost, and Reliability**

* **Budgets:** Per-session and per-day token/cost ceilings tracked in Redis; optional circuit breaker trips when spend exceeds threshold. Alert via ChatOps at 80% consumption.
* **Caching:** Cache Screening Agent outputs for a configurable TTL (aligned with the scheduling interval) so multiple strategies do not spam identical macro prompts.
* **Provider fallback:** If the primary model is down or rate-limited, fall back to a secondary provider or to "risk-only / technical-only" mode (no new discretionary bias) per §1.1. Fallback chain is configured in `app.yaml`.
* **Prompt hygiene:** Minimize PII in prompts; never include raw API keys in LLM context; never log raw prompt content that might contain sensitive account data.
* **Evaluation:** Periodically score agent outputs against realized outcomes (links to §10.4 Reviewer Agent). Track per-provider latency and error rates in `agent_runs`.

## **12\. Compliance, Security, and Data Governance**

* **Regulatory awareness:** Automated trading in crypto and commodity futures is subject to jurisdiction-specific rules, Binance Terms of Service, and applicable financial regulations. This PRD does not constitute legal advice; production use requires review of Binance's API usage policies and applicable local laws.
* **Secrets:** No secrets in repo; use `configs/secrets.env` (gitignored) and a secret manager for production where feasible.
* **Retention:** Define retention windows for trades, logs, and agent reasoning; archive or purge per policy (storage cost + privacy).
* **Backups:** PostgreSQL backup strategy for Supabase (or self-hosted Postgres) aligned with recovery objectives.

## **13\. Testing, Quality, and Observability**

* **Testing:** Unit tests for sizing, risk gates, indicators, rate-limit tracking, and parsers; integration tests against Binance Testnet (Spot) and Binance Futures Testnet (XAUUSDT); replay tests for WebSocket kline message handling; deterministic backtest runs using fixture files.
* **Determinism:** Backtests and paper mode should be reproducible given fixed seeds and cached historical data.
* **Observability:** Structured logs (`slog` JSON) with `correlation_id` per trade; metrics for order latency, WS reconnects, agent timeouts, and LLM errors; optional OpenTelemetry traces later (see §9 Phase 3).

## **14\. Assumptions & Open Questions**

* Which assets ship in **MVP** vs later (Crypto Spot first, then add XAUUSDT Futures)?
* Maximum capital and position limits per venue for V1.
* Whether scraping-heavy sources (chromedp) remain acceptable long-term vs licensed data feeds at scale.
* Formal **runbook**: who is on-call, how to trigger `force_halt_trading`, and how to verify broker state after an incident.
* Target p95 latency per venue: document once baseline infrastructure is chosen.

## **15\. Document History**

| Version | Date | Changes |
| :---- | :---- | :---- |
| 1.8 | 2026-04-12 | Initial baseline |
| 1.9 | 2026-04-12 | Added north-star outcomes, decision precedence, conflict resolution, `force_halt_trading` modes, YAML snippet fix, idempotency, audit logging, roadmap Phase 3, §§ 11–14 |
| 2.0 | 2026-04-12 | Bias precompute clarification (not on hot path); tool policy table; signal deduplication (§4.1); market session awareness; TUI event bus; ChatOps dispatcher with command table; environment triple-agreement; trailing stop detail; Reviewer output destination; LLM provider fallback chain; reconnect policy; goose migrations; `app.yaml` in configs list; backtesting lookahead-bias note; Phase 2 framework updated to Chi; full config attribute reference tables §§5.1–5.4 covering all trading configuration attributes (timeframe, leverage, SL/TP, indicators, sessions, etc.) |
| 2.1 | 2026-04-12 | Removed Stocks market and Alpaca Markets broker; updated scope to Crypto (Binance) + Forex (OANDA) only; removed Alpaca keys from §5.1; removed `trading_session` from §5.3; removed FMP/Marketaux stock news source from §3.2; updated §12 compliance and §13 testing accordingly |
| 2.2 | 2026-04-12 | Removed OANDA/Forex entirely; consolidated to single Binance ecosystem (Crypto Spot/Futures + Gold via XAUUSDT Futures Perpetual); added §6.1 Binance Rate-Limit Compliance table (6000 weight/min, 50 orders/10s, 160k orders/day, 300 WS connections/5min, 418/429 handling); updated §3.1 broker table; recommended `github.com/adshao/go-binance/v2` with rejection note for `gjvr/binance-api`; updated session filters to UTC time windows; removed `pip_value` and Forex contract types; updated strategies §4.1–§4.3 for Gold/Crypto context |
| 2.3 | 2026-04-12 | Added CoinGlass API v4 integration (§3.3): Open Interest, Funding Rate, Long/Short Ratio, Liquidation Data + Heatmap, CVD, Taker Buy/Sell Volume, Fear & Greed Index, Coinbase Premium, Futures Basis; added `get_derivatives_data` Screening Agent skill (§1.2); added `COINGLASS_API_KEY` to §5.1; added 9 new derivatives filter attributes to §5.4 (`funding_rate_*`, `oi_divergence_filter`, `long_short_ratio_*`, `require_positive_taker_delta`, `avoid_liquidation_zone_pct`, `fear_greed_*`) |

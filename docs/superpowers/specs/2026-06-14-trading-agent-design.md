# Cerebro Trading Agent — Position Lifecycle & Bias Reconciliation

**Date:** 2026-06-14
**Status:** Approved design, pending spec review
**Author:** Design session (Azhar + Claude)

## Problem

Cerebro opens positions but never re-evaluates an open position against
changed market conditions. Concretely:

- The Screening Agent writes a directional **bias** per symbol to Redis
  (`bias:<symbol>`, `internal/agent/screening.go:458`). When it flips
  Bear→Bull, only a cache key is overwritten and the TUI panel updates.
- The **risk gate** reads that bias (`internal/risk/gate.go:152`) but only
  logs it — it takes no action.
- The **discovery planner** reads bias (`internal/agent/planner.go:204`)
  only to generate *new* trade-plan suggestions, never to manage existing
  positions.
- Open positions are managed solely by (a) the broker **bracket** (fires on
  price crossing SL/TP, `paper/book.go:229`) and (b) the **Monitor**, which
  only acts on stop-loss (`monitor.go:86`) — its take-profit branch is an
  empty stub (`monitor.go:95-99`) and trailing-stop does nothing
  (`monitor.go:129`).

**Result:** a SELL position in profit stays open after bias flips Bear→Bull,
because no code path connects the bias change to the open position. It will
only close if price happens to hit SL or TP.

Two additional gaps:

1. **No hard TP/SL guarantee.** LIMIT / STOP_LIMIT entries skip bracket
   placement entirely (`worker.go:155`), leaving positions that can only be
   closed by the SL-only Monitor — never by take-profit.
2. **Paper close bug.** A reduce-only close routes a fresh opposite-side
   order through `Book.Fill`, which keys positions by symbol and would
   **overwrite** a long with a short rather than flatten it
   (`paper/book.go:127`).

## Goals

- Every open position is guaranteed to have a protective bracket (TP + SL),
  enforced deterministically even when the LLM is unavailable.
- When market conditions change (bias flip, profit threshold, approaching
  TP/SL), an agent evaluates the position using PnL, live indicators, bias,
  and distance to TP/SL, and decides: **hold / tighten stop / close / flip**.
- Decisions are posted for human confirmation; if the human does not respond
  within **1 minute**, the action executes autonomously.
- Works for both the paper matcher and live Binance futures.

## Non-Goals

- Replacing the entry-side strategy engine (unchanged).
- Multi-leg / scale-out take-profit beyond the existing TP1 (future phase).
- Hedge-mode (both long and short on one symbol) — one-way mode only.

## Decisions (from brainstorming)

| Decision | Choice |
|---|---|
| Autonomy | Suggest-and-confirm; auto-execute after 1-min human silence |
| Bias-flip action | Agent decides per-case (hold/tighten/close/flip) |
| TP/SL | Hard guarantee — no naked positions, ever |
| Scope | Paper + live Binance futures |
| Flip sizing | New entry sized by strategy risk-% logic (not mirrored qty) |
| LLM failure | Fallback to tighten-stop-to-breakeven (never auto-close/flip) |

## Architecture

New work slots into the existing hexagonal layout. The TP/SL guarantee and
orphan sweep are **pure Go** (run even if the LLM is down); only the
hold/close/flip judgment calls the LLM.

```
internal/
  execution/
    reconciler.go        NEW — deterministic: bracket guarantee, orphan
                         sweep, review-trigger detection (no LLM)
    action_queue.go      NEW — suggest→confirm→(1-min timeout)→auto-execute
  agent/
    position_manager.go  NEW — LLM judgment: hold / tighten / close / flip
    prompts/
      position_manager.tmpl        NEW — system prompt
      position_manager_user.tmpl   NEW — per-decision context
  domain/
    position_review.go   NEW — ReviewTrigger, ManagedAction, decision enums
  config/
    config.go            EDIT — PositionManagerConfig block + validation
  app/
    runtime.go           EDIT — wire reconciler goroutine + action queue
  execution/
    paper/book.go        EDIT — fix reduce-only close overwrite bug
```

`position_manager` becomes the 4th `domain.AgentRole` alongside
Screener / Copilot / Reviewer. No new ports are required — the work reuses
`port.Broker`, `port.LLM`, `port.Notifier`, `port.TradeStore`, `port.Cache`.

### Data flow per reconcile cycle

```
Reconciler tick (every reconcile_interval_ms)
  ├─ snapshot open positions (collectPositions) + live brackets
  ├─ for each open position:
  │    ├─ Job A — bracket guarantee:
  │    │    ├─ no bracket? attach from stored SL/TP (or strategy default)
  │    │    └─ attach fails? flatten position (reduce-only)
  │    └─ Job B — review-trigger detection (debounced):
  │         ├─ BiasFlipAgainst  (cached bias opposes position side)
  │         ├─ ProfitThreshold  (unrealized PnL% past mark)
  │         └─ NearTPSL         (price within band of TP or SL)
  │              └─ trigger fired → PositionManager.Decide(context)
  │                   └─ ManagedAction → ActionQueue
  ├─ orphan sweep: bracket with no position → cancel
  └─ ActionQueue: post suggestion to Telegram + TUI, start 1-min timer
       ├─ human confirms → execute now
       ├─ human rejects  → drop + log
       └─ 1 min elapses  → execute autonomously, then notify
```

## Components

### 1. Position Reconciler (`internal/execution/reconciler.go`)

Deterministic Go. Runs on a ticker (`reconcile_interval_ms`, default 5000).
Holds references to the broker set, the bias cache, an indicator source, the
Position Manager agent, and the Action Queue. Two jobs per open position:

**Job A — Hard TP/SL guarantee:**
- Compares expected brackets (derived from open positions) against live
  brackets at the broker.
- Position with no bracket → attach using the position's stored `StopLoss`/
  `TakeProfit1`; if those are zero, derive from strategy defaults
  (`deriveStopLoss` / `deriveFirstTakeProfit` logic, lifted to a shared
  helper). Attach failure → **flatten the position** (reduce-only) rather
  than leave it naked.
- Bracket with no position (orphan) → cancel it.
- Paper: reconciles against `Book`. Live futures: reconciles against the
  exchange's open algo orders (STOP_MARKET / TAKE_PROFIT_MARKET).

**Job B — Review-trigger detection** (returns typed `ReviewTrigger`, no
judgment):
- `BiasFlipAgainst` — `bias:<symbol>` now opposes position side
  (Bull vs SELL, Bear vs BUY).
- `ProfitThreshold` — `UnrealizedPnLPct()` past `profit_threshold_pct`.
- `NearTPSL` — price within `near_tp_sl_pct` of TP or SL.
- Each trigger debounced per (position, trigger-type) within
  `trigger_debounce_sec` so one flip does not spam every tick.

When a trigger fires, the reconciler calls `PositionManager.Decide` with the
full context, then enqueues the returned action.

### 2. Position Manager agent (`internal/agent/position_manager.go`)

LLM judgment, depends on `port.LLM`. Mirrors the existing agent pattern
(prompt templates, ctx timeout, logs to `AgentLogStore`).

**Input context** (rendered into `position_manager_user.tmpl`):
- Position: side, entry, qty, leverage, opened-at.
- Live unrealized PnL (quote) and ROI%.
- Indicator snapshot — same indicators the strategies use (RSI, BB, ATR,
  trend), pulled from a shared indicator source.
- Cached bias score + reasoning.
- Distance to TP and SL (%).
- Recent strategy performance (reuse `AggregatePerformance`).

**Output** (structured JSON, parsed into `domain.ManagedAction`):
```
ManagedAction{
  Decision:   hold | tighten_stop | close | flip
  NewStopLoss decimal   // required when Decision == tighten_stop
  Reason      string
  Confidence  float64   // 0..1
}
```

**LLM failure** → deterministic fallback `tighten_breakeven`: move stop to
entry price. Never auto-closes or flips on an LLM error.

### 3. Action Queue (`internal/execution/action_queue.go`)

Owns the suggest→confirm→timeout lifecycle. Injected clock for testability.

- Receives a `ManagedAction` + position reference; posts a formatted
  suggestion (decision + reason + confidence) to Telegram and TUI.
- Starts a `confirm_timeout_sec` timer.
- **Confirm** (ChatOps control) → execute now.
- **Reject** → drop + log.
- **Timeout** (`autonomous_on_timeout: true`) → execute autonomously, then
  notify "auto-executed after 1 min".
- **Before executing**, re-check the position still exists (it may have been
  closed by its bracket during the wait). If gone → drop + notify.

**Execution semantics:**
- `tighten_stop` — cancel existing bracket, re-place with new SL, same TP.
  Reduce-only; skips risk gate.
- `close` — reduce-only market close; skips risk gate (matches `/close`).
- `flip` — reduce-only close → poll until flat → submit fresh opposite-side
  entry **through the full risk gate**, sized by strategy risk-% logic. If
  the gate rejects the re-entry, the position stays flat (never a naked
  reversed position). The two legs are never simultaneously naked.

## Configuration

New block in `app.yaml` (and `app.yaml.example`), validated in
`config.Validate()`:

```yaml
position_manager:
  enabled: true
  reconcile_interval_ms: 5000      # deterministic loop cadence
  confirm_timeout_sec: 60          # 1-minute human-confirm rule
  autonomous_on_timeout: true      # execute if human silent past timeout
  trigger_debounce_sec: 300        # don't re-ask same trigger within window
  llm_failure_action: tighten_breakeven   # enum; safe fallback
  review_triggers:
    bias_flip_against: true
    profit_threshold_pct: 1.0      # ask agent once PnL% crosses this
    near_tp_sl_pct: 0.2            # "approaching TP/SL" band
```

Validation rules:
- `reconcile_interval_ms` > 0; `confirm_timeout_sec` > 0.
- `trigger_debounce_sec` >= 0.
- `llm_failure_action` ∈ {`tighten_breakeven`, `hold`}.
- `profit_threshold_pct` >= 0; `near_tp_sl_pct` >= 0.
- When `enabled: false`, the reconciler goroutine is not started (existing
  Monitor/bracket behavior is unchanged).

Live futures uses the same block; `confirm_timeout_sec` and
`autonomous_on_timeout` apply identically.

## Safety Edge Cases

- **Bracket fired during the 1-min wait** → Action Queue re-checks the
  position exists before executing; if gone, drops the action and notifies.
- **Paper close overwrite bug** (`paper/book.go:127`) → fixed: a reduce-only
  order that opposes an existing position on the same symbol flattens (or
  reduces) it instead of overwriting with a reversed position. Reconciler
  closes depend on this.
- **Flip on live futures** → reduce-only close, poll until flat (or
  user-data fill confirm), then gated entry. Never both legs naked.
- **Kill-switch / halt active** → reconciler still enforces the bracket
  guarantee (protective) and allows close/tighten, but suppresses flips and
  new entries (additive risk), consistent with the gate's halt behavior.
- **Duplicate suggestions** → debounced per (position, trigger-type).
- **LLM down** → bracket guarantee + orphan sweep still run; review triggers
  resolve via `llm_failure_action` without contacting the LLM.

## Testing

Table-driven, per `testing-standards.md`. Stubs implement ports
(`stubBroker`, `stubLLM`, `stubCache`), never concrete adapters. `-race` on
all.

**Reconciler:**
- naked position → attaches bracket.
- attach fails → flattens position (reduce-only).
- orphan bracket → cancelled.
- each review trigger fires correctly and debounces within window.
- halt active → bracket enforced, flip/entry suppressed.

**Position Manager:**
- stub LLM returning each decision → asserts structured `ManagedAction`.
- LLM error → fallback to `tighten_breakeven` (stop = entry).
- ctx timeout respected.

**Action Queue** (injected clock):
- confirm path executes immediately.
- reject path drops + logs.
- timeout path auto-executes then notifies.
- position-gone-during-wait → drops.

**Flip sequencing:**
- close → flat → gated re-entry happens in order.
- gate rejection on re-entry leaves position flat (not naked reversed).

**Paper book fix:**
- reduce-only opposite-side order flattens rather than overwrites.

## Implementation Phases

1. **Domain + config** — `position_review.go` types, `PositionManagerConfig`,
   validation, `.example` updates. `position_manager` AgentRole.
2. **Paper book fix** — flatten-on-reduce-only + regression test (unblocks
   reconciler closes).
3. **Reconciler (Job A)** — bracket guarantee + orphan sweep, deterministic,
   fully tested. Delivers the hard TP/SL guarantee on its own.
4. **Position Manager agent** — prompts, decision parsing, LLM-failure
   fallback, agent logging.
5. **Reconciler (Job B) + Action Queue** — trigger detection, suggest/
   confirm/timeout lifecycle, ChatOps confirm control, TUI surface.
6. **Runtime wiring** — goroutine in `runtime.go`, behind `enabled` flag,
   paper first then live-futures path.

Each phase is independently testable; phases 1–3 deliver the TP/SL guarantee
even before the LLM judgment layer lands.

## Open Questions

None blocking. Indicator-source plumbing (how the reconciler obtains the live
indicator snapshot for the agent) is detailed during planning — the strategy
engine already computes these, so the likely path is a shared snapshot cache.

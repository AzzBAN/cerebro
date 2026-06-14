# Exchange-Side SL/TP Detection + Web-Confirmed Position Adjustments

**Date:** 2026-06-14
**Status:** Approved (pending spec review)
**Approach:** B — web-native proposal store, independent of ChatOps; proposals have **no timeout**.

## Problem

When a position is opened outside Cerebro (e.g. directly on the Binance
website), it arrives with `StopLoss` and `TakeProfit1` both zero. The
reconciler's `enforceBrackets` treats a position with no protective levels as
an unsafe state and **flattens it immediately** (reduce-only market close).
Observed log sequence:

```
WARN reconciler: position has no SL/TP levels to attach; flattening symbol=BTC/USDT-PERP
INFO order submitted ... side=sell ...
```

Two gaps cause this:

1. `StopLoss`/`TakeProfit1` on `domain.Position` are Cerebro-internal metadata,
   populated only on positions Cerebro itself opened via the bracket path
   (`applyPositionSnapshot` carries them forward only from an existing cache
   entry). The adapters never read the user's open conditional orders on the
   exchange.
2. The position-manager agent (Job B) only *adjusts existing* brackets reacting
   to triggers; it never originates protection for a bare position, and Job A
   flattens before Job B could weigh in anyway.

## Goals

1. Detect exchange-side protective orders (futures `STOP_MARKET` /
   `TAKE_PROFIT_MARKET`; spot OCO `STOP_LOSS_LIMIT` + `LIMIT_MAKER`) and map
   their prices onto `Position.StopLoss` / `Position.TakeProfit1`.
2. Stop the reconciler from flattening — or double-bracketing — a position that
   is already protected on the exchange.
3. Let the agent *propose* an SL/TP readjustment for such a position, surfaced
   in the web Active Positions panel, that executes **only after the operator
   confirms it on the website**. Proposals persist until confirmed or rejected
   (no clock timeout).

## Non-Goals

- No autonomous execution of adjustments. The agent is advisory; the operator
  is the sole trigger.
- No new TUI keypress confirm path (web is the confirmation surface).
- No change to the paper/demo auto-execution behavior of the existing
  `ActionQueue` — the new proposal store is a separate mechanism.

## Architecture

### 1. Exchange-side detection (adapters)

**Futures** (`internal/adapter/binance/futures/rest.go`):
After `fetchPositionSnapshot` builds the position map, call
`NewListOpenOrdersService`. For each symbol, inspect reduce-only /
`closePosition` conditional orders:

- `STOP_MARKET.stopPrice` → `StopLoss`
- `TAKE_PROFIT_MARKET.stopPrice` → `TakeProfit1`

Retain the exchange `orderId`s in an internal
`map[domain.Symbol]protectiveOrders` (under the existing `b.mu`) so a confirmed
adjustment can cancel the exact orders. Detection merges into the snapshot on
both bootstrap and periodic resync, so editing SL/TP on Binance self-heals.

**Spot** (`internal/adapter/binance/spot/rest.go`):
List open orders against the synthesized balance positions:

- `STOP_LOSS_LIMIT.stopPrice` → `StopLoss`
- OCO `LIMIT_MAKER.price` → `TakeProfit1`

Map onto positions produced by `rebuildPositionsLocked`. Retain the
OCO `orderListId` / order IDs for later cancel.

A detected level only counts when it comes from a **live exchange order**, not
from Cerebro's own tracked bracket (those already flow through `BracketTracker`).

### 2. Domain + reconciler guard

Add one field to `domain.Position`:

```go
// ExternallyProtected reports that StopLoss/TakeProfit1 were read from live
// protective orders on the exchange that Cerebro did not place itself.
ExternallyProtected bool
```

`enforceBrackets` (`internal/execution/reconciler.go`) gains a branch ordered
before the naked-position flatten:

1. `Tracker.Has(sym)` → skip (Cerebro's own bracket). *unchanged*
2. `pos.ExternallyProtected` → **skip** (operator's exchange SL/TP). *new — kills
   the flatten bug and prevents placing a duplicate bracket.*
3. naked (`StopLoss` & `TakeProfit1` zero, not externally protected) → flatten.
   *unchanged*
4. has levels, untracked, not externally protected → place bracket. *unchanged*

### 3. Proposal store (`internal/positionproposal`)

New package, deliberately separate from `ActionQueue`, with **no timeout and no
autonomous execution**:

```go
type Proposal struct {
    ID            string
    Symbol        domain.Symbol
    Venue         domain.Venue
    Side          domain.Side
    CurrentStop   decimal.Decimal
    CurrentTP     decimal.Decimal
    ProposedStop  decimal.Decimal
    ProposedTP    decimal.Decimal
    Reasoning     string
    CreatedAt     time.Time
}

type Store interface {
    Propose(p Proposal) string                 // returns id; supersedes any
                                               // existing proposal for Symbol
    Pending() []Proposal                        // for web snapshot/WS
    Confirm(ctx context.Context, id string) error
    Reject(id string) error
    SetPositionExists(fn func(domain.Symbol) bool)
}
```

Semantics:

- **No clock expiry.** A proposal lives until `Confirm` or `Reject`.
- **One proposal per symbol**; a newer `Propose` replaces the older.
- **Position-gone guard** (reused pattern from `ActionQueue`): before execution,
  and during a periodic prune tick, a proposal whose position no longer exists
  is silently dropped. This is the only automatic removal.
- `Confirm` executes the adjustment via an injected execute func, then removes
  the proposal. `Reject` removes without executing.

### 4. Agent origination (Job B extension)

When Job B's reviewer decides an SL/TP change for a position that is
`ExternallyProtected` (or otherwise flagged as requiring sign-off), it calls
`proposalStore.Propose(...)` instead of enqueuing on the auto-executing
`ActionQueue`. The decision carries proposed stop and TP levels plus reasoning.

On confirm, the proposal store invokes a single injected `applyAdjustment`
execute func (not the `ActionExecutor`/`ActionQueue` path). `applyAdjustment`
performs: cancel the **detected exchange protective orders** for the symbol
(by the order IDs cached in §1), then `Broker.PlaceBracket` at the proposed
SL+TP, then `BracketTracker.Record`. This is decision-agnostic — there is **no
new `domain.ManagedAction` decision type** and `ActionExecutor.tightenStop` is
left unchanged; the cancel+place+record logic is shared via a small unexported
helper rather than by routing through the executor.

### 5. Web transport (independent of ChatOps — Approach B)

`internal/adapter/web`:

- Add `ProposalDTO` and `pendingProposals []ProposalDTO` to the state snapshot
  (`state.go`); push a `proposal` WS event when the pending set changes.
- New bearer-gated REST handlers in `handlers.go`, bypassing the dispatcher:
  - `POST /api/proposals/{id}/confirm`
  - `POST /api/proposals/{id}/reject`
- The composition root wires the proposal store into the web server and fans
  proposal updates through `multiSink` (a new `SendProposals` method, no-op on
  the TUI sink for now).

### 6. Frontend (`web/components/panels.tsx`, `web/lib`)

- Extend `lib/types.ts` with `Proposal` and `lib/store.ts` with a
  `proposals` slice fed by the `proposal` WS event and the snapshot.
- In the Active Positions card, when a position has a matching pending proposal,
  render a highlighted block: `Agent proposes SL X→Y · TP X→Y — <reasoning>`
  with **Confirm** and **Reject** buttons POSTing the two endpoints.
- Always render the SL/TP line for protected positions (today it shows only when
  non-zero; detection now populates it for externally-protected positions).

## Data Flow

```
Binance open orders ─┐
                     ├─(detect)→ Position{StopLoss,TakeProfit1,ExternallyProtected}
account snapshot ────┘                    │
                                          ├─→ reconciler: skip (protected)
                                          └─→ Job B reviewer
                                                   │ (adjustment for protected pos)
                                                   ▼
                                           proposalStore.Propose
                                                   │
                                multiSink.SendProposals → web WS → panel
                                                   │
                       operator clicks Confirm → POST /api/proposals/{id}/confirm
                                                   │
                            store.Confirm → execute: cancel exchange orders,
                                            place new bracket, record tracker
```

## Error Handling

- Detection failures are logged, non-fatal: a failed open-orders list leaves
  the position with zero levels for that tick (next resync retries). It must not
  crash the snapshot path.
- Confirm with unknown / already-resolved id → 404 / 409 from the handler.
- Confirm when the position has since closed → store drops it, returns a
  no-op success (matches `ActionQueue` "position already gone" behavior).
- Execution failure on confirm (cancel or place fails) → surface error to the
  handler (500) and keep the proposal pending so the operator can retry.

## Testing

- **Detection** (adapter, table-driven): mock open-order payloads → expected
  `StopLoss`/`TakeProfit1`/`ExternallyProtected`; futures STOP/TP_MARKET; spot
  OCO legs; mixed/partial (stop only, tp only).
- **Reconciler**: an `ExternallyProtected` position is neither flattened nor
  double-bracketed; a naked position still flattens; a Cerebro-tracked position
  still skips.
- **Proposal store**: propose → pending; supersede same-symbol; confirm executes
  once then removed; reject discards; position-gone prune drops it; assert **no
  timeout-based removal**.
- **Web handlers**: confirm/reject 200; unknown id; already-resolved; snapshot
  includes pending proposals.
- All unit tests run with `-race`. Money compared with `decimal.Equal`.

## Affected Files

- `internal/domain/position.go` — add `ExternallyProtected`.
- `internal/adapter/binance/futures/rest.go` — open-order detection + ID cache.
- `internal/adapter/binance/spot/rest.go` — OCO detection + ID cache.
- `internal/execution/reconciler.go` — protected-position guard branch.
- `internal/positionproposal/` — new package: proposal store + the
  `applyAdjustment` cancel→place→record helper + tests.
  `ActionExecutor.tightenStop` is left unchanged.
- `internal/adapter/web/state.go`, `handlers.go`, `server.go` — DTO, WS event,
  REST endpoints.
- `internal/app/uisink.go`, `runtime.go` — `SendProposals`, wiring.
- `web/lib/types.ts`, `web/lib/store.ts`, `web/components/panels.tsx` — UI.

## Rollout

Single feature branch off the current `fix/futures-current-price-mapping`
lineage (or a fresh branch from `main`). Detection + reconciler guard are the
safety-critical core; the proposal/confirm UI builds on top. Paper/demo behavior
is unchanged.

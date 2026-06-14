# Exchange-Side SL/TP Detection + Web-Confirmed Adjustments — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Detect protective orders set directly on Binance, stop the reconciler flattening such positions, and let the agent propose SL/TP adjustments the operator confirms on the web dashboard (no timeout).

**Architecture:** Adapters read live open conditional orders and map them onto `Position.StopLoss/TakeProfit1`, flagging `ExternallyProtected`. The reconciler skips protected positions (no flatten, no duplicate bracket). A new `internal/positionproposal` store holds agent adjustment proposals with no clock expiry; the web layer exposes confirm/reject REST endpoints that execute a cancel→place→record helper.

**Tech Stack:** Go 1.26, `shopspring/decimal`, `go-binance/v2`, `gorilla/websocket`, Next.js/React/Zustand frontend.

**Spec:** `docs/superpowers/specs/2026-06-14-exchange-side-bracket-detection-design.md`

---

## File Structure

- `internal/domain/position.go` — add `ExternallyProtected bool`.
- `internal/execution/reconciler.go` — protected-position guard branch.
- `internal/adapter/binance/futures/rest.go` — open-order detection + ID cache.
- `internal/adapter/binance/spot/rest.go` — OCO detection + ID cache.
- `internal/positionproposal/store.go` — new proposal store (no timeout).
- `internal/positionproposal/adjust.go` — `ApplyAdjustment` cancel→place→record helper.
- `internal/adapter/web/state.go` — `ProposalDTO`, snapshot field.
- `internal/adapter/web/server.go` — `SendProposals`, proposal state, snapshot.
- `internal/adapter/web/handlers.go` — confirm/reject REST endpoints.
- `internal/uistate/*` — `SendProposals` on the `Sink` interface.
- `internal/app/uisink.go` — `multiSink.SendProposals`.
- `internal/app/runtime.go` — wire store into reconciler + web server.
- `web/lib/types.ts`, `web/lib/store.ts`, `web/components/panels.tsx` — UI.

---

## Task 1: Add `ExternallyProtected` to the Position domain type

**Files:**
- Modify: `internal/domain/position.go:10-34`
- Test: `internal/domain/position_test.go`

- [ ] **Step 1: Add the field**

In `internal/domain/position.go`, inside the `Position` struct, after the
`Isolated bool` field (line 33), add:

```go
	// ExternallyProtected reports that StopLoss/TakeProfit1 were read from live
	// protective orders on the exchange that Cerebro did not place itself
	// (e.g. an SL/TP the operator set on the Binance website). When true the
	// reconciler must neither flatten the position nor attach its own bracket.
	ExternallyProtected bool
```

- [ ] **Step 2: Build to verify it compiles**

Run: `go build ./internal/domain/...`
Expected: no output (success).

- [ ] **Step 3: Commit**

```bash
git add internal/domain/position.go
git commit -m "feat(domain): add Position.ExternallyProtected flag"
```

---

## Task 2: Reconciler skips externally-protected positions

**Files:**
- Modify: `internal/execution/reconciler.go:107-120`
- Test: `internal/execution/reconciler_test.go`

- [ ] **Step 1: Write the failing test**

Add to `internal/execution/reconciler_test.go`. This asserts a protected
position is neither flattened nor bracketed. Reuse existing test stubs in that
file (`stubBroker`, `newTestReconciler` or equivalent — match the file's
existing helpers; if a helper differs, adapt the names to what's already there).

```go
func TestEnforceBrackets_SkipsExternallyProtected(t *testing.T) {
	broker := &stubBroker{}
	tracker := NewBracketTracker()
	pos := domain.Position{
		Symbol:              "BTC/USDT-PERP",
		Venue:               domain.VenueBinanceFutures,
		Side:                domain.SideBuy,
		Quantity:            decimal.NewFromInt(1),
		StopLoss:            decimal.NewFromInt(60000),
		TakeProfit1:         decimal.NewFromInt(70000),
		ExternallyProtected: true,
	}
	r := NewReconciler(ReconcilerDeps{
		Venue:     domain.VenueBinanceFutures,
		Broker:    broker,
		Tracker:   tracker,
		Router:    nil, // flatten must not be reached
		Env:       domain.EnvironmentPaper,
		Positions: func() []domain.Position { return []domain.Position{pos} },
	})
	r.enforceBrackets(context.Background())

	if len(broker.placedBrackets) != 0 {
		t.Errorf("expected no bracket placed, got %d", len(broker.placedBrackets))
	}
	if tracker.Has(pos.Symbol) {
		t.Error("expected symbol not tracked")
	}
}
```

> If `stubBroker` does not record `placedBrackets`, add a `placedBrackets
> []domain.BracketRequest` field and append to it in its `PlaceBracket` method.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/execution/ -run TestEnforceBrackets_SkipsExternallyProtected -v`
Expected: FAIL — current code calls `PlaceBracket` (or panics on nil Router via flatten).

- [ ] **Step 3: Add the guard branch**

In `internal/execution/reconciler.go`, in `enforceBrackets`, immediately after
the `if r.deps.Tracker.Has(pos.Symbol) { continue }` block (line 112-114) and
**before** the `if pos.StopLoss.IsZero() && pos.TakeProfit1.IsZero()` block,
insert:

```go
		if pos.ExternallyProtected {
			// Operator set SL/TP directly on the exchange. Respect it: do not
			// flatten and do not attach a duplicate Cerebro bracket.
			continue
		}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/execution/ -run TestEnforceBrackets_SkipsExternallyProtected -v`
Expected: PASS.

- [ ] **Step 5: Run the full execution suite with race**

Run: `go test -race ./internal/execution/...`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/execution/reconciler.go internal/execution/reconciler_test.go
git commit -m "feat(execution): reconciler skips externally-protected positions"
```

---

## Task 3: Futures exchange-side SL/TP detection

**Files:**
- Modify: `internal/adapter/binance/futures/rest.go` (add detection + ID cache; call from `fetchPositionSnapshot`)
- Test: `internal/adapter/binance/futures/rest_test.go`

**Context:** `fetchPositionSnapshot` (`rest.go:531`) builds the position map from
`NewGetAccountService`. go-binance exposes `NewListOpenOrdersService()` returning
`[]*futures.Order` with fields `Symbol`, `Type` (`futures.OrderType`),
`StopPrice string`, `ClosePosition bool`, `ReduceOnly bool`, `PositionSide`.
The relevant types are `futures.OrderTypeStopMarket` and
`futures.OrderTypeTakeProfitMarket`.

- [ ] **Step 1: Add a protective-orders cache field**

In the `FuturesBroker` struct (find the struct definition near the top of
`rest.go`), add alongside the existing `positions` map field:

```go
	// protective caches the exchange order IDs of detected externally-set
	// STOP_MARKET / TAKE_PROFIT_MARKET orders per symbol, so a confirmed
	// adjustment can cancel the exact orders. Guarded by b.mu.
	protective map[domain.Symbol]protectiveOrders
```

Add the type near the other small types in the file:

```go
// protectiveOrders holds the exchange order IDs of an externally-set
// stop / take-profit pair for one symbol.
type protectiveOrders struct {
	StopOrderID       string
	TakeProfitOrderID string
}
```

Initialise the map wherever the broker is constructed (find `&FuturesBroker{`
and add `protective: make(map[domain.Symbol]protectiveOrders),`).

- [ ] **Step 2: Write the failing test for the pure mapping function**

Add to `internal/adapter/binance/futures/rest_test.go`:

```go
func TestDetectProtectiveLevels_Futures(t *testing.T) {
	orders := []*futures.Order{
		{Symbol: "BTCUSDT", Type: futures.OrderTypeStopMarket, StopPrice: "60000", ClosePosition: true, OrderID: 111},
		{Symbol: "BTCUSDT", Type: futures.OrderTypeTakeProfitMarket, StopPrice: "70000", ClosePosition: true, OrderID: 222},
		{Symbol: "BTCUSDT", Type: futures.OrderTypeLimit, Price: "65000", OrderID: 333}, // ignored
	}
	sl, tp, ids := detectProtectiveLevels(orders)
	if !sl["BTCUSDT"].Equal(decimal.RequireFromString("60000")) {
		t.Errorf("stop = %s, want 60000", sl["BTCUSDT"])
	}
	if !tp["BTCUSDT"].Equal(decimal.RequireFromString("70000")) {
		t.Errorf("tp = %s, want 70000", tp["BTCUSDT"])
	}
	if ids["BTCUSDT"].StopOrderID != "111" || ids["BTCUSDT"].TakeProfitOrderID != "222" {
		t.Errorf("ids = %+v, want stop=111 tp=222", ids["BTCUSDT"])
	}
}
```

- [ ] **Step 2b: Run it to confirm it fails**

Run: `go test ./internal/adapter/binance/futures/ -run TestDetectProtectiveLevels_Futures -v`
Expected: FAIL — `detectProtectiveLevels` undefined.

- [ ] **Step 3: Implement the pure mapping function**

Add to `rest.go`. Keys are the raw exchange symbol (`BTCUSDT`); the caller maps
to `domain.Symbol` when merging.

```go
// detectProtectiveLevels extracts externally-set stop / take-profit levels and
// their order IDs from a list of open futures orders. Only reduce-only or
// closePosition conditional orders count as protective.
func detectProtectiveLevels(orders []*futures.Order) (
	stops map[string]decimal.Decimal,
	tps map[string]decimal.Decimal,
	ids map[string]protectiveOrders,
) {
	stops = make(map[string]decimal.Decimal)
	tps = make(map[string]decimal.Decimal)
	ids = make(map[string]protectiveOrders)
	for _, o := range orders {
		if o == nil || (!o.ClosePosition && !o.ReduceOnly) {
			continue
		}
		px, err := decimal.NewFromString(o.StopPrice)
		if err != nil || px.IsZero() {
			continue
		}
		cur := ids[o.Symbol]
		switch o.Type {
		case futures.OrderTypeStopMarket:
			stops[o.Symbol] = px
			cur.StopOrderID = strconv.FormatInt(o.OrderID, 10)
		case futures.OrderTypeTakeProfitMarket:
			tps[o.Symbol] = px
			cur.TakeProfitOrderID = strconv.FormatInt(o.OrderID, 10)
		default:
			continue
		}
		ids[o.Symbol] = cur
	}
	return stops, tps, ids
}
```

- [ ] **Step 4: Run the mapping test to verify it passes**

Run: `go test ./internal/adapter/binance/futures/ -run TestDetectProtectiveLevels_Futures -v`
Expected: PASS.

- [ ] **Step 5: Wire detection into `fetchPositionSnapshot`**

In `fetchPositionSnapshot`, after the loop that fills `next` (just before
`return next, nil` at ~line 569), add an open-orders fetch and merge. On error,
log and continue (non-fatal). `b.client` is the `*futures.Client`.

```go
	openOrders, ooErr := b.client.NewListOpenOrdersService().Do(ctx)
	if ooErr != nil {
		slog.Warn("futures open-orders fetch for SL/TP detection failed", "error", ooErr)
		return next, nil
	}
	stops, tps, ids := detectProtectiveLevels(openOrders)
	detected := make(map[domain.Symbol]protectiveOrders, len(ids))
	for rawSym, po := range ids {
		sym := domain.Symbol(normaliseFuturesSymbol(rawSym))
		pos, ok := next[sym]
		if !ok {
			continue
		}
		if sl, has := stops[rawSym]; has {
			pos.StopLoss = sl
		}
		if tp, has := tps[rawSym]; has {
			pos.TakeProfit1 = tp
		}
		if !pos.StopLoss.IsZero() || !pos.TakeProfit1.IsZero() {
			pos.ExternallyProtected = true
		}
		next[sym] = pos
		detected[sym] = po
	}
	b.mu.Lock()
	b.protective = detected
	b.mu.Unlock()
```

> `normaliseFuturesSymbol` converts `BTCUSDT` → the `domain.Symbol` form used
> in `next` (e.g. `BTC/USDT-PERP`). Find the existing helper this file already
> uses to build `pos.Symbol` inside `futuresAccountPositionToDomain` and reuse
> the same conversion. If it is inline, extract it into
> `normaliseFuturesSymbol(raw string) string` and use it in both places.

- [ ] **Step 6: Preserve flag through `applyPositionSnapshot`**

In `applyPositionSnapshot` (`rest.go:475`), the snapshot now carries
`StopLoss`/`TakeProfit1`/`ExternallyProtected` freshly detected each resync, so
the existing carry-forward of `existing.StopLoss`/`existing.TakeProfit1` would
**overwrite** fresh detection with stale values. Change those two lines to only
carry forward when the snapshot itself has no protective data:

```go
			if pos.StopLoss.IsZero() && pos.TakeProfit1.IsZero() && !pos.ExternallyProtected {
				pos.StopLoss = existing.StopLoss
				pos.TakeProfit1 = existing.TakeProfit1
			}
```

(Leave the `Strategy` / `CorrelationID` / `OpenedAt` carry-forward unchanged.)

- [ ] **Step 7: Build and run the futures suite with race**

Run: `go test -race ./internal/adapter/binance/futures/...`
Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add internal/adapter/binance/futures/rest.go internal/adapter/binance/futures/rest_test.go
git commit -m "feat(futures): detect externally-set SL/TP from open orders"
```

---

## Task 4: Spot exchange-side SL/TP detection

**Files:**
- Modify: `internal/adapter/binance/spot/rest.go` (detection + ID cache; call from the snapshot path)
- Test: `internal/adapter/binance/spot/rest_test.go`

**Context:** Spot positions are synthesized from balances in
`rebuildPositionsLocked` (`rest.go:729`); the symbol form is
`domain.Symbol(asset + "/USDT")` (line 740). go-binance spot exposes
`NewListOpenOrdersService().Do(ctx)` → `[]*gobinance.Order` with fields
`Symbol`, `Type` (`gobinance.OrderType`), `Side`, `StopPrice string`,
`Price string`, `OrderID int64`, `OrderListId int64`. Protective leg types:
`gobinance.OrderTypeStopLossLimit` (stop) and `gobinance.OrderTypeLimitMaker`
(the OCO take-profit leg).

- [ ] **Step 1: Add a protective cache + type to SpotBroker**

In the `SpotBroker` struct add (guarded by the existing `b.mu`):

```go
	// protective caches detected externally-set protective order IDs per symbol
	// (OCO list ID + leg order IDs) so a confirmed adjustment can cancel them.
	protective map[domain.Symbol]spotProtectiveOrders
```

Add the type:

```go
// spotProtectiveOrders holds the IDs needed to cancel an externally-set spot
// protective order (OCO list, or a lone STOP_LOSS_LIMIT).
type spotProtectiveOrders struct {
	ListID            string // OCO orderListId; "" for a lone stop
	StopOrderID       string
	TakeProfitOrderID string
}
```

Initialise it where `&SpotBroker{` is constructed:
`protective: make(map[domain.Symbol]spotProtectiveOrders),`.

- [ ] **Step 2: Write the failing mapping test**

Add to `internal/adapter/binance/spot/rest_test.go`:

```go
func TestDetectProtectiveLevels_Spot(t *testing.T) {
	orders := []*gobinance.Order{
		{Symbol: "ETHUSDT", Type: gobinance.OrderTypeStopLossLimit, Side: gobinance.SideTypeSell, StopPrice: "2800", OrderID: 11, OrderListId: 99},
		{Symbol: "ETHUSDT", Type: gobinance.OrderTypeLimitMaker, Side: gobinance.SideTypeSell, Price: "3500", OrderID: 12, OrderListId: 99},
		{Symbol: "ETHUSDT", Type: gobinance.OrderTypeLimit, Price: "3000", OrderID: 13}, // ignored
	}
	sl, tp, ids := detectSpotProtectiveLevels(orders)
	if !sl["ETHUSDT"].Equal(decimal.RequireFromString("2800")) {
		t.Errorf("stop = %s, want 2800", sl["ETHUSDT"])
	}
	if !tp["ETHUSDT"].Equal(decimal.RequireFromString("3500")) {
		t.Errorf("tp = %s, want 3500", tp["ETHUSDT"])
	}
	got := ids["ETHUSDT"]
	if got.ListID != "99" || got.StopOrderID != "11" || got.TakeProfitOrderID != "12" {
		t.Errorf("ids = %+v, want list=99 stop=11 tp=12", got)
	}
}
```

- [ ] **Step 2b: Run it to confirm it fails**

Run: `go test ./internal/adapter/binance/spot/ -run TestDetectProtectiveLevels_Spot -v`
Expected: FAIL — `detectSpotProtectiveLevels` undefined.

- [ ] **Step 3: Implement the pure mapping function**

```go
// detectSpotProtectiveLevels extracts externally-set stop / take-profit levels
// and their order IDs from open spot orders. STOP_LOSS_LIMIT supplies the stop
// (from StopPrice); the OCO LIMIT_MAKER leg supplies the take-profit (from
// Price). Keys are raw exchange symbols (e.g. ETHUSDT).
func detectSpotProtectiveLevels(orders []*gobinance.Order) (
	stops map[string]decimal.Decimal,
	tps map[string]decimal.Decimal,
	ids map[string]spotProtectiveOrders,
) {
	stops = make(map[string]decimal.Decimal)
	tps = make(map[string]decimal.Decimal)
	ids = make(map[string]spotProtectiveOrders)
	for _, o := range orders {
		if o == nil {
			continue
		}
		cur := ids[o.Symbol]
		if o.OrderListId > 0 {
			cur.ListID = strconv.FormatInt(o.OrderListId, 10)
		}
		switch o.Type {
		case gobinance.OrderTypeStopLossLimit:
			px, err := decimal.NewFromString(o.StopPrice)
			if err != nil || px.IsZero() {
				continue
			}
			stops[o.Symbol] = px
			cur.StopOrderID = strconv.FormatInt(o.OrderID, 10)
		case gobinance.OrderTypeLimitMaker:
			px, err := decimal.NewFromString(o.Price)
			if err != nil || px.IsZero() {
				continue
			}
			tps[o.Symbol] = px
			cur.TakeProfitOrderID = strconv.FormatInt(o.OrderID, 10)
		default:
			continue
		}
		ids[o.Symbol] = cur
	}
	return stops, tps, ids
}
```

- [ ] **Step 4: Run the mapping test to verify it passes**

Run: `go test ./internal/adapter/binance/spot/ -run TestDetectProtectiveLevels_Spot -v`
Expected: PASS.

- [ ] **Step 5: Wire detection into the snapshot path**

Find the method that fetches the authoritative snapshot before
`applyPositionSnapshot`/`rebuildPositionsLocked` is called on resync (the spot
analogue of futures `fetchPositionSnapshot` — `fetchBalanceSnapshot` near
`rest.go:498-537`). After positions are rebuilt for the snapshot, fetch open
orders and merge, non-fatally:

```go
	openOrders, ooErr := b.client.NewListOpenOrdersService().Do(ctx)
	if ooErr != nil {
		slog.Warn("spot open-orders fetch for SL/TP detection failed", "error", ooErr)
	} else {
		stops, tps, ids := detectSpotProtectiveLevels(openOrders)
		detected := make(map[domain.Symbol]spotProtectiveOrders, len(ids))
		b.mu.Lock()
		for rawSym, po := range ids {
			sym := domain.Symbol(spotSymbolToDomain(rawSym))
			pos, ok := b.positions[sym]
			if !ok {
				continue
			}
			if sl, has := stops[rawSym]; has {
				pos.StopLoss = sl
			}
			if tp, has := tps[rawSym]; has {
				pos.TakeProfit1 = tp
			}
			if !pos.StopLoss.IsZero() || !pos.TakeProfit1.IsZero() {
				pos.ExternallyProtected = true
			}
			b.positions[sym] = pos
			detected[sym] = po
		}
		b.protective = detected
		b.mu.Unlock()
	}
```

> Delete the `for asset := range ...` no-op line — it is a placeholder marker.
> `spotSymbolToDomain(raw string) string` converts `ETHUSDT` → `ETH/USDT` to
> match the `asset + "/USDT"` form built in `rebuildPositionsLocked`. If no such
> helper exists, add one: strip a trailing `USDT` and insert `/USDT`. Place the
> merge wherever positions are accessible under lock in the snapshot path; if
> the snapshot uses a local map rather than `b.positions`, merge into that map
> before it is stored, mirroring the futures approach in Task 3.

- [ ] **Step 6: Build and run the spot suite with race**

Run: `go test -race ./internal/adapter/binance/spot/...`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/adapter/binance/spot/rest.go internal/adapter/binance/spot/rest_test.go
git commit -m "feat(spot): detect externally-set SL/TP from open OCO orders"
```

---

## Task 5: Proposal store package (no timeout)

**Files:**
- Create: `internal/positionproposal/store.go`
- Test: `internal/positionproposal/store_test.go`

**Context:** A standalone store, deliberately separate from
`execution.ActionQueue`. No clock expiry: a proposal lives until `Confirm` or
`Reject`. One proposal per symbol (newer supersedes). A position-gone guard is
the only automatic removal.

- [ ] **Step 1: Write the store + types**

Create `internal/positionproposal/store.go`:

```go
// Package positionproposal holds agent-originated SL/TP adjustment proposals
// that require explicit operator confirmation from the web dashboard. Unlike
// execution.ActionQueue, proposals never expire on a clock — they live until
// the operator confirms or rejects, or until the underlying position closes.
package positionproposal

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/azhar/cerebro/internal/domain"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// Proposal is a pending SL/TP adjustment awaiting operator confirmation.
type Proposal struct {
	ID           string
	Symbol       domain.Symbol
	Venue        domain.Venue
	Side         domain.Side
	CurrentStop  decimal.Decimal
	CurrentTP    decimal.Decimal
	ProposedStop decimal.Decimal
	ProposedTP   decimal.Decimal
	Reasoning    string
	CreatedAt    time.Time
}

// ApplyFunc executes a confirmed proposal (cancel exchange protection, place a
// new bracket at the proposed levels, record it). Implemented in adjust.go and
// injected so the store has no broker dependency.
type ApplyFunc func(ctx context.Context, p Proposal) error

// Store holds pending proposals. Safe for concurrent use.
type Store struct {
	mu             sync.Mutex
	bySymbol       map[domain.Symbol]*Proposal // one live proposal per symbol
	byID           map[string]*Proposal
	apply          ApplyFunc
	positionExists func(domain.Symbol) bool
	onChange       func() // notified after any mutation so the UI can refresh
}

// NewStore builds a Store. apply executes confirmed proposals; onChange (may be
// nil) fires after every mutation so the caller can push a fresh snapshot.
func NewStore(apply ApplyFunc, onChange func()) *Store {
	return &Store{
		bySymbol: make(map[domain.Symbol]*Proposal),
		byID:     make(map[string]*Proposal),
		apply:    apply,
		onChange: onChange,
	}
}

// SetPositionExists installs the guard consulted before execution and during
// Prune. When it returns false for a symbol, that proposal is dropped.
func (s *Store) SetPositionExists(fn func(domain.Symbol) bool) {
	s.mu.Lock()
	s.positionExists = fn
	s.mu.Unlock()
}

// Propose adds or replaces the proposal for a symbol and returns its ID.
func (s *Store) Propose(p Proposal) string {
	if p.ID == "" {
		p.ID = uuid.New().String()
	}
	if p.CreatedAt.IsZero() {
		p.CreatedAt = time.Now().UTC()
	}
	s.mu.Lock()
	if old, ok := s.bySymbol[p.Symbol]; ok {
		delete(s.byID, old.ID) // supersede the previous proposal for this symbol
	}
	cp := p
	s.bySymbol[p.Symbol] = &cp
	s.byID[p.ID] = &cp
	s.mu.Unlock()
	slog.Info("proposal: enqueued", "id", p.ID, "symbol", p.Symbol,
		"proposed_stop", p.ProposedStop, "proposed_tp", p.ProposedTP)
	s.notify()
	return p.ID
}

// Pending returns a snapshot copy of all live proposals.
func (s *Store) Pending() []Proposal {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Proposal, 0, len(s.byID))
	for _, p := range s.byID {
		out = append(out, *p)
	}
	return out
}

// Confirm executes the proposal then removes it. Returns an error for an
// unknown ID. If the position is already gone the proposal is dropped and a
// nil error is returned (nothing to do).
func (s *Store) Confirm(ctx context.Context, id string) error {
	s.mu.Lock()
	p, ok := s.byID[id]
	if !ok {
		s.mu.Unlock()
		return fmt.Errorf("positionproposal: unknown id %q", id)
	}
	snapshot := *p
	guard := s.positionExists
	s.mu.Unlock()

	if guard != nil && !guard(snapshot.Symbol) {
		s.remove(snapshot.ID, snapshot.Symbol)
		slog.Info("proposal: dropped on confirm; position gone",
			"id", id, "symbol", snapshot.Symbol)
		return nil
	}
	if err := s.apply(ctx, snapshot); err != nil {
		// Keep the proposal pending so the operator can retry.
		return fmt.Errorf("positionproposal: apply %q: %w", id, err)
	}
	s.remove(snapshot.ID, snapshot.Symbol)
	slog.Info("proposal: confirmed and applied", "id", id, "symbol", snapshot.Symbol)
	return nil
}

// Reject removes a proposal without executing. Errors on an unknown ID.
func (s *Store) Reject(id string) error {
	s.mu.Lock()
	p, ok := s.byID[id]
	if !ok {
		s.mu.Unlock()
		return fmt.Errorf("positionproposal: unknown id %q", id)
	}
	sym := p.Symbol
	delete(s.byID, id)
	delete(s.bySymbol, sym)
	s.mu.Unlock()
	slog.Info("proposal: rejected", "id", id, "symbol", sym)
	s.notify()
	return nil
}

// Prune drops proposals whose position no longer exists. Call periodically.
func (s *Store) Prune() {
	s.mu.Lock()
	guard := s.positionExists
	var gone []*Proposal
	if guard != nil {
		for _, p := range s.byID {
			if !guard(p.Symbol) {
				gone = append(gone, p)
			}
		}
		for _, p := range gone {
			delete(s.byID, p.ID)
			delete(s.bySymbol, p.Symbol)
		}
	}
	s.mu.Unlock()
	for _, p := range gone {
		slog.Info("proposal: pruned; position gone", "id", p.ID, "symbol", p.Symbol)
	}
	if len(gone) > 0 {
		s.notify()
	}
}

func (s *Store) remove(id string, sym domain.Symbol) {
	s.mu.Lock()
	delete(s.byID, id)
	if cur, ok := s.bySymbol[sym]; ok && cur.ID == id {
		delete(s.bySymbol, sym)
	}
	s.mu.Unlock()
	s.notify()
}

func (s *Store) notify() {
	if s.onChange != nil {
		s.onChange()
	}
}
```

- [ ] **Step 2: Write the store tests**

Create `internal/positionproposal/store_test.go`:

```go
package positionproposal

import (
	"context"
	"testing"

	"github.com/azhar/cerebro/internal/domain"
	"github.com/shopspring/decimal"
)

func mkProposal(sym domain.Symbol) Proposal {
	return Proposal{Symbol: sym, Venue: domain.VenueBinanceFutures, Side: domain.SideBuy,
		ProposedStop: decimal.NewFromInt(60000), ProposedTP: decimal.NewFromInt(70000)}
}

func TestProposeSupersedesSameSymbol(t *testing.T) {
	s := NewStore(func(context.Context, Proposal) error { return nil }, nil)
	s.Propose(mkProposal("BTC/USDT-PERP"))
	s.Propose(mkProposal("BTC/USDT-PERP"))
	if got := len(s.Pending()); got != 1 {
		t.Fatalf("pending = %d, want 1", got)
	}
}

func TestConfirmExecutesOnce(t *testing.T) {
	calls := 0
	s := NewStore(func(context.Context, Proposal) error { calls++; return nil }, nil)
	id := s.Propose(mkProposal("BTC/USDT-PERP"))
	if err := s.Confirm(context.Background(), id); err != nil {
		t.Fatalf("confirm: %v", err)
	}
	if calls != 1 {
		t.Fatalf("apply calls = %d, want 1", calls)
	}
	if err := s.Confirm(context.Background(), id); err == nil {
		t.Fatal("second confirm should error (unknown id)")
	}
}

func TestRejectDiscards(t *testing.T) {
	calls := 0
	s := NewStore(func(context.Context, Proposal) error { calls++; return nil }, nil)
	id := s.Propose(mkProposal("BTC/USDT-PERP"))
	if err := s.Reject(id); err != nil {
		t.Fatalf("reject: %v", err)
	}
	if calls != 0 || len(s.Pending()) != 0 {
		t.Fatalf("calls=%d pending=%d, want 0/0", calls, len(s.Pending()))
	}
}

func TestConfirmDropsWhenPositionGone(t *testing.T) {
	calls := 0
	s := NewStore(func(context.Context, Proposal) error { calls++; return nil }, nil)
	s.SetPositionExists(func(domain.Symbol) bool { return false })
	id := s.Propose(mkProposal("BTC/USDT-PERP"))
	if err := s.Confirm(context.Background(), id); err != nil {
		t.Fatalf("confirm: %v", err)
	}
	if calls != 0 {
		t.Fatalf("apply should not run when position gone, calls=%d", calls)
	}
	if len(s.Pending()) != 0 {
		t.Fatal("proposal should be dropped")
	}
}

func TestPrunePositionGone(t *testing.T) {
	s := NewStore(func(context.Context, Proposal) error { return nil }, nil)
	s.SetPositionExists(func(domain.Symbol) bool { return false })
	s.Propose(mkProposal("BTC/USDT-PERP"))
	s.Prune()
	if len(s.Pending()) != 0 {
		t.Fatal("prune should drop proposals whose position is gone")
	}
}
```

- [ ] **Step 3: Run the store tests with race**

Run: `go test -race ./internal/positionproposal/...`
Expected: PASS (all five tests).

- [ ] **Step 4: Commit**

```bash
git add internal/positionproposal/store.go internal/positionproposal/store_test.go
git commit -m "feat(positionproposal): no-timeout proposal store"
```

---

## Task 6: ApplyAdjustment helper (cancel → place → record)

**Files:**
- Create: `internal/positionproposal/adjust.go`
- Test: `internal/positionproposal/adjust_test.go`
- Modify: `internal/adapter/binance/futures/rest.go` (add `ProtectiveBracket`)
- Modify: `internal/adapter/binance/spot/rest.go` (add `ProtectiveBracket`)

**Context:** On confirm we must cancel the operator's *exchange* protective
orders (IDs cached in Tasks 3-4), then place a Cerebro bracket at the proposed
levels and record it in the `BracketTracker` so the reconciler treats it as
Cerebro-owned from then on. The helper depends on small interfaces, not the
concrete brokers.

- [ ] **Step 1: Expose cached protective orders from the futures broker**

Add to `internal/adapter/binance/futures/rest.go`:

```go
// ProtectiveBracket returns a BracketResponse describing the externally-set
// protective orders detected for sym, suitable for CancelBracket. ok is false
// when no externally-set protection is cached.
func (b *FuturesBroker) ProtectiveBracket(sym domain.Symbol) (domain.BracketResponse, bool) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	po, ok := b.protective[sym]
	if !ok {
		return domain.BracketResponse{}, false
	}
	return domain.BracketResponse{
		Symbol:            sym,
		StopOrderID:       po.StopOrderID,
		TakeProfitOrderID: po.TakeProfitOrderID,
	}, true
}
```

- [ ] **Step 2: Expose cached protective orders from the spot broker**

Add to `internal/adapter/binance/spot/rest.go`:

```go
// ProtectiveBracket returns a BracketResponse describing the externally-set
// protective orders detected for sym, suitable for CancelBracket. ok is false
// when no externally-set protection is cached.
func (b *SpotBroker) ProtectiveBracket(sym domain.Symbol) (domain.BracketResponse, bool) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	po, ok := b.protective[sym]
	if !ok {
		return domain.BracketResponse{}, false
	}
	return domain.BracketResponse{
		Symbol:            sym,
		ListID:            po.ListID,
		StopOrderID:       po.StopOrderID,
		TakeProfitOrderID: po.TakeProfitOrderID,
	}, true
}
```

- [ ] **Step 3: Write the failing helper test**

Create `internal/positionproposal/adjust_test.go`:

```go
package positionproposal

import (
	"context"
	"testing"

	"github.com/azhar/cerebro/internal/domain"
	"github.com/shopspring/decimal"
)

type fakeAdjuster struct {
	cancelled []domain.BracketResponse
	placed    []domain.BracketRequest
	protective map[domain.Symbol]domain.BracketResponse
	recorded  map[domain.Symbol]domain.BracketResponse
}

func (f *fakeAdjuster) PlaceBracket(_ context.Context, req domain.BracketRequest) (domain.BracketResponse, error) {
	f.placed = append(f.placed, req)
	return domain.BracketResponse{Symbol: req.Symbol, StopOrderID: "new-stop"}, nil
}
func (f *fakeAdjuster) CancelBracket(_ context.Context, resp domain.BracketResponse) error {
	f.cancelled = append(f.cancelled, resp)
	return nil
}
func (f *fakeAdjuster) ProtectiveBracket(sym domain.Symbol) (domain.BracketResponse, bool) {
	r, ok := f.protective[sym]
	return r, ok
}
func (f *fakeAdjuster) Record(sym domain.Symbol, resp domain.BracketResponse) {
	if f.recorded == nil {
		f.recorded = map[domain.Symbol]domain.BracketResponse{}
	}
	f.recorded[sym] = resp
}

func TestApplyAdjustment_CancelsThenPlacesAndRecords(t *testing.T) {
	fa := &fakeAdjuster{protective: map[domain.Symbol]domain.BracketResponse{
		"BTC/USDT-PERP": {Symbol: "BTC/USDT-PERP", StopOrderID: "old-stop", TakeProfitOrderID: "old-tp"},
	}}
	apply := ApplyAdjustment(fa, fa, fa)
	p := Proposal{
		Symbol: "BTC/USDT-PERP", Venue: domain.VenueBinanceFutures, Side: domain.SideBuy,
		ProposedStop: decimal.NewFromInt(61000), ProposedTP: decimal.NewFromInt(72000),
	}
	if err := apply(context.Background(), p); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(fa.cancelled) != 1 || fa.cancelled[0].StopOrderID != "old-stop" {
		t.Fatalf("expected old protection cancelled, got %+v", fa.cancelled)
	}
	if len(fa.placed) != 1 || !fa.placed[0].StopLoss.Equal(decimal.NewFromInt(61000)) {
		t.Fatalf("expected new bracket at proposed stop, got %+v", fa.placed)
	}
	if _, ok := fa.recorded["BTC/USDT-PERP"]; !ok {
		t.Fatal("expected new bracket recorded in tracker")
	}
}
```

- [ ] **Step 3b: Run it to confirm it fails**

Run: `go test ./internal/positionproposal/ -run TestApplyAdjustment -v`
Expected: FAIL — `ApplyAdjustment` undefined.

- [ ] **Step 4: Implement the helper**

Create `internal/positionproposal/adjust.go`:

```go
package positionproposal

import (
	"context"
	"fmt"

	"github.com/azhar/cerebro/internal/domain"
)

// Bracketer places and cancels protective brackets (satisfied by port.Broker).
type Bracketer interface {
	PlaceBracket(ctx context.Context, req domain.BracketRequest) (domain.BracketResponse, error)
	CancelBracket(ctx context.Context, resp domain.BracketResponse) error
}

// ProtectiveLookup exposes the externally-set protective orders cached by the
// adapter for a symbol (implemented by *futures.FuturesBroker / *spot.SpotBroker).
type ProtectiveLookup interface {
	ProtectiveBracket(sym domain.Symbol) (domain.BracketResponse, bool)
}

// Recorder records a freshly-placed bracket as Cerebro-owned (satisfied by
// *execution.BracketTracker).
type Recorder interface {
	Record(sym domain.Symbol, resp domain.BracketResponse)
}

// ApplyAdjustment returns an ApplyFunc that, on confirmation, cancels the
// operator's externally-set protective orders, places a Cerebro bracket at the
// proposed SL/TP, and records it so the reconciler treats it as Cerebro-owned.
func ApplyAdjustment(b Bracketer, look ProtectiveLookup, rec Recorder) ApplyFunc {
	return func(ctx context.Context, p Proposal) error {
		if existing, ok := look.ProtectiveBracket(p.Symbol); ok {
			if err := b.CancelBracket(ctx, existing); err != nil {
				return fmt.Errorf("cancel existing protection for %s: %w", p.Symbol, err)
			}
		}
		req := domain.BracketRequest{
			Symbol:     p.Symbol,
			Venue:      p.Venue,
			Side:       p.Side,
			StopLoss:   p.ProposedStop,
			TakeProfit: p.ProposedTP,
			ClientTag:  "proposal_adjust",
		}
		resp, err := b.PlaceBracket(ctx, req)
		if err != nil {
			return fmt.Errorf("place adjusted bracket for %s: %w", p.Symbol, err)
		}
		rec.Record(p.Symbol, resp)
		return nil
	}
}
```

> `BracketRequest` omits `Quantity`; the futures/spot `PlaceBracket` use
> `closePosition`/full-position OCO semantics, so quantity is not required for a
> protective close. If a venue's `PlaceBracket` rejects a zero quantity during
> integration, set `req.Quantity` from the live position inside the ApplyFunc
> closure (the runtime wiring in Task 9 has `positionsForVenue` available).

- [ ] **Step 5: Run helper test + full package with race**

Run: `go test -race ./internal/positionproposal/...`
Expected: PASS.

- [ ] **Step 6: Build the adapters**

Run: `go build ./internal/adapter/binance/...`
Expected: success.

- [ ] **Step 7: Commit**

```bash
git add internal/positionproposal/adjust.go internal/positionproposal/adjust_test.go internal/adapter/binance/futures/rest.go internal/adapter/binance/spot/rest.go
git commit -m "feat(positionproposal): ApplyAdjustment cancel-place-record helper"
```

---

## Task 7: Web transport — DTO, snapshot, WS event, REST endpoints

**Files:**
- Modify: `internal/adapter/web/state.go` (add `ProposalDTO` + snapshot field)
- Modify: `internal/adapter/web/server.go` (`proposals` state, `SendProposals`, snapshot)
- Modify: `internal/adapter/web/handlers.go` (confirm/reject routes)
- Test: `internal/adapter/web/handlers_test.go`

**Context:** The Server struct (`server.go:40`) holds DTO slices under `s.mu` and
exposes `Send*` methods that update state then `broadcast(typ, data)`. The
`snapshot` struct is assembled in `Snapshot()` (`server.go:236`). Existing routes
are registered in `handlers.go:routes` and gated by `s.auth`. Per Approach B the
confirm/reject endpoints bypass the ChatOps dispatcher entirely.

- [ ] **Step 1: Add ProposalDTO + a confirmer interface (state.go)**

Append to `internal/adapter/web/state.go`:

```go
// ProposalDTO is one pending agent SL/TP adjustment awaiting operator action.
type ProposalDTO struct {
	ID           string `json:"id"`
	Symbol       string `json:"symbol"`
	Venue        string `json:"venue"`
	Side         string `json:"side"`
	CurrentStop  string `json:"currentStop"`
	CurrentTP    string `json:"currentTp"`
	ProposedStop string `json:"proposedStop"`
	ProposedTP   string `json:"proposedTp"`
	Reasoning    string `json:"reasoning"`
	CreatedAt    int64  `json:"createdAt"` // unix millis
}

func proposalToDTO(p positionproposal.Proposal) ProposalDTO {
	return ProposalDTO{
		ID:           p.ID,
		Symbol:       string(p.Symbol),
		Venue:        string(p.Venue),
		Side:         string(p.Side),
		CurrentStop:  p.CurrentStop.String(),
		CurrentTP:    p.CurrentTP.String(),
		ProposedStop: p.ProposedStop.String(),
		ProposedTP:   p.ProposedTP.String(),
		Reasoning:    p.Reasoning,
		CreatedAt:    p.CreatedAt.UnixMilli(),
	}
}
```

Add the import `"github.com/azhar/cerebro/internal/positionproposal"` to
`state.go`.

- [ ] **Step 2: Define the ProposalController interface (server.go)**

The server confirms/rejects through a narrow interface (satisfied by
`*positionproposal.Store`), so the web package does not import broker code. Add
near the top of `server.go`:

```go
// ProposalController is the subset of positionproposal.Store the web server
// needs. Confirm executes the adjustment; Reject discards it.
type ProposalController interface {
	Confirm(ctx context.Context, id string) error
	Reject(id string) error
}
```

Add two fields to the `Server` struct (under the existing state block):

```go
	proposals          []ProposalDTO
	proposalController ProposalController
```

Add a setter near `SetDispatcher`:

```go
// SetProposalController wires the proposal store for confirm/reject handling.
func (s *Server) SetProposalController(c ProposalController) {
	s.proposalController = c
}
```

- [ ] **Step 3: Add SendProposals + include in snapshot (server.go)**

Add the method (mirrors `SendPositions`):

```go
// SendProposals replaces the pending-proposal snapshot and broadcasts it.
func (s *Server) SendProposals(proposals []positionproposal.Proposal) {
	dtos := make([]ProposalDTO, 0, len(proposals))
	for _, p := range proposals {
		dtos = append(dtos, proposalToDTO(p))
	}
	s.mu.Lock()
	s.proposals = dtos
	s.mu.Unlock()
	s.broadcast("proposals", dtos)
}
```

In the `snapshot` struct definition (search `type snapshot struct` in
`server.go`), add:

```go
	Proposals []ProposalDTO `json:"proposals"`
```

In `Snapshot()`, add to the `snap := snapshot{...}` literal:

```go
		Proposals: append([]ProposalDTO(nil), s.proposals...),
```

Add the `positionproposal` import to `server.go`.

- [ ] **Step 4: Write the failing handler test**

Add to `internal/adapter/web/handlers_test.go`:

```go
type stubProposalController struct {
	confirmed, rejected []string
	confirmErr          error
}

func (s *stubProposalController) Confirm(_ context.Context, id string) error {
	s.confirmed = append(s.confirmed, id)
	return s.confirmErr
}
func (s *stubProposalController) Reject(id string) error {
	s.rejected = append(s.rejected, id)
	return nil
}

func TestHandleProposalConfirm(t *testing.T) {
	ctrl := &stubProposalController{}
	srv := newTestServer(t) // existing helper in handlers_test.go / server_test.go
	srv.SetProposalController(ctrl)

	req := httptest.NewRequest(http.MethodPost, "/api/proposals/abc-123/confirm", nil)
	rec := httptest.NewRecorder()
	srv.handleProposalAction(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if len(ctrl.confirmed) != 1 || ctrl.confirmed[0] != "abc-123" {
		t.Fatalf("confirmed = %v, want [abc-123]", ctrl.confirmed)
	}
}
```

> If no `newTestServer` helper exists, construct the server the same way
> `server_test.go` does in its existing tests (match that constructor).

- [ ] **Step 4b: Run to confirm it fails**

Run: `go test ./internal/adapter/web/ -run TestHandleProposalConfirm -v`
Expected: FAIL — `handleProposalAction` undefined.

- [ ] **Step 5: Add the routes + handler (handlers.go)**

In `routes`, after the existing `POST /api/command` line, add:

```go
	mux.Handle("POST /api/proposals/{id}/confirm", s.auth(http.HandlerFunc(s.handleProposalAction)))
	mux.Handle("POST /api/proposals/{id}/reject", s.auth(http.HandlerFunc(s.handleProposalAction)))
```

Add the handler. It distinguishes confirm vs reject by the URL suffix
(`r.URL.Path`), reads the `{id}` path value (Go 1.22+ `r.PathValue`), and maps
store errors to status codes:

```go
func (s *Server) handleProposalAction(w http.ResponseWriter, r *http.Request) {
	if s.proposalController == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "proposals not configured"})
		return
	}
	id := r.PathValue("id")
	if id == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing proposal id"})
		return
	}
	reject := strings.HasSuffix(r.URL.Path, "/reject")
	var err error
	if reject {
		err = s.proposalController.Reject(id)
	} else {
		err = s.proposalController.Confirm(r.Context(), id)
	}
	if err != nil {
		// Unknown id → 404; execution failure on confirm → 500.
		if strings.Contains(err.Error(), "unknown id") {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
```

Add `"strings"` to the `handlers.go` import block.

- [ ] **Step 6: Run handler test + full web suite with race**

Run: `go test -race ./internal/adapter/web/...`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/adapter/web/state.go internal/adapter/web/server.go internal/adapter/web/handlers.go internal/adapter/web/handlers_test.go
git commit -m "feat(web): proposal DTO, snapshot, WS event, confirm/reject endpoints"
```

---

## Task 8: Extend the UI Sink with SendProposals

**Files:**
- Modify: `internal/uistate/uistate.go:88-102` (interface)
- Modify: `internal/app/uisink.go` (multiSink)
- Modify: `internal/tui/runner.go` (no-op impl on the TUI runner)
- Modify: `internal/adapter/web/server.go` (already added in Task 7)

**Context:** `uistate.Sink` (`uistate.go:88`) is the fan-out interface;
`multiSink` (`uisink.go`) forwards to each member; the TUI `*Runner` and the web
`*Server` both implement it. `SendProposals` takes
`[]positionproposal.Proposal`. The TUI does not render proposals (web-only
surface), so its implementation is a no-op.

> **Import-cycle check:** `internal/uistate` must not import a package that
> imports it. `internal/positionproposal` imports only `internal/domain` (+
> stdlib, uuid, decimal), so `uistate` importing `positionproposal` is safe.

- [ ] **Step 1: Add to the Sink interface**

In `internal/uistate/uistate.go`, add the import
`"github.com/azhar/cerebro/internal/positionproposal"` and add to the `Sink`
interface (after `SendPositions`):

```go
	// SendProposals pushes the current set of pending SL/TP adjustment
	// proposals. Web-only; the TUI implements it as a no-op.
	SendProposals(proposals []positionproposal.Proposal)
```

- [ ] **Step 2: Implement on multiSink**

In `internal/app/uisink.go`, add (mirroring `SendPositions`):

```go
func (m multiSink) SendProposals(p []positionproposal.Proposal) {
	for _, s := range m {
		s.SendProposals(p)
	}
}
```

Add the `positionproposal` import to `uisink.go`.

- [ ] **Step 3: Implement no-op on the TUI runner**

In `internal/tui/runner.go`, add near `SendPositions` (line 118):

```go
// SendProposals is a no-op on the TUI: SL/TP adjustment proposals are surfaced
// on the web dashboard only.
func (r *Runner) SendProposals([]positionproposal.Proposal) {}
```

Add the `positionproposal` import to `runner.go`.

- [ ] **Step 4: Build everything**

Run: `go build ./...`
Expected: success. (The web `*Server.SendProposals` from Task 7 satisfies the
new interface method; confirm no "does not implement uistate.Sink" errors.)

- [ ] **Step 5: Commit**

```bash
git add internal/uistate/uistate.go internal/app/uisink.go internal/tui/runner.go
git commit -m "feat(uistate): add SendProposals to the UI Sink fan-out"
```

---

## Task 9: Wire the proposal store in the composition root

**Files:**
- Modify: `internal/app/runtime.go` (build store, inject ApplyFunc, wire web + reconciler review path)

**Context:** Per-venue reconciler wiring is around `runtime.go:672-690`; the web
server is constructed at ~228 and configured at ~548. `positionsForVenue(gctx,
brokersByVenue[venue], venue)` gives the live positions for the existence guard.
`brokersByVenue[venue]` is a `port.Broker`; the futures/spot concrete types add
`ProtectiveBracket`. `bracketTracker` (an `*execution.BracketTracker`) satisfies
`positionproposal.Recorder`.

- [ ] **Step 1: Build the store before the per-venue reconciler loop**

Where the position-manager block begins (just before the
`for _, venue := range activeVenues` reconciler loop), add a single shared
store. The ApplyFunc is venue-aware via the proposal's `Venue` field:

```go
	// Proposal store for agent SL/TP adjustments confirmed from the web. No
	// timeout: proposals live until the operator confirms/rejects or the
	// position closes.
	proposalApply := func(ctx context.Context, p positionproposal.Proposal) error {
		broker := brokersByVenue[p.Venue]
		look, ok := broker.(positionproposal.ProtectiveLookup)
		if !ok {
			return fmt.Errorf("broker for %s lacks ProtectiveLookup", p.Venue)
		}
		bracketer, ok := broker.(positionproposal.Bracketer)
		if !ok {
			return fmt.Errorf("broker for %s lacks Bracketer", p.Venue)
		}
		return positionproposal.ApplyAdjustment(bracketer, look, bracketTracker)(ctx, p)
	}
	var proposalStore *positionproposal.Store
	proposalStore = positionproposal.NewStore(proposalApply, func() {
		uiSink.SendProposals(proposalStore.Pending())
	})
	// Existence guard: a proposal is dropped if its position closes. Checks
	// every active venue's live positions.
	proposalStore.SetPositionExists(func(sym domain.Symbol) bool {
		for _, venue := range activeVenues {
			for _, p := range positionsForVenue(gctx, brokersByVenue[venue], venue) {
				if p.Symbol == sym {
					return true
				}
			}
		}
		return false
	})
	if webServer != nil {
		webServer.SetProposalController(proposalStore)
	}
```

> If `activeVenues` is not in scope at this point, use the same venue slice the
> reconciler loop ranges over. `fmt` is already imported in runtime.go.

- [ ] **Step 2: Add a Prune ticker**

Alongside the existing `queue.Tick` ticker goroutine (~`runtime.go:660`), add a
prune loop so closed-position proposals are cleared even without a confirm:

```go
	g.Go(func() error {
		t := time.NewTicker(5 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-gctx.Done():
				return nil
			case <-t.C:
				proposalStore.Prune()
			}
		}
	})
```

- [ ] **Step 3: Route agent adjustments for protected positions to the store**

This connects Job B to the store. In `internal/execution/reconciler.go`,
`reviewPositions` currently enqueues every non-HOLD action on the `ActionQueue`
(`reconciler.go:212-219`). Add an optional proposal sink to `ReconcilerDeps` and,
when set, route `ActionTightenStop` decisions on `ExternallyProtected` positions
to it instead of the queue.

Add to `ReconcilerDeps` (in `reconciler.go`):

```go
	// Propose, when set, receives SL/TP adjustments for externally-protected
	// positions that require operator confirmation, instead of the autonomous
	// ActionQueue. May be nil.
	Propose func(pos domain.Position, action domain.ManagedAction)
```

In `reviewPositions`, replace the enqueue block (after the `ActionHold` check)
with:

```go
		if r.deps.Propose != nil && pos.ExternallyProtected && action.Decision == domain.ActionTightenStop {
			r.deps.Propose(pos, action)
			slog.Info("reconciler: routed adjustment to proposal store (operator confirm)",
				"symbol", trig.Symbol, "new_stop", action.NewStopLoss)
			continue
		}
		id := r.deps.Queue.Enqueue(pos, trig, action)
		slog.Info("reconciler: queued managed action",
			"id", id, "symbol", trig.Symbol, "trigger", trig.Type, "action", action.Decision)
```

- [ ] **Step 4: Pass the Propose closure when building each reconciler**

In `runtime.go`, in the `execution.ReconcilerDeps{...}` literal (~line 678), add:

```go
				Propose: func(pos domain.Position, action domain.ManagedAction) {
					proposalStore.Propose(positionproposal.Proposal{
						Symbol:       pos.Symbol,
						Venue:        pos.Venue,
						Side:         pos.Side,
						CurrentStop:  pos.StopLoss,
						CurrentTP:    pos.TakeProfit1,
						ProposedStop: action.NewStopLoss,
						ProposedTP:   pos.TakeProfit1,
						Reasoning:    action.Reason,
					})
				},
```

Add the `positionproposal` import to `runtime.go`.

- [ ] **Step 5: Build + full test suite with race**

Run: `go build ./... && go test -race ./...`
Expected: success; all tests pass.

- [ ] **Step 6: Lint**

Run: `make lint`
Expected: zero warnings.

- [ ] **Step 7: Commit**

```bash
git add internal/app/runtime.go internal/execution/reconciler.go
git commit -m "feat(app): wire no-timeout proposal store into reconciler + web"
```

---

## Task 10: Frontend — Proposal type, store slice, confirm/reject UI

**Files:**
- Modify: `web/lib/types.ts` (add `Proposal`, extend `Snapshot`)
- Modify: `web/lib/store.ts` (add `proposals` slice + WS `proposals` case + snapshot merge)
- Modify: `web/lib/api.ts` (add `confirmProposal` / `rejectProposal`)
- Modify: `web/components/panels.tsx` (render proposal block in `Positions`)

**Context:** `types.ts` defines `Position` (line 17). `store.ts` is a Zustand
store: state at `interface State` (line 19), initialised in `create<State>`
(line 38), WS envelopes handled in a `switch (type)` (lines 88-119) with
`case "positions": return { positions: data as Position[] };`, and a snapshot
merge block (~line 145). `api.ts` has `postCommand` (line 52) showing the POST +
`authHeaders()` pattern. Match the existing 2-space indentation and style.

- [ ] **Step 1: Add the Proposal type + extend Snapshot (types.ts)**

After the `Position` interface, add:

```ts
export interface Proposal {
  id: string;
  symbol: string;
  venue: string;
  side: string;
  currentStop: string;
  currentTp: string;
  proposedStop: string;
  proposedTp: string;
  reasoning: string;
  createdAt: number;
}
```

Add `proposals: Proposal[];` to the `Snapshot` interface (find
`export interface Snapshot`).

- [ ] **Step 2: Add api calls (api.ts)**

Append, mirroring `postCommand`:

```ts
export async function confirmProposal(id: string): Promise<void> {
  const res = await fetch(`/api/proposals/${encodeURIComponent(id)}/confirm`, {
    method: "POST",
    headers: authHeaders(),
  });
  if (!res.ok) throw new Error(`confirm failed: ${res.status}`);
}

export async function rejectProposal(id: string): Promise<void> {
  const res = await fetch(`/api/proposals/${encodeURIComponent(id)}/reject`, {
    method: "POST",
    headers: authHeaders(),
  });
  if (!res.ok) throw new Error(`reject failed: ${res.status}`);
}
```

- [ ] **Step 3: Add the store slice + WS handling (store.ts)**

Add `Proposal` to the type import. Add `proposals: Proposal[];` to
`interface State` (after `positions`). Add `proposals: [],` to the
`create<State>` initial state (after `positions: []`). Add a WS case in the
envelope switch (alongside `case "positions"`):

```ts
        case "proposals":
          return { proposals: data as Proposal[] };
```

In the snapshot-merge block (where `positions: s.positions ?? []` appears),
add:

```ts
        proposals: s.proposals ?? [],
```

- [ ] **Step 4: Render the proposal block in the Positions panel (panels.tsx)**

At the top of the `Positions` component, read proposals and index by symbol:

```tsx
  const proposals = useStore((s) => s.proposals);
  const proposalBySymbol = new Map(proposals.map((p) => [p.symbol, p]));
```

Inside the position card, after the `SL … · TP …` line, add the proposal block:

```tsx
            {proposalBySymbol.get(p.symbol) && (
              <ProposalBlock proposal={proposalBySymbol.get(p.symbol)!} />
            )}
```

Add the component (same file, below `Positions`). It uses `confirmProposal` /
`rejectProposal` and disables the buttons while a request is in flight:

```tsx
function ProposalBlock({ proposal }: { proposal: Proposal }) {
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const act = async (fn: (id: string) => Promise<void>) => {
    setBusy(true);
    setErr(null);
    try {
      await fn(proposal.id);
    } catch (e) {
      setErr(e instanceof Error ? e.message : "failed");
    } finally {
      setBusy(false);
    }
  };
  return (
    <div className="mt-1 rounded border border-agent/50 bg-agent/10 p-1.5 text-2xs">
      <div className="font-bold text-agent">Agent proposes adjustment</div>
      <div className="text-fg">
        SL {fmtPrice(proposal.currentStop)} → {fmtPrice(proposal.proposedStop)} · TP{" "}
        {fmtPrice(proposal.currentTp)} → {fmtPrice(proposal.proposedTp)}
      </div>
      {proposal.reasoning && <div className="text-fg-dim">{proposal.reasoning}</div>}
      <div className="mt-1 flex gap-2">
        <button
          disabled={busy}
          onClick={() => act(confirmProposal)}
          className="rounded bg-bull/80 px-2 py-0.5 font-bold text-bg disabled:opacity-50"
        >
          Confirm
        </button>
        <button
          disabled={busy}
          onClick={() => act(rejectProposal)}
          className="rounded bg-bear/80 px-2 py-0.5 font-bold text-bg disabled:opacity-50"
        >
          Reject
        </button>
      </div>
      {err && <div className="text-bear">{err}</div>}
    </div>
  );
}
```

Add imports at the top of `panels.tsx`:

```tsx
import { useState } from "react";
import { confirmProposal, rejectProposal } from "@/lib/api";
import type { Proposal } from "@/lib/types";
```

> `agent`, `bull`, `bear`, `bg`, `fg`, `fg-dim` are existing Tailwind theme
> colors used elsewhere in this file — reuse them; do not invent new tokens.

- [ ] **Step 5: Build the frontend**

Run: `cd web && npm run build`
Expected: build succeeds (Next.js export). Resolve any TS errors before commit.

- [ ] **Step 6: Commit**

```bash
git add web/lib/types.ts web/lib/store.ts web/lib/api.ts web/components/panels.tsx
git commit -m "feat(web-ui): render agent SL/TP proposals with confirm/reject"
```

---

## Task 11: Full verification + integration sanity

**Files:** none (verification only)

- [ ] **Step 1: Full build + unit tests with race**

Run: `go build ./... && go test -race ./...`
Expected: all pass.

- [ ] **Step 2: Lint**

Run: `make lint`
Expected: zero warnings.

- [ ] **Step 3: Frontend build embeds into the binary**

Run: `make web && make build`
Expected: both succeed; `internal/adapter/web/dist/index.html` exists so
`frontendFS()` serves the SPA.

- [ ] **Step 4: Manual smoke (testbed/demo, optional but recommended)**

Open a futures position with an SL/TP **on the Binance website**. Run Cerebro
against the same account in demo. Confirm in the log that the reconciler does
**not** emit `position has no SL/TP levels to attach; flattening` for that
symbol, and the web Active Positions panel shows the SL/TP line. This is the
exact regression that motivated the feature.

- [ ] **Step 5: Final commit (if any verification fixups were needed)**

```bash
git add -A
git commit -m "chore: verification fixups for exchange-side SL/TP detection"
```

---

## Self-Review Notes (for the implementer)

- **Spot quantity:** if spot `PlaceBracket` rejects a zero `Quantity` in
  `ApplyAdjustment` (Task 6), populate `req.Quantity` from the live position in
  the runtime ApplyFunc closure (Task 9 has `positionsForVenue`).
- **Symbol normalisation** (Tasks 3-4) is the riskiest detail: the raw exchange
  symbol (`BTCUSDT`) must map to the exact `domain.Symbol` key used in the
  position map (`BTC/USDT-PERP` futures, `BTC/USDT` spot). Verify against the
  existing conversion the adapter already uses before relying on it.
- **No-timeout invariant:** the proposal store has no ticker that expires items
  by age. The only automatic removal is `Prune` on position-gone. Do not add a
  TTL.

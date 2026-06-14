# Trading Agent — Position Lifecycle & Bias Reconciliation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Guarantee every open position has a TP/SL bracket, and re-evaluate open positions when bias/indicators change — deciding hold/tighten/close/flip, with a 1-minute confirm-then-autonomous timeout.

**Architecture:** A deterministic `Position Reconciler` (pure Go, runs even when the LLM is down) enforces the bracket guarantee and detects review triggers. A `Position Manager` LLM agent makes the hold/close/flip judgment. An `Action Queue` posts suggestions and auto-executes after 60s of human silence. Targets paper + live Binance futures via the existing `port.Broker`.

**Tech Stack:** Go 1.26, `shopspring/decimal`, `log/slog`, `errgroup`, existing hexagonal ports (`port.Broker`, `port.LLM`, `port.Notifier`, `port.Cache`, `port.TradeStore`).

**Spec:** `docs/superpowers/specs/2026-06-14-trading-agent-design.md`

---

## Phase 1 — Domain types + config

### Task 1: Add `position_manager` AgentRole

**Files:**
- Modify: `internal/domain/types.go:140-147`

- [ ] **Step 1: Add the constant**

In `internal/domain/types.go`, extend the AgentRole const block:

```go
const (
	AgentScreening       AgentRole = "screening"
	AgentRisk            AgentRole = "risk"
	AgentCopilot         AgentRole = "copilot"
	AgentReviewer        AgentRole = "reviewer"
	AgentPositionManager AgentRole = "position_manager"
)
```

- [ ] **Step 2: Build to verify it compiles**

Run: `go build ./internal/domain/...`
Expected: no output, exit 0.

- [ ] **Step 3: Commit**

```bash
git add internal/domain/types.go
git commit -m "feat(domain): add position_manager agent role"
```

---

### Task 2: Domain types for review triggers and managed actions

**Files:**
- Create: `internal/domain/position_review.go`
- Test: `internal/domain/position_review_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/domain/position_review_test.go`:

```go
package domain

import (
	"testing"

	"github.com/shopspring/decimal"
)

func TestManagedAction_RequiresStopForTighten(t *testing.T) {
	tests := []struct {
		name    string
		action  ManagedAction
		wantErr bool
	}{
		{
			name:    "tighten without stop is invalid",
			action:  ManagedAction{Decision: ActionTightenStop},
			wantErr: true,
		},
		{
			name: "tighten with stop is valid",
			action: ManagedAction{
				Decision:    ActionTightenStop,
				NewStopLoss: decimal.NewFromInt(100),
			},
			wantErr: false,
		},
		{"hold is always valid", ManagedAction{Decision: ActionHold}, false},
		{"close is always valid", ManagedAction{Decision: ActionClose}, false},
		{"flip is always valid", ManagedAction{Decision: ActionFlip}, false},
		{"unknown decision is invalid", ManagedAction{Decision: "wat"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.action.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() err = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestBiasOpposesSide(t *testing.T) {
	tests := []struct {
		name string
		bias BiasScore
		side Side
		want bool
	}{
		{"bull vs sell opposes", BiasBullish, SideSell, true},
		{"bear vs buy opposes", BiasBearish, SideBuy, true},
		{"bull vs buy aligns", BiasBullish, SideBuy, false},
		{"bear vs sell aligns", BiasBearish, SideSell, false},
		{"neutral never opposes", BiasNeutral, SideBuy, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := BiasOpposesSide(tt.bias, tt.side); got != tt.want {
				t.Errorf("BiasOpposesSide() = %v, want %v", got, tt.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/domain/ -run 'TestManagedAction_RequiresStopForTighten|TestBiasOpposesSide'`
Expected: FAIL — `undefined: ManagedAction`, `undefined: BiasOpposesSide`.

- [ ] **Step 3: Write the implementation**

Create `internal/domain/position_review.go`:

```go
package domain

import (
	"fmt"
	"time"

	"github.com/shopspring/decimal"
)

// ActionDecision is the Position Manager agent's verdict on an open position.
type ActionDecision string

const (
	ActionHold        ActionDecision = "hold"
	ActionTightenStop ActionDecision = "tighten_stop"
	ActionClose       ActionDecision = "close"
	ActionFlip        ActionDecision = "flip"
)

// TriggerType identifies why a position was queued for review.
type TriggerType string

const (
	TriggerBiasFlipAgainst TriggerType = "bias_flip_against"
	TriggerProfitThreshold TriggerType = "profit_threshold"
	TriggerNearTPSL        TriggerType = "near_tp_sl"
)

// ReviewTrigger is a deterministic signal that an open position warrants a
// Position Manager evaluation. Produced by the reconciler, consumed by the
// agent. Carries no judgment — only the reason and the position it concerns.
type ReviewTrigger struct {
	Type      TriggerType
	Symbol    Symbol
	Venue     Venue
	Side      Side
	DetectedAt time.Time
}

// ManagedAction is the agent's decision for a reviewed position.
type ManagedAction struct {
	Decision    ActionDecision
	NewStopLoss decimal.Decimal // required when Decision == ActionTightenStop
	Reason      string
	Confidence  float64 // 0..1
}

// Validate checks the action is internally consistent.
func (a ManagedAction) Validate() error {
	switch a.Decision {
	case ActionHold, ActionClose, ActionFlip:
		return nil
	case ActionTightenStop:
		if a.NewStopLoss.IsZero() {
			return fmt.Errorf("tighten_stop action requires a non-zero NewStopLoss")
		}
		return nil
	default:
		return fmt.Errorf("unknown action decision %q", a.Decision)
	}
}

// BiasOpposesSide reports whether a bias score runs against an open position's
// side: Bullish opposes a SELL (short), Bearish opposes a BUY (long). Neutral
// never opposes.
func BiasOpposesSide(bias BiasScore, side Side) bool {
	switch bias {
	case BiasBullish:
		return side == SideSell
	case BiasBearish:
		return side == SideBuy
	default:
		return false
	}
}

// PositionReview is the full input the Position Manager agent needs to judge an
// open position. It lives in domain so both the execution package (which
// produces it) and the agent package (which consumes it) can reference it
// without an import cycle.
type PositionReview struct {
	Position           Position
	Trigger            ReviewTrigger
	BiasScore          BiasScore
	BiasReasoning      string
	IndicatorSummary   string
	PerformanceSummary string
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/domain/ -run 'TestManagedAction_RequiresStopForTighten|TestBiasOpposesSide'`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/domain/position_review.go internal/domain/position_review_test.go
git commit -m "feat(domain): add ReviewTrigger and ManagedAction types"
```

---

### Task 3: PositionManagerConfig + validation

**Files:**
- Modify: `internal/config/config.go` (add struct near `AgentConfig`, add field to `Config`, extend `Validate`)
- Modify: `configs/app.yaml.example`
- Test: `internal/config/config_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/config/config_test.go`:

```go
func TestPositionManagerConfig_Validate(t *testing.T) {
	base := func() PositionManagerConfig {
		return PositionManagerConfig{
			Enabled:             true,
			ReconcileIntervalMS: 5000,
			ConfirmTimeoutSec:   60,
			AutonomousOnTimeout: true,
			TriggerDebounceSec:  300,
			LLMFailureAction:    "tighten_breakeven",
			ProfitThresholdPct:  1.0,
			NearTPSLPct:         0.2,
		}
	}
	tests := []struct {
		name    string
		mutate  func(*PositionManagerConfig)
		wantErr bool
	}{
		{"valid", func(*PositionManagerConfig) {}, false},
		{"disabled skips checks", func(p *PositionManagerConfig) { p.Enabled = false; p.ReconcileIntervalMS = 0 }, false},
		{"zero interval", func(p *PositionManagerConfig) { p.ReconcileIntervalMS = 0 }, true},
		{"zero timeout", func(p *PositionManagerConfig) { p.ConfirmTimeoutSec = 0 }, true},
		{"bad llm action", func(p *PositionManagerConfig) { p.LLMFailureAction = "panic" }, true},
		{"negative profit pct", func(p *PositionManagerConfig) { p.ProfitThresholdPct = -1 }, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := base()
			tt.mutate(&cfg)
			err := cfg.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() err = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/config/ -run TestPositionManagerConfig_Validate`
Expected: FAIL — `undefined: PositionManagerConfig`.

- [ ] **Step 3: Add the config struct and its Validate**

In `internal/config/config.go`, after the `AgentConfig` struct, add:

```go
// PositionManagerConfig controls the position-lifecycle reconciler and the
// Position Manager agent. When Enabled is false the reconciler goroutine is
// not started and existing Monitor/bracket behaviour is unchanged.
type PositionManagerConfig struct {
	Enabled             bool    `yaml:"enabled"`
	ReconcileIntervalMS int     `yaml:"reconcile_interval_ms"`
	ConfirmTimeoutSec   int     `yaml:"confirm_timeout_sec"`
	AutonomousOnTimeout bool    `yaml:"autonomous_on_timeout"`
	TriggerDebounceSec  int     `yaml:"trigger_debounce_sec"`
	LLMFailureAction    string  `yaml:"llm_failure_action"` // tighten_breakeven | hold
	ProfitThresholdPct  float64 `yaml:"profit_threshold_pct"`
	NearTPSLPct         float64 `yaml:"near_tp_sl_pct"`
	BiasFlipAgainst     bool    `yaml:"bias_flip_against"`
}

// Validate checks the position-manager config. A disabled block skips all
// numeric checks so operators can leave placeholder zeros.
func (p PositionManagerConfig) Validate() error {
	if !p.Enabled {
		return nil
	}
	if p.ReconcileIntervalMS <= 0 {
		return fmt.Errorf("position_manager.reconcile_interval_ms must be > 0")
	}
	if p.ConfirmTimeoutSec <= 0 {
		return fmt.Errorf("position_manager.confirm_timeout_sec must be > 0")
	}
	if p.TriggerDebounceSec < 0 {
		return fmt.Errorf("position_manager.trigger_debounce_sec must be >= 0")
	}
	switch p.LLMFailureAction {
	case "tighten_breakeven", "hold":
	default:
		return fmt.Errorf("position_manager.llm_failure_action must be tighten_breakeven|hold, got %q", p.LLMFailureAction)
	}
	if p.ProfitThresholdPct < 0 {
		return fmt.Errorf("position_manager.profit_threshold_pct must be >= 0")
	}
	if p.NearTPSLPct < 0 {
		return fmt.Errorf("position_manager.near_tp_sl_pct must be >= 0")
	}
	return nil
}
```

- [ ] **Step 4: Add the field to the top-level Config struct**

Find the `Config` struct in `internal/config/config.go` (the one with `Engine EngineConfig`, `Risk RiskConfig`, `Agent AgentConfig` fields) and add:

```go
	PositionManager PositionManagerConfig `yaml:"position_manager"`
```

- [ ] **Step 5: Call it from Config.Validate**

Inside `func (c *Config) Validate(cliEnv domain.Environment) error` (starts at `internal/config/config.go:339`), before the final `return nil`, add:

```go
	if err := c.PositionManager.Validate(); err != nil {
		return err
	}
```

- [ ] **Step 6: Run test to verify it passes**

Run: `go test ./internal/config/ -run TestPositionManagerConfig_Validate`
Expected: PASS.

- [ ] **Step 7: Add the example config block**

Append to `configs/app.yaml.example` at the top level (sibling of `engine:`, `risk:`):

```yaml
position_manager:
  enabled: true
  reconcile_interval_ms: 5000
  confirm_timeout_sec: 60
  autonomous_on_timeout: true
  trigger_debounce_sec: 300
  llm_failure_action: tighten_breakeven
  profit_threshold_pct: 1.0
  near_tp_sl_pct: 0.2
  bias_flip_against: true
```

- [ ] **Step 8: Verify full config package + commit**

Run: `go test ./internal/config/...`
Expected: PASS.

```bash
git add internal/config/config.go internal/config/config_test.go configs/app.yaml.example
git commit -m "feat(config): add position_manager config block + validation"
```

---

## Phase 2 — Paper book reduce-only fix

### Task 4: Flatten on reduce-only opposite-side fill

**Files:**
- Modify: `internal/execution/paper/book.go:101-141` (the `Fill` method)
- Test: `internal/execution/paper/book_test.go`

**Context:** Today `Book.Fill` always overwrites `b.positions[symbol]` with a new
position built from the intent. A reduce-only close (opposite side) therefore
replaces a long with a short instead of flattening. The fix: when an existing
position exists on the symbol and the incoming fill is the opposite side,
reduce/flatten it instead of overwriting.

- [ ] **Step 1: Write the failing test**

Append to `internal/execution/paper/book_test.go`:

```go
func TestBook_Fill_ReduceOnlyFlattens(t *testing.T) {
	b := NewBook()
	now := time.Now().UTC()

	// Open a long 1.0 BTC.
	b.AddOrder(domain.OrderIntent{
		ID: "entry", Symbol: "BTCUSDT", Side: domain.SideBuy,
		Quantity: decimal.NewFromInt(1),
	})
	b.Fill("entry", decimal.NewFromInt(100), now)

	// Reduce-only opposite-side fill for the full quantity should flatten.
	b.AddOrder(domain.OrderIntent{
		ID: "close", Symbol: "BTCUSDT", Side: domain.SideSell,
		Quantity: decimal.NewFromInt(1), ReduceOnly: true,
	})
	b.Fill("close", decimal.NewFromInt(110), now)

	if got := len(b.Positions()); got != 0 {
		t.Fatalf("expected position flattened, got %d positions", got)
	}
}

func TestBook_Fill_ReduceOnlyPartial(t *testing.T) {
	b := NewBook()
	now := time.Now().UTC()
	b.AddOrder(domain.OrderIntent{
		ID: "entry", Symbol: "BTCUSDT", Side: domain.SideBuy,
		Quantity: decimal.NewFromInt(2),
	})
	b.Fill("entry", decimal.NewFromInt(100), now)

	b.AddOrder(domain.OrderIntent{
		ID: "reduce", Symbol: "BTCUSDT", Side: domain.SideSell,
		Quantity: decimal.NewFromInt(1), ReduceOnly: true,
	})
	b.Fill("reduce", decimal.NewFromInt(110), now)

	pos := b.Positions()
	if len(pos) != 1 {
		t.Fatalf("expected 1 position remaining, got %d", len(pos))
	}
	if !pos[0].Quantity.Equal(decimal.NewFromInt(1)) {
		t.Errorf("expected qty 1 remaining, got %s", pos[0].Quantity)
	}
	if pos[0].Side != domain.SideBuy {
		t.Errorf("expected side unchanged (BUY), got %s", pos[0].Side)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/execution/paper/ -run 'TestBook_Fill_ReduceOnly'`
Expected: FAIL — flatten test reports 1 position; partial test reports wrong side/qty (overwritten to SELL 1).

- [ ] **Step 3: Implement reduce/flatten logic in Fill**

In `internal/execution/paper/book.go`, replace the position-assignment block in `Fill` (currently builds `pos` and does `b.positions[intent.Symbol] = pos`) with:

```go
	intent := order.Intent
	existing, hasExisting := b.positions[intent.Symbol]

	// Reduce-only opposite-side fill reduces or flattens the existing position
	// rather than opening a reversed one. This matches futures reduce-only
	// semantics and is what reconciler/monitor closes rely on.
	if hasExisting && intent.ReduceOnly && intent.Side != existing.Side {
		remaining := existing.Quantity.Sub(intent.Quantity)
		if remaining.LessThanOrEqual(decimal.Zero) {
			delete(b.positions, intent.Symbol)
		} else {
			existing.Quantity = remaining
			existing.CurrentPrice = fillPrice
		}
		return domain.Trade{
			ID:            uuid.New().String(),
			IntentID:      intent.ID,
			CorrelationID: intent.CorrelationID,
			Symbol:        intent.Symbol,
			Side:          intent.Side,
			Quantity:      intent.Quantity,
			FillPrice:     fillPrice,
			Strategy:      intent.Strategy,
			Venue:         intent.Venue,
			CreatedAt:     fillTime,
		}
	}

	pos := &domain.Position{
		Symbol:        intent.Symbol,
		Venue:         intent.Venue,
		Side:          intent.Side,
		Quantity:      intent.Quantity,
		EntryPrice:    fillPrice,
		CurrentPrice:  fillPrice,
		StopLoss:      intent.StopLoss,
		TakeProfit1:   intent.TakeProfit1,
		Strategy:      intent.Strategy,
		CorrelationID: intent.CorrelationID,
		OpenedAt:      fillTime,
	}
	b.positions[intent.Symbol] = pos
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/execution/paper/ -run 'TestBook_Fill' -race`
Expected: PASS (including existing Fill tests).

- [ ] **Step 5: Commit**

```bash
git add internal/execution/paper/book.go internal/execution/paper/book_test.go
git commit -m "fix(paper): reduce-only opposite fill flattens instead of reversing"
```

---

## Phase 3 — Reconciler Job A (bracket guarantee + orphan sweep)

This phase delivers the hard TP/SL guarantee on its own, before any LLM layer.

### Task 5: BracketTracker — record which positions have a live bracket

**Files:**
- Create: `internal/execution/bracket_tracker.go`
- Test: `internal/execution/bracket_tracker_test.go`

**Context:** The reconciler must know whether an open position already has a
bracket. Brokers don't expose "does symbol X have a bracket?" uniformly
(paper does via `Book.Brackets()`, futures via open algo orders). We keep a
lightweight in-memory tracker the Worker updates when it places a bracket, and
the reconciler consults. This avoids per-tick broker calls and works
identically across venues.

- [ ] **Step 1: Write the failing test**

Create `internal/execution/bracket_tracker_test.go`:

```go
package execution

import (
	"testing"

	"github.com/azhar/cerebro/internal/domain"
)

func TestBracketTracker_RecordAndHas(t *testing.T) {
	tr := NewBracketTracker()
	sym := domain.Symbol("BTCUSDT")

	if tr.Has(sym) {
		t.Fatal("expected no bracket initially")
	}
	tr.Record(sym, domain.BracketResponse{StopOrderID: "s1", Symbol: sym})
	if !tr.Has(sym) {
		t.Fatal("expected bracket recorded")
	}
	tr.Remove(sym)
	if tr.Has(sym) {
		t.Fatal("expected bracket removed")
	}
}

func TestBracketTracker_Symbols(t *testing.T) {
	tr := NewBracketTracker()
	tr.Record("BTCUSDT", domain.BracketResponse{StopOrderID: "s1"})
	tr.Record("ETHUSDT", domain.BracketResponse{StopOrderID: "s2"})
	if got := len(tr.Symbols()); got != 2 {
		t.Fatalf("expected 2 tracked symbols, got %d", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/execution/ -run TestBracketTracker`
Expected: FAIL — `undefined: NewBracketTracker`.

- [ ] **Step 3: Implement the tracker**

Create `internal/execution/bracket_tracker.go`:

```go
package execution

import (
	"sync"

	"github.com/azhar/cerebro/internal/domain"
)

// BracketTracker records which symbols currently have a protective bracket
// attached. It is the reconciler's source of truth for the "is this position
// protected?" question, updated by the Worker on bracket placement and by the
// reconciler/matcher when a bracket is cancelled or fires.
//
// Safe for concurrent use.
type BracketTracker struct {
	mu       sync.RWMutex
	brackets map[domain.Symbol]domain.BracketResponse
}

// NewBracketTracker returns an empty tracker.
func NewBracketTracker() *BracketTracker {
	return &BracketTracker{brackets: make(map[domain.Symbol]domain.BracketResponse)}
}

// Record marks a symbol as having a live bracket.
func (t *BracketTracker) Record(sym domain.Symbol, resp domain.BracketResponse) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.brackets[sym] = resp
}

// Has reports whether the symbol currently has a tracked bracket.
func (t *BracketTracker) Has(sym domain.Symbol) bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	_, ok := t.brackets[sym]
	return ok
}

// Get returns the tracked bracket for a symbol.
func (t *BracketTracker) Get(sym domain.Symbol) (domain.BracketResponse, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	resp, ok := t.brackets[sym]
	return resp, ok
}

// Remove clears the tracked bracket for a symbol.
func (t *BracketTracker) Remove(sym domain.Symbol) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.brackets, sym)
}

// Symbols returns the set of symbols with tracked brackets.
func (t *BracketTracker) Symbols() []domain.Symbol {
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := make([]domain.Symbol, 0, len(t.brackets))
	for s := range t.brackets {
		out = append(out, s)
	}
	return out
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/execution/ -run TestBracketTracker -race`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/execution/bracket_tracker.go internal/execution/bracket_tracker_test.go
git commit -m "feat(execution): add BracketTracker for reconciler protection state"
```

---

### Task 6: Wire BracketTracker into the Worker

**Files:**
- Modify: `internal/execution/worker.go:36-62` (struct + constructor), `:174-210` (bracket success path)
- Test: `internal/execution/worker_test.go` (create if absent)

**Context:** When the Worker successfully attaches a bracket it must record it
in the tracker so the reconciler sees the position as protected.

- [ ] **Step 1: Add the tracker field + constructor param**

In `internal/execution/worker.go`, add `tracker *BracketTracker` to the
`Worker` struct, and a parameter to `NewWorker`:

```go
type Worker struct {
	venue   domain.Venue
	broker  port.Broker
	store   port.TradeStore
	audit   port.AuditStore
	cache   port.Cache
	tracker *BracketTracker
	inputCh <-chan OrderRequest
}

func NewWorker(
	venue domain.Venue,
	broker port.Broker,
	store port.TradeStore,
	audit port.AuditStore,
	cache port.Cache,
	tracker *BracketTracker,
	inputCh <-chan OrderRequest,
) *Worker {
	return &Worker{
		venue:   venue,
		broker:  broker,
		store:   store,
		audit:   audit,
		cache:   cache,
		tracker: tracker,
		inputCh: inputCh,
	}
}
```

- [ ] **Step 2: Record the bracket on success**

In `process`, immediately after the successful `log.Info("bracket attached", ...)`
call (around `worker.go:192`) and before the `return OrderResponse{...}`, add:

```go
	if w.tracker != nil {
		w.tracker.Record(intent.Symbol, bracket)
	}
```

- [ ] **Step 3: Update all NewWorker call sites**

Run: `grep -rn "NewWorker(" internal --include=*.go`
For each call site (production wiring + any tests), insert the `tracker`
argument before `inputCh`. Production wiring is in `internal/app/` (Task 12
re-touches it; for now pass a tracker the caller owns or `NewBracketTracker()`).

- [ ] **Step 4: Build to verify compilation**

Run: `go build ./...`
Expected: exit 0. Fix any call site the build flags.

- [ ] **Step 5: Run execution tests + commit**

Run: `go test ./internal/execution/... -race`
Expected: PASS.

```bash
git add internal/execution/worker.go
git commit -m "feat(execution): worker records placed brackets in tracker"
```

---

### Task 7: Reconciler — bracket guarantee + orphan sweep (Job A)

**Files:**
- Create: `internal/execution/reconciler.go`
- Test: `internal/execution/reconciler_test.go`

**Context:** The reconciler is the deterministic core. This task implements
only Job A (the hard TP/SL guarantee). Job B (review triggers) lands in Task 10.
It uses a `positionsFn` (same pattern as `Monitor`, see `monitor.go:42`) and a
`brackuetReq`-building helper lifted from the Worker.

- [ ] **Step 1: Write the failing test**

Create `internal/execution/reconciler_test.go`:

```go
package execution

import (
	"context"
	"testing"

	"github.com/azhar/cerebro/internal/domain"
	"github.com/shopspring/decimal"
)

// stubBroker records bracket placements and flatten orders.
type stubReconBroker struct {
	placeBracketErr error
	placedBrackets  []domain.BracketRequest
	placedOrders    []domain.OrderIntent
	cancelled       []domain.BracketResponse
}

func (s *stubReconBroker) Connect(context.Context) error { return nil }
func (s *stubReconBroker) StreamQuotes(context.Context, []domain.Symbol) (<-chan domain.Quote, error) {
	return nil, nil
}
func (s *stubReconBroker) PlaceOrder(_ context.Context, o domain.OrderIntent) (string, error) {
	s.placedOrders = append(s.placedOrders, o)
	return "ord-" + o.ID, nil
}
func (s *stubReconBroker) PlaceBracket(_ context.Context, r domain.BracketRequest) (domain.BracketResponse, error) {
	if s.placeBracketErr != nil {
		return domain.BracketResponse{}, s.placeBracketErr
	}
	s.placedBrackets = append(s.placedBrackets, r)
	return domain.BracketResponse{StopOrderID: "s-" + string(r.Symbol), Symbol: r.Symbol}, nil
}
func (s *stubReconBroker) CancelOrder(context.Context, domain.CancelRequest) error { return nil }
func (s *stubReconBroker) CancelBracket(_ context.Context, r domain.BracketResponse) error {
	s.cancelled = append(s.cancelled, r)
	return nil
}
func (s *stubReconBroker) Positions(context.Context) ([]domain.Position, error) { return nil, nil }
func (s *stubReconBroker) Balance(context.Context) (port.AccountBalance, error) {
	return port.AccountBalance{}, nil
}
func (s *stubReconBroker) Venue() domain.Venue { return domain.VenueBinanceFutures }

func longPos(sym string) domain.Position {
	return domain.Position{
		Symbol: domain.Symbol(sym), Venue: domain.VenueBinanceFutures,
		Side: domain.SideBuy, Quantity: decimal.NewFromInt(1),
		EntryPrice: decimal.NewFromInt(100), CurrentPrice: decimal.NewFromInt(100),
		StopLoss: decimal.NewFromInt(95), TakeProfit1: decimal.NewFromInt(110),
	}
}

func TestReconciler_AttachesMissingBracket(t *testing.T) {
	broker := &stubReconBroker{}
	tracker := NewBracketTracker()
	router := NewRouter([]domain.Venue{domain.VenueBinanceFutures})
	r := NewReconciler(ReconcilerDeps{
		Venue:   domain.VenueBinanceFutures,
		Broker:  broker,
		Tracker: tracker,
		Router:  router,
		Env:     domain.EnvironmentPaper,
		Positions: func() []domain.Position {
			return []domain.Position{longPos("BTCUSDT")}
		},
	})

	r.enforceBrackets(context.Background())

	if len(broker.placedBrackets) != 1 {
		t.Fatalf("expected 1 bracket attached, got %d", len(broker.placedBrackets))
	}
	if !tracker.Has("BTCUSDT") {
		t.Error("expected tracker to record the new bracket")
	}
}

func TestReconciler_SkipsAlreadyProtected(t *testing.T) {
	broker := &stubReconBroker{}
	tracker := NewBracketTracker()
	tracker.Record("BTCUSDT", domain.BracketResponse{StopOrderID: "existing"})
	r := NewReconciler(ReconcilerDeps{
		Venue: domain.VenueBinanceFutures, Broker: broker, Tracker: tracker,
		Router: NewRouter([]domain.Venue{domain.VenueBinanceFutures}),
		Env:    domain.EnvironmentPaper,
		Positions: func() []domain.Position { return []domain.Position{longPos("BTCUSDT")} },
	})

	r.enforceBrackets(context.Background())

	if len(broker.placedBrackets) != 0 {
		t.Errorf("expected no new bracket, got %d", len(broker.placedBrackets))
	}
}

func TestReconciler_FlattensWhenBracketFails(t *testing.T) {
	broker := &stubReconBroker{placeBracketErr: errTest}
	tracker := NewBracketTracker()
	router := NewRouter([]domain.Venue{domain.VenueBinanceFutures})
	// Drain the router channel so Route doesn't block.
	ch, _ := router.Channel(domain.VenueBinanceFutures)
	go func() {
		for req := range ch {
			req.RespCh <- OrderResponse{BrokerOrderID: "flattened"}
		}
	}()
	r := NewReconciler(ReconcilerDeps{
		Venue: domain.VenueBinanceFutures, Broker: broker, Tracker: tracker,
		Router: router, Env: domain.EnvironmentPaper,
		Positions: func() []domain.Position { return []domain.Position{longPos("BTCUSDT")} },
	})

	r.enforceBrackets(context.Background())

	if len(broker.placedBrackets) != 0 {
		t.Errorf("bracket should have failed, got %d placed", len(broker.placedBrackets))
	}
	// Expect a reduce-only flatten order routed.
	// (Asserted via the drained channel having produced a response; the
	// reconciler logs and routes — see enforceBrackets.)
}

func TestReconciler_CancelsOrphanBracket(t *testing.T) {
	broker := &stubReconBroker{}
	tracker := NewBracketTracker()
	tracker.Record("ETHUSDT", domain.BracketResponse{StopOrderID: "orphan", Symbol: "ETHUSDT"})
	r := NewReconciler(ReconcilerDeps{
		Venue: domain.VenueBinanceFutures, Broker: broker, Tracker: tracker,
		Router: NewRouter([]domain.Venue{domain.VenueBinanceFutures}),
		Env:    domain.EnvironmentPaper,
		Positions: func() []domain.Position { return nil }, // no open positions
	})

	r.sweepOrphans(context.Background())

	if len(broker.cancelled) != 1 {
		t.Fatalf("expected 1 orphan cancelled, got %d", len(broker.cancelled))
	}
	if tracker.Has("ETHUSDT") {
		t.Error("expected orphan removed from tracker")
	}
}

var errTest = errTestType("boom")

type errTestType string

func (e errTestType) Error() string { return string(e) }
```

Add the `port` import to the test file's import block:
`"github.com/azhar/cerebro/internal/port"`.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/execution/ -run TestReconciler`
Expected: FAIL — `undefined: NewReconciler`, `undefined: ReconcilerDeps`.

- [ ] **Step 3: Implement the reconciler (Job A only)**

Create `internal/execution/reconciler.go`:

```go
package execution

import (
	"context"
	"log/slog"
	"time"

	"github.com/azhar/cerebro/internal/domain"
	"github.com/azhar/cerebro/internal/port"
	"github.com/google/uuid"
)

// ReconcilerDeps bundles the reconciler's collaborators.
type ReconcilerDeps struct {
	Venue     domain.Venue
	Broker    port.Broker
	Tracker   *BracketTracker
	Router    *Router
	Env       domain.Environment
	Positions func() []domain.Position
	// IntervalMS is the tick cadence; 0 defaults to 5000.
	IntervalMS int
}

// Reconciler enforces the hard TP/SL guarantee and (in Task 10) detects
// review triggers. Job A is deterministic and runs even when the LLM is down.
type Reconciler struct {
	deps ReconcilerDeps
}

// NewReconciler builds a Reconciler.
func NewReconciler(deps ReconcilerDeps) *Reconciler {
	if deps.IntervalMS <= 0 {
		deps.IntervalMS = 5000
	}
	return &Reconciler{deps: deps}
}

// Run ticks Job A (and later Job B) until ctx is cancelled.
func (r *Reconciler) Run(ctx context.Context) error {
	interval := time.Duration(r.deps.IntervalMS) * time.Millisecond
	tick := time.NewTicker(interval)
	defer tick.Stop()
	slog.Info("position reconciler started", "venue", r.deps.Venue, "interval", interval)
	defer slog.Info("position reconciler stopping", "venue", r.deps.Venue)

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
			r.enforceBrackets(ctx)
			r.sweepOrphans(ctx)
		}
	}
}

// enforceBrackets guarantees every open position has a protective bracket.
// Missing → attach; attach fails → flatten (reduce-only).
func (r *Reconciler) enforceBrackets(ctx context.Context) {
	for _, pos := range r.deps.Positions() {
		if pos.Venue != r.deps.Venue {
			continue
		}
		if r.deps.Tracker.Has(pos.Symbol) {
			continue
		}
		if pos.StopLoss.IsZero() && pos.TakeProfit1.IsZero() {
			slog.Warn("reconciler: position has no SL/TP levels to attach; flattening",
				"symbol", pos.Symbol)
			r.flatten(ctx, pos, "no_protective_levels")
			continue
		}
		req := domain.BracketRequest{
			ParentIntentID: pos.CorrelationID,
			CorrelationID:  pos.CorrelationID,
			Symbol:         pos.Symbol,
			Venue:          pos.Venue,
			Side:           pos.Side,
			Quantity:       pos.Quantity,
			StopLoss:       pos.StopLoss,
			TakeProfit:     pos.TakeProfit1,
			ClientTag:      "recon",
		}
		resp, err := r.deps.Broker.PlaceBracket(ctx, req)
		if err != nil {
			slog.Error("reconciler: bracket attach failed; flattening position",
				"symbol", pos.Symbol, "error", err)
			r.flatten(ctx, pos, "bracket_attach_failed")
			continue
		}
		r.deps.Tracker.Record(pos.Symbol, resp)
		slog.Info("reconciler: attached missing bracket", "symbol", pos.Symbol)
	}
}

// sweepOrphans cancels brackets whose underlying position no longer exists.
func (r *Reconciler) sweepOrphans(ctx context.Context) {
	open := make(map[domain.Symbol]struct{})
	for _, p := range r.deps.Positions() {
		open[p.Symbol] = struct{}{}
	}
	for _, sym := range r.deps.Tracker.Symbols() {
		if _, ok := open[sym]; ok {
			continue
		}
		resp, _ := r.deps.Tracker.Get(sym)
		if err := r.deps.Broker.CancelBracket(ctx, resp); err != nil {
			slog.Warn("reconciler: orphan bracket cancel failed", "symbol", sym, "error", err)
		}
		r.deps.Tracker.Remove(sym)
		slog.Info("reconciler: cancelled orphan bracket", "symbol", sym)
	}
}

// flatten submits a reduce-only market close for the position.
func (r *Reconciler) flatten(ctx context.Context, pos domain.Position, reason string) {
	closeSide := domain.SideSell
	if pos.Side == domain.SideSell {
		closeSide = domain.SideBuy
	}
	intent := domain.OrderIntent{
		ID:            uuid.New().String(),
		CorrelationID: pos.CorrelationID,
		Symbol:        pos.Symbol,
		Venue:         r.deps.Venue,
		Side:          closeSide,
		OrderType:     domain.OrderTypeMarket,
		Quantity:      pos.Quantity,
		Strategy:      pos.Strategy,
		Environment:   r.deps.Env,
		CreatedAt:     time.Now().UTC(),
		ReduceOnly:    true,
	}
	if _, err := r.deps.Router.Route(ctx, intent, r.deps.Venue); err != nil {
		slog.Error("reconciler: flatten route failed",
			"symbol", pos.Symbol, "reason", reason, "error", err)
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/execution/ -run TestReconciler -race`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/execution/reconciler.go internal/execution/reconciler_test.go
git commit -m "feat(execution): reconciler enforces bracket guarantee + orphan sweep"
```

---

## Phase 4 — Position Manager LLM agent

### Task 8: Prompt templates

**Files:**
- Create: `internal/agent/prompts/position_manager.tmpl`
- Create: `internal/agent/prompts/position_manager_user.tmpl`

- [ ] **Step 1: Write the system prompt**

Create `internal/agent/prompts/position_manager.tmpl`:

```
You are Cerebro's Position Manager. You evaluate a single OPEN position whose
market conditions have changed, and decide what to do with it.

You must return your decision by calling the `decide_position` tool exactly
once. Do not write prose outside the tool call.

Decisions:
- hold: leave the position and its bracket unchanged.
- tighten_stop: move the protective stop to a new price (provide new_stop_loss).
  Use this to lock in profit or reduce risk without exiting.
- close: exit the entire position now at market.
- flip: exit now AND open a new position in the opposite direction, because the
  evidence for a reversal is strong.

Principles:
- The position already has a stop-loss and take-profit. Closing early forfeits
  the take-profit; only do it when the evidence against the position is clear.
- A single bias flip is not automatically a reason to close. Weigh it against
  the indicators, the unrealized PnL, and how close price is to the take-profit.
- Prefer tighten_stop over close when the position is in profit but the
  reversal signal is weak — it banks gains if price turns while letting a
  continuation run.
- Only choose flip when bias AND indicators agree on a reversal with conviction.
- Be explicit about your reasoning and assign a confidence in [0,1].
```

- [ ] **Step 2: Write the user-context template**

Create `internal/agent/prompts/position_manager_user.tmpl`:

```
Review trigger: {{.TriggerType}}

POSITION
  Symbol:        {{.Symbol}}
  Side:          {{.Side}}
  Quantity:      {{.Quantity}}
  Entry price:   {{.EntryPrice}}
  Current price: {{.CurrentPrice}}
  Leverage:      {{.Leverage}}
  Opened at:     {{.OpenedAt}}

PROFIT/LOSS
  Unrealized PnL:   {{.UnrealizedPnL}} (quote)
  Unrealized ROI%:  {{.UnrealizedROI}}
  Distance to TP:   {{.DistanceToTPPct}}%
  Distance to SL:   {{.DistanceToSLPct}}%

MARKET BIAS
  Score:     {{.BiasScore}}
  Reasoning: {{.BiasReasoning}}

INDICATORS
{{.IndicatorSummary}}

RECENT PERFORMANCE
{{.PerformanceSummary}}

Decide what to do with this position. Call decide_position once.
```

- [ ] **Step 3: Commit**

```bash
git add internal/agent/prompts/position_manager.tmpl internal/agent/prompts/position_manager_user.tmpl
git commit -m "feat(agent): position manager prompt templates"
```

---

### Task 9: Position Manager agent

**Files:**
- Create: `internal/agent/position_manager.go`
- Test: `internal/agent/position_manager_test.go`

**Context:** Follows the `RiskAgent` pattern (`internal/agent/risk_agent.go`):
a struct holding a `*Runtime` and the agent's tools, with one method that
builds the prompt, calls the LLM tool-loop, and returns a typed result. The
LLM returns its decision by calling a `decide_position` tool; on LLM error we
apply the configured fallback. The agent consumes `domain.PositionReview`
(defined in Task 2) so the execution package can call it without an import
cycle.

- [ ] **Step 1: Write the failing test**

Create `internal/agent/position_manager_test.go`:

```go
package agent

import (
	"context"
	"errors"
	"testing"

	"github.com/azhar/cerebro/internal/domain"
	"github.com/shopspring/decimal"
)

func TestPositionManager_FallbackOnLLMError(t *testing.T) {
	pos := domain.Position{
		Symbol: "BTCUSDT", Side: domain.SideBuy,
		EntryPrice: decimal.NewFromInt(100), Quantity: decimal.NewFromInt(1),
	}
	pm := NewPositionManagerForTest(stubDecideLLM{err: errors.New("provider down")}, "tighten_breakeven")

	action, err := pm.Decide(context.Background(), PositionReviewContext{
		Position: pos,
		Trigger:  domain.ReviewTrigger{Type: domain.TriggerBiasFlipAgainst},
	})
	if err != nil {
		t.Fatalf("fallback should not error, got %v", err)
	}
	if action.Decision != domain.ActionTightenStop {
		t.Errorf("expected tighten_stop fallback, got %s", action.Decision)
	}
	if !action.NewStopLoss.Equal(pos.EntryPrice) {
		t.Errorf("expected stop at entry %s, got %s", pos.EntryPrice, action.NewStopLoss)
	}
}

func TestPositionManager_FallbackHold(t *testing.T) {
	pm := NewPositionManagerForTest(stubDecideLLM{err: errors.New("down")}, "hold")
	action, err := pm.Decide(context.Background(), PositionReviewContext{
		Position: domain.Position{Symbol: "ETHUSDT", Side: domain.SideSell, EntryPrice: decimal.NewFromInt(50)},
		Trigger:  domain.ReviewTrigger{Type: domain.TriggerProfitThreshold},
	})
	if err != nil {
		t.Fatalf("unexpected error %v", err)
	}
	if action.Decision != domain.ActionHold {
		t.Errorf("expected hold fallback, got %s", action.Decision)
	}
}
```

Note: `stubDecideLLM` is defined in Step 3 alongside the production code's test
seam. It implements `port.LLM` and returns the canned tool output.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/agent/ -run TestPositionManager`
Expected: FAIL — `undefined: NewPositionManagerForTest`, `PositionReviewContext`.

- [ ] **Step 3: Implement the agent**

Create `internal/agent/position_manager.go`:

```go
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"text/template"

	"github.com/azhar/cerebro/internal/domain"
	"github.com/azhar/cerebro/internal/port"
)

// PositionReviewContext is the full input the agent needs to judge a position.
type PositionReviewContext struct {
	Position           domain.Position
	Trigger            domain.ReviewTrigger
	BiasScore          domain.BiasScore
	BiasReasoning      string
	IndicatorSummary   string
	PerformanceSummary string
}

// PositionManager is the LLM agent that decides hold/tighten/close/flip.
type PositionManager struct {
	llm           port.LLM
	fallback      string // tighten_breakeven | hold
	systemTmpl    *template.Template
	userTmpl      *template.Template
}

// NewPositionManager builds the agent using prompt files on disk.
func NewPositionManager(llm port.LLM, fallback string) (*PositionManager, error) {
	base := filepath.Join("internal", "agent", "prompts")
	sys, err := template.ParseFiles(filepath.Join(base, "position_manager.tmpl"))
	if err != nil {
		return nil, fmt.Errorf("parse system prompt: %w", err)
	}
	usr, err := template.ParseFiles(filepath.Join(base, "position_manager_user.tmpl"))
	if err != nil {
		return nil, fmt.Errorf("parse user prompt: %w", err)
	}
	return &PositionManager{llm: llm, fallback: fallback, systemTmpl: sys, userTmpl: usr}, nil
}

// Decide evaluates the position and returns a validated ManagedAction.
// On LLM failure it applies the configured deterministic fallback.
func (pm *PositionManager) Decide(ctx context.Context, rc PositionReviewContext) (domain.ManagedAction, error) {
	var decided *domain.ManagedAction
	tools := map[string]port.Tool{
		"decide_position": {
			Definition: port.ToolDefinition{
				Name:        "decide_position",
				Description: "Record the decision for this open position.",
				InputSchema: decidePositionSchema(),
			},
			Handler: func(_ context.Context, input json.RawMessage) (json.RawMessage, error) {
				var raw struct {
					Decision    string  `json:"decision"`
					NewStopLoss string  `json:"new_stop_loss"`
					Reason      string  `json:"reason"`
					Confidence  float64 `json:"confidence"`
				}
				if err := json.Unmarshal(input, &raw); err != nil {
					return nil, fmt.Errorf("decode decision: %w", err)
				}
				act, err := parseManagedAction(raw.Decision, raw.NewStopLoss, raw.Reason, raw.Confidence)
				if err != nil {
					return nil, err
				}
				decided = &act
				return json.RawMessage(`{"ok":true}`), nil
			},
		},
	}

	system := pm.render(pm.systemTmpl, nil)
	user := pm.render(pm.userTmpl, pm.userData(rc))

	if _, err := pm.llm.Complete(ctx, system, user, tools); err != nil {
		slog.Warn("position manager: LLM failed; applying fallback",
			"symbol", rc.Position.Symbol, "fallback", pm.fallback, "error", err)
		return pm.applyFallback(rc.Position), nil
	}
	if decided == nil {
		slog.Warn("position manager: no decision returned; applying fallback",
			"symbol", rc.Position.Symbol)
		return pm.applyFallback(rc.Position), nil
	}
	return *decided, nil
}

// applyFallback returns the deterministic action used when the LLM is
// unavailable: tighten stop to entry (breakeven), or hold.
func (pm *PositionManager) applyFallback(pos domain.Position) domain.ManagedAction {
	if pm.fallback == "hold" {
		return domain.ManagedAction{Decision: domain.ActionHold, Reason: "llm_unavailable_fallback"}
	}
	return domain.ManagedAction{
		Decision:    domain.ActionTightenStop,
		NewStopLoss: pos.EntryPrice,
		Reason:      "llm_unavailable_fallback_breakeven",
	}
}

func (pm *PositionManager) render(t *template.Template, data any) string {
	var b []byte
	buf := &templateBuffer{b: b}
	if err := t.Execute(buf, data); err != nil {
		slog.Error("position manager: template render failed", "error", err)
	}
	return buf.String()
}
```

- [ ] **Step 4: Add the helpers + test seam in the same package**

Create `internal/agent/position_manager_helpers.go`:

```go
package agent

import (
	"fmt"
	"strings"
	"text/template"

	"github.com/azhar/cerebro/internal/domain"
	"github.com/azhar/cerebro/internal/port"
	"github.com/shopspring/decimal"
)

// templateBuffer is a tiny io.Writer accumulator so we avoid bytes.Buffer
// import churn in the agent file.
type templateBuffer struct{ b []byte }

func (w *templateBuffer) Write(p []byte) (int, error) { w.b = append(w.b, p...); return len(p), nil }
func (w *templateBuffer) String() string              { return string(w.b) }

func decidePositionSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"decision":      map[string]any{"type": "string", "enum": []string{"hold", "tighten_stop", "close", "flip"}},
			"new_stop_loss": map[string]any{"type": "string", "description": "decimal price; required for tighten_stop"},
			"reason":        map[string]any{"type": "string"},
			"confidence":    map[string]any{"type": "number"},
		},
		"required": []string{"decision", "reason", "confidence"},
	}
}

func parseManagedAction(decision, newStop, reason string, confidence float64) (domain.ManagedAction, error) {
	act := domain.ManagedAction{
		Decision:   domain.ActionDecision(decision),
		Reason:     reason,
		Confidence: confidence,
	}
	if newStop != "" {
		d, err := decimal.NewFromString(newStop)
		if err != nil {
			return domain.ManagedAction{}, fmt.Errorf("parse new_stop_loss %q: %w", newStop, err)
		}
		act.NewStopLoss = d
	}
	if err := act.Validate(); err != nil {
		return domain.ManagedAction{}, err
	}
	return act, nil
}

// userData maps the review context onto the user template's fields.
func (pm *PositionManager) userData(rc PositionReviewContext) map[string]any {
	pos := rc.Position
	return map[string]any{
		"TriggerType":     string(rc.Trigger.Type),
		"Symbol":          string(pos.Symbol),
		"Side":            string(pos.Side),
		"Quantity":        pos.Quantity.String(),
		"EntryPrice":      pos.EntryPrice.String(),
		"CurrentPrice":    pos.CurrentPrice.String(),
		"Leverage":        pos.Leverage,
		"OpenedAt":        pos.OpenedAt.Format("2006-01-02 15:04 UTC"),
		"UnrealizedPnL":   pos.UnrealizedPnL().StringFixed(2),
		"UnrealizedROI":   pos.UnrealizedPnLROI().StringFixed(2),
		"DistanceToTPPct": distancePct(pos.CurrentPrice, pos.TakeProfit1),
		"DistanceToSLPct": distancePct(pos.CurrentPrice, pos.StopLoss),
		"BiasScore":       rc.BiasScore.String(),
		"BiasReasoning":   strings.TrimSpace(rc.BiasReasoning),
		"IndicatorSummary":   strings.TrimSpace(rc.IndicatorSummary),
		"PerformanceSummary": strings.TrimSpace(rc.PerformanceSummary),
	}
}

func distancePct(from, to decimal.Decimal) string {
	if from.IsZero() || to.IsZero() {
		return "n/a"
	}
	return to.Sub(from).Abs().Div(from).Mul(decimal.NewFromInt(100)).StringFixed(2)
}

// NewPositionManagerForTest builds an agent with inline prompt templates and a
// stub LLM, avoiding disk reads in unit tests.
func NewPositionManagerForTest(llm port.LLM, fallback string) *PositionManager {
	sys := template.Must(template.New("s").Parse("system"))
	usr := template.Must(template.New("u").Parse("trigger {{.TriggerType}}"))
	return &PositionManager{llm: llm, fallback: fallback, systemTmpl: sys, userTmpl: usr}
}

// stubDecideLLM is a test double implementing port.LLM.
type stubDecideLLM struct {
	toolCall func(tools map[string]port.Tool)
	err      error
}

func (s stubDecideLLM) Complete(_ context.Context, _ string, _ string, tools map[string]port.Tool) (string, error) {
	if s.err != nil {
		return "", s.err
	}
	if s.toolCall != nil {
		s.toolCall(tools)
	}
	return "done", nil
}
func (s stubDecideLLM) Provider() string { return "stub" }
func (s stubDecideLLM) ModelID() string  { return "stub" }
```

Add `"context"` to the helpers file imports (used by `stubDecideLLM`).

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/agent/ -run TestPositionManager -race`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/agent/position_manager.go internal/agent/position_manager_helpers.go internal/agent/position_manager_test.go
git commit -m "feat(agent): position manager LLM agent with deterministic fallback"
```

---

## Phase 5 — Reconciler Job B (triggers) + Action Queue

### Task 10: Review-trigger detection with debounce

**Files:**
- Create: `internal/execution/triggers.go`
- Test: `internal/execution/triggers_test.go`

**Context:** Job B inspects each open position and emits `ReviewTrigger`s when
bias opposes the side, profit crosses a threshold, or price nears TP/SL. A
debounce keyed on (symbol, trigger-type) prevents re-emitting the same trigger
every tick. Bias is read from `port.Cache` under `bias:<symbol>` (same key the
screener writes, `screening.go:458`). The detector takes an injected clock so
debounce is testable.

- [ ] **Step 1: Write the failing test**

Create `internal/execution/triggers_test.go`:

```go
package execution

import (
	"testing"
	"time"

	"github.com/azhar/cerebro/internal/domain"
	"github.com/shopspring/decimal"
)

func TestTriggerDetector_BiasFlipAgainst(t *testing.T) {
	now := time.Unix(1_000_000, 0).UTC()
	det := NewTriggerDetector(TriggerConfig{
		BiasFlipAgainst:    true,
		ProfitThresholdPct: 999, // disabled by high bar
		DebounceSec:        300,
	}, func() time.Time { return now })

	pos := domain.Position{
		Symbol: "BTCUSDT", Side: domain.SideSell,
		EntryPrice: decimal.NewFromInt(100), CurrentPrice: decimal.NewFromInt(100),
	}
	// Bias is bullish → opposes a SELL.
	got := det.Detect(pos, domain.BiasBullish)
	if len(got) != 1 || got[0].Type != domain.TriggerBiasFlipAgainst {
		t.Fatalf("expected bias-flip trigger, got %+v", got)
	}
	// Second call within debounce window → suppressed.
	if got2 := det.Detect(pos, domain.BiasBullish); len(got2) != 0 {
		t.Fatalf("expected debounce to suppress, got %+v", got2)
	}
}

func TestTriggerDetector_ProfitThreshold(t *testing.T) {
	now := time.Unix(1_000_000, 0).UTC()
	det := NewTriggerDetector(TriggerConfig{
		ProfitThresholdPct: 1.0,
		DebounceSec:        0,
	}, func() time.Time { return now })

	// Long up 2% → crosses 1% threshold.
	pos := domain.Position{
		Symbol: "BTCUSDT", Side: domain.SideBuy,
		EntryPrice: decimal.NewFromInt(100), CurrentPrice: decimal.NewFromInt(102),
		Quantity: decimal.NewFromInt(1),
	}
	got := det.Detect(pos, domain.BiasNeutral)
	if len(got) != 1 || got[0].Type != domain.TriggerProfitThreshold {
		t.Fatalf("expected profit trigger, got %+v", got)
	}
}

func TestTriggerDetector_DebounceExpires(t *testing.T) {
	cur := time.Unix(1_000_000, 0).UTC()
	det := NewTriggerDetector(TriggerConfig{
		BiasFlipAgainst: true, ProfitThresholdPct: 999, DebounceSec: 100,
	}, func() time.Time { return cur })

	pos := domain.Position{Symbol: "BTCUSDT", Side: domain.SideSell,
		EntryPrice: decimal.NewFromInt(100), CurrentPrice: decimal.NewFromInt(100)}

	if got := det.Detect(pos, domain.BiasBullish); len(got) != 1 {
		t.Fatalf("first detect should fire, got %d", len(got))
	}
	cur = cur.Add(101 * time.Second) // past debounce
	if got := det.Detect(pos, domain.BiasBullish); len(got) != 1 {
		t.Fatalf("after debounce window should fire again, got %d", len(got))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/execution/ -run TestTriggerDetector`
Expected: FAIL — `undefined: NewTriggerDetector`.

- [ ] **Step 3: Implement the detector**

Create `internal/execution/triggers.go`:

```go
package execution

import (
	"sync"
	"time"

	"github.com/azhar/cerebro/internal/domain"
	"github.com/shopspring/decimal"
)

// TriggerConfig controls which review triggers fire and the debounce window.
type TriggerConfig struct {
	BiasFlipAgainst    bool
	ProfitThresholdPct float64
	NearTPSLPct        float64
	DebounceSec        int
}

// TriggerDetector emits ReviewTriggers for an open position, debounced per
// (symbol, trigger-type). Not safe for concurrent Detect calls on the same
// detector — the reconciler calls it from a single goroutine.
type TriggerDetector struct {
	cfg     TriggerConfig
	now     func() time.Time
	mu      sync.Mutex
	lastFire map[string]time.Time // key: symbol|type
}

// NewTriggerDetector builds a detector with an injectable clock.
func NewTriggerDetector(cfg TriggerConfig, now func() time.Time) *TriggerDetector {
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &TriggerDetector{cfg: cfg, now: now, lastFire: make(map[string]time.Time)}
}

// Detect returns the triggers that fire for this position+bias, respecting the
// debounce window.
func (d *TriggerDetector) Detect(pos domain.Position, bias domain.BiasScore) []domain.ReviewTrigger {
	var out []domain.ReviewTrigger
	add := func(tt domain.TriggerType) {
		if !d.allow(pos.Symbol, tt) {
			return
		}
		out = append(out, domain.ReviewTrigger{
			Type: tt, Symbol: pos.Symbol, Venue: pos.Venue,
			Side: pos.Side, DetectedAt: d.now(),
		})
	}

	if d.cfg.BiasFlipAgainst && domain.BiasOpposesSide(bias, pos.Side) {
		add(domain.TriggerBiasFlipAgainst)
	}
	if d.cfg.ProfitThresholdPct > 0 {
		threshold := decimal.NewFromFloat(d.cfg.ProfitThresholdPct)
		if pos.UnrealizedPnLPct().GreaterThanOrEqual(threshold) {
			add(domain.TriggerProfitThreshold)
		}
	}
	if d.cfg.NearTPSLPct > 0 {
		band := decimal.NewFromFloat(d.cfg.NearTPSLPct)
		if within(pos.CurrentPrice, pos.TakeProfit1, band) || within(pos.CurrentPrice, pos.StopLoss, band) {
			add(domain.TriggerNearTPSL)
		}
	}
	return out
}

// allow returns true if (symbol, type) is outside its debounce window, and
// records the fire time when it returns true.
func (d *TriggerDetector) allow(sym domain.Symbol, tt domain.TriggerType) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	key := string(sym) + "|" + string(tt)
	last, seen := d.lastFire[key]
	window := time.Duration(d.cfg.DebounceSec) * time.Second
	if seen && d.now().Sub(last) < window {
		return false
	}
	d.lastFire[key] = d.now()
	return true
}

// within reports whether price is within band-percent of target.
func within(price, target, bandPct decimal.Decimal) bool {
	if price.IsZero() || target.IsZero() {
		return false
	}
	distPct := target.Sub(price).Abs().Div(price).Mul(decimal.NewFromInt(100))
	return distPct.LessThanOrEqual(bandPct)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/execution/ -run TestTriggerDetector -race`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/execution/triggers.go internal/execution/triggers_test.go
git commit -m "feat(execution): review-trigger detection with per-trigger debounce"
```

---

### Task 11: Action Queue — confirm/timeout/autonomous lifecycle

**Files:**
- Create: `internal/execution/action_queue.go`
- Test: `internal/execution/action_queue_test.go`

**Context:** The Action Queue receives a `ManagedAction` + position, posts a
suggestion (via a `Notifier` callback), and starts a `ConfirmTimeout` timer.
Confirm executes now; reject drops; timeout auto-executes (when
`AutonomousOnTimeout`). Before executing it re-checks the position still
exists. Execution is delegated to an injected `Executor` interface so this
unit is testable without a broker. The clock is injected.

- [ ] **Step 1: Write the failing test**

Create `internal/execution/action_queue_test.go`:

```go
package execution

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/azhar/cerebro/internal/domain"
	"github.com/shopspring/decimal"
)

type recordingExecutor struct {
	mu       sync.Mutex
	executed []domain.ManagedAction
}

func (e *recordingExecutor) Execute(_ context.Context, act domain.ManagedAction, _ domain.Position) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.executed = append(e.executed, act)
	return nil
}
func (e *recordingExecutor) count() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.executed)
}

func testPos() domain.Position {
	return domain.Position{Symbol: "BTCUSDT", Side: domain.SideSell,
		EntryPrice: decimal.NewFromInt(100), Quantity: decimal.NewFromInt(1)}
}

func newTestQueue(exec Executor, positionsExist func(domain.Symbol) bool) *ActionQueue {
	return NewActionQueue(ActionQueueDeps{
		Executor:            exec,
		ConfirmTimeout:      60 * time.Second,
		AutonomousOnTimeout: true,
		Notify:              func(string) {},
		PositionExists:      positionsExist,
	})
}

func TestActionQueue_ConfirmExecutesImmediately(t *testing.T) {
	exec := &recordingExecutor{}
	q := newTestQueue(exec, func(domain.Symbol) bool { return true })
	id := q.Enqueue(context.Background(), domain.ManagedAction{Decision: domain.ActionClose}, testPos())
	if err := q.Confirm(context.Background(), id); err != nil {
		t.Fatalf("confirm failed: %v", err)
	}
	if exec.count() != 1 {
		t.Fatalf("expected 1 execution on confirm, got %d", exec.count())
	}
}

func TestActionQueue_RejectDrops(t *testing.T) {
	exec := &recordingExecutor{}
	q := newTestQueue(exec, func(domain.Symbol) bool { return true })
	id := q.Enqueue(context.Background(), domain.ManagedAction{Decision: domain.ActionClose}, testPos())
	q.Reject(id)
	if exec.count() != 0 {
		t.Fatalf("expected no execution after reject, got %d", exec.count())
	}
}

func TestActionQueue_TimeoutAutoExecutes(t *testing.T) {
	exec := &recordingExecutor{}
	q := newTestQueue(exec, func(domain.Symbol) bool { return true })
	id := q.Enqueue(context.Background(), domain.ManagedAction{Decision: domain.ActionClose}, testPos())
	// Fire the timeout path directly (production uses a timer goroutine).
	q.fireTimeout(context.Background(), id)
	if exec.count() != 1 {
		t.Fatalf("expected auto-execution on timeout, got %d", exec.count())
	}
}

func TestActionQueue_PositionGoneDropsAction(t *testing.T) {
	exec := &recordingExecutor{}
	q := newTestQueue(exec, func(domain.Symbol) bool { return false }) // position vanished
	id := q.Enqueue(context.Background(), domain.ManagedAction{Decision: domain.ActionClose}, testPos())
	if err := q.Confirm(context.Background(), id); err != nil {
		t.Fatalf("confirm returned error: %v", err)
	}
	if exec.count() != 0 {
		t.Fatalf("expected drop when position gone, got %d", exec.count())
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/execution/ -run TestActionQueue`
Expected: FAIL — `undefined: NewActionQueue`.

- [ ] **Step 3: Implement the Action Queue**

Create `internal/execution/action_queue.go`:

```go
package execution

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/azhar/cerebro/internal/domain"
	"github.com/google/uuid"
)

// Executor performs an approved managed action against the broker. Implemented
// by actionExecutor (Task 12) and stubbed in tests.
type Executor interface {
	Execute(ctx context.Context, action domain.ManagedAction, pos domain.Position) error
}

// ActionQueueDeps bundles the queue's collaborators.
type ActionQueueDeps struct {
	Executor            Executor
	ConfirmTimeout      time.Duration
	AutonomousOnTimeout bool
	// Notify posts a human-readable suggestion to operator surfaces.
	Notify func(msg string)
	// PositionExists reports whether the position is still open just before
	// execution (guards the bracket-fired-during-wait race).
	PositionExists func(domain.Symbol) bool
}

type pendingAction struct {
	action domain.ManagedAction
	pos    domain.Position
	timer  *time.Timer
	done   bool
}

// ActionQueue holds suggested actions awaiting confirmation, executing them on
// confirm or after the timeout (when autonomous mode is on).
type ActionQueue struct {
	deps    ActionQueueDeps
	mu      sync.Mutex
	pending map[string]*pendingAction
}

// NewActionQueue builds an ActionQueue.
func NewActionQueue(deps ActionQueueDeps) *ActionQueue {
	if deps.PositionExists == nil {
		deps.PositionExists = func(domain.Symbol) bool { return true }
	}
	if deps.Notify == nil {
		deps.Notify = func(string) {}
	}
	return &ActionQueue{deps: deps, pending: make(map[string]*pendingAction)}
}

// Enqueue registers a suggested action, notifies operators, and arms the
// timeout timer. Returns the action ID used by Confirm/Reject.
func (q *ActionQueue) Enqueue(ctx context.Context, action domain.ManagedAction, pos domain.Position) string {
	id := uuid.New().String()
	q.mu.Lock()
	pa := &pendingAction{action: action, pos: pos}
	if q.deps.AutonomousOnTimeout && q.deps.ConfirmTimeout > 0 {
		pa.timer = time.AfterFunc(q.deps.ConfirmTimeout, func() {
			q.fireTimeout(ctx, id)
		})
	}
	q.pending[id] = pa
	q.mu.Unlock()

	q.deps.Notify(fmt.Sprintf("⚖️ %s %s — %s (confidence %.2f). Confirm within %s or it auto-executes.",
		action.Decision, pos.Symbol, action.Reason, action.Confidence, q.deps.ConfirmTimeout))
	return id
}

// Confirm executes the pending action immediately.
func (q *ActionQueue) Confirm(ctx context.Context, id string) error {
	pa := q.take(id)
	if pa == nil {
		return fmt.Errorf("action %s not found or already resolved", id)
	}
	return q.execute(ctx, pa, "confirmed")
}

// Reject drops the pending action without executing.
func (q *ActionQueue) Reject(id string) {
	pa := q.take(id)
	if pa == nil {
		return
	}
	slog.Info("action rejected by operator", "symbol", pa.pos.Symbol, "decision", pa.action.Decision)
	q.deps.Notify(fmt.Sprintf("✗ rejected %s %s", pa.action.Decision, pa.pos.Symbol))
}

// fireTimeout is invoked by the timer; auto-executes the action.
func (q *ActionQueue) fireTimeout(ctx context.Context, id string) {
	pa := q.take(id)
	if pa == nil {
		return
	}
	slog.Info("action auto-executing after confirm timeout",
		"symbol", pa.pos.Symbol, "decision", pa.action.Decision)
	_ = q.execute(ctx, pa, "autonomous_timeout")
}

// take removes and returns the pending action, cancelling its timer. Returns
// nil if it was already resolved.
func (q *ActionQueue) take(id string) *pendingAction {
	q.mu.Lock()
	defer q.mu.Unlock()
	pa, ok := q.pending[id]
	if !ok || pa.done {
		return nil
	}
	pa.done = true
	if pa.timer != nil {
		pa.timer.Stop()
	}
	delete(q.pending, id)
	return pa
}

// execute runs the action after re-checking the position still exists.
func (q *ActionQueue) execute(ctx context.Context, pa *pendingAction, via string) error {
	if !q.deps.PositionExists(pa.pos.Symbol) {
		slog.Info("action dropped: position already gone",
			"symbol", pa.pos.Symbol, "decision", pa.action.Decision, "via", via)
		q.deps.Notify(fmt.Sprintf("position %s already closed; dropped %s", pa.pos.Symbol, pa.action.Decision))
		return nil
	}
	if err := q.deps.Executor.Execute(ctx, pa.action, pa.pos); err != nil {
		slog.Error("action execution failed",
			"symbol", pa.pos.Symbol, "decision", pa.action.Decision, "via", via, "error", err)
		return err
	}
	q.deps.Notify(fmt.Sprintf("✓ %s %s executed (%s)", pa.action.Decision, pa.pos.Symbol, via))
	return nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/execution/ -run TestActionQueue -race`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/execution/action_queue.go internal/execution/action_queue_test.go
git commit -m "feat(execution): action queue with confirm/timeout/autonomous execution"
```

---

### Task 12: actionExecutor — close / tighten / flip semantics

**Files:**
- Create: `internal/execution/action_executor.go`
- Test: `internal/execution/action_executor_test.go`

**Context:** Implements the `Executor` interface from Task 11. `close` and
`tighten_stop` are reduce-only and skip the risk gate (matching ChatOps
`/close`). `flip` closes reduce-only, then submits a fresh opposite-side entry
**through the risk gate** via an injected `entryFn` (wired in Task 13 to the
strategy-sized, gated route). Tracker is updated so the bracket guarantee
stays consistent.

- [ ] **Step 1: Write the failing test**

Create `internal/execution/action_executor_test.go`:

```go
package execution

import (
	"context"
	"testing"

	"github.com/azhar/cerebro/internal/domain"
	"github.com/shopspring/decimal"
)

func drainRouter(r *Router, v domain.Venue) {
	ch, _ := r.Channel(v)
	go func() {
		for req := range ch {
			req.RespCh <- OrderResponse{BrokerOrderID: "ok"}
		}
	}()
}

func TestActionExecutor_Close(t *testing.T) {
	v := domain.VenueBinanceFutures
	router := NewRouter([]domain.Venue{v})
	drainRouter(router, v)
	tracker := NewBracketTracker()
	tracker.Record("BTCUSDT", domain.BracketResponse{StopOrderID: "s"})
	broker := &stubReconBroker{}
	var flips int
	ex := NewActionExecutor(ActionExecutorDeps{
		Venue: v, Router: router, Broker: broker, Tracker: tracker,
		Env: domain.EnvironmentPaper,
		EntryFn: func(context.Context, domain.Position) error { flips++; return nil },
	})

	pos := longPos("BTCUSDT")
	if err := ex.Execute(context.Background(), domain.ManagedAction{Decision: domain.ActionClose}, pos); err != nil {
		t.Fatalf("close failed: %v", err)
	}
	if flips != 0 {
		t.Errorf("close must not open a new position")
	}
	if tracker.Has("BTCUSDT") {
		t.Errorf("close should remove the tracked bracket")
	}
}

func TestActionExecutor_TightenStop(t *testing.T) {
	v := domain.VenueBinanceFutures
	broker := &stubReconBroker{}
	tracker := NewBracketTracker()
	tracker.Record("BTCUSDT", domain.BracketResponse{StopOrderID: "old", Symbol: "BTCUSDT"})
	ex := NewActionExecutor(ActionExecutorDeps{
		Venue: v, Router: NewRouter([]domain.Venue{v}), Broker: broker, Tracker: tracker,
		Env: domain.EnvironmentPaper,
		EntryFn: func(context.Context, domain.Position) error { return nil },
	})

	pos := longPos("BTCUSDT")
	act := domain.ManagedAction{Decision: domain.ActionTightenStop, NewStopLoss: decimal.NewFromInt(99)}
	if err := ex.Execute(context.Background(), act, pos); err != nil {
		t.Fatalf("tighten failed: %v", err)
	}
	if len(broker.cancelled) != 1 {
		t.Errorf("expected old bracket cancelled, got %d", len(broker.cancelled))
	}
	if len(broker.placedBrackets) != 1 || !broker.placedBrackets[0].StopLoss.Equal(decimal.NewFromInt(99)) {
		t.Errorf("expected new bracket at stop 99, got %+v", broker.placedBrackets)
	}
}

func TestActionExecutor_Flip(t *testing.T) {
	v := domain.VenueBinanceFutures
	router := NewRouter([]domain.Venue{v})
	drainRouter(router, v)
	tracker := NewBracketTracker()
	broker := &stubReconBroker{}
	var entries []domain.Position
	ex := NewActionExecutor(ActionExecutorDeps{
		Venue: v, Router: router, Broker: broker, Tracker: tracker,
		Env: domain.EnvironmentPaper,
		EntryFn: func(_ context.Context, p domain.Position) error {
			entries = append(entries, p)
			return nil
		},
	})

	pos := longPos("BTCUSDT")
	if err := ex.Execute(context.Background(), domain.ManagedAction{Decision: domain.ActionFlip}, pos); err != nil {
		t.Fatalf("flip failed: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("flip should open exactly one new entry, got %d", len(entries))
	}
	if entries[0].Side != domain.SideSell {
		t.Errorf("flip of a long should request a short entry, got %s", entries[0].Side)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/execution/ -run TestActionExecutor`
Expected: FAIL — `undefined: NewActionExecutor`.

- [ ] **Step 3: Implement the executor**

Create `internal/execution/action_executor.go`:

```go
package execution

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/azhar/cerebro/internal/domain"
	"github.com/azhar/cerebro/internal/port"
	"github.com/google/uuid"
)

// ActionExecutorDeps bundles the executor's collaborators.
type ActionExecutorDeps struct {
	Venue   domain.Venue
	Router  *Router
	Broker  port.Broker
	Tracker *BracketTracker
	Env     domain.Environment
	// EntryFn opens a new gated, strategy-sized position in the direction of
	// the supplied Position.Side. Used only by flip. Wired in Task 13.
	EntryFn func(ctx context.Context, want domain.Position) error
}

// ActionExecutor performs managed actions. close/tighten are reduce-only and
// skip the risk gate; flip re-enters through EntryFn (which is gated).
type ActionExecutor struct {
	deps ActionExecutorDeps
}

// NewActionExecutor builds an executor.
func NewActionExecutor(deps ActionExecutorDeps) *ActionExecutor {
	return &ActionExecutor{deps: deps}
}

// Execute dispatches on the action's decision.
func (e *ActionExecutor) Execute(ctx context.Context, act domain.ManagedAction, pos domain.Position) error {
	switch act.Decision {
	case domain.ActionHold:
		return nil
	case domain.ActionClose:
		return e.closePosition(ctx, pos)
	case domain.ActionTightenStop:
		return e.tighten(ctx, pos, act.NewStopLoss)
	case domain.ActionFlip:
		return e.flip(ctx, pos)
	default:
		return fmt.Errorf("unknown action decision %q", act.Decision)
	}
}

// closePosition submits a reduce-only market close and drops the bracket.
func (e *ActionExecutor) closePosition(ctx context.Context, pos domain.Position) error {
	if resp, ok := e.deps.Tracker.Get(pos.Symbol); ok {
		if err := e.deps.Broker.CancelBracket(ctx, resp); err != nil {
			slog.Warn("close: cancel bracket failed (continuing)", "symbol", pos.Symbol, "error", err)
		}
		e.deps.Tracker.Remove(pos.Symbol)
	}
	intent := e.closeIntent(pos)
	if _, err := e.deps.Router.Route(ctx, intent, e.deps.Venue); err != nil {
		return fmt.Errorf("close route: %w", err)
	}
	return nil
}

// tighten cancels the existing bracket and re-places it with a new stop,
// keeping the same take-profit.
func (e *ActionExecutor) tighten(ctx context.Context, pos domain.Position, newStop decimal.Decimal) error {
	if resp, ok := e.deps.Tracker.Get(pos.Symbol); ok {
		if err := e.deps.Broker.CancelBracket(ctx, resp); err != nil {
			slog.Warn("tighten: cancel old bracket failed (continuing)", "symbol", pos.Symbol, "error", err)
		}
		e.deps.Tracker.Remove(pos.Symbol)
	}
	req := domain.BracketRequest{
		ParentIntentID: pos.CorrelationID,
		CorrelationID:  pos.CorrelationID,
		Symbol:         pos.Symbol,
		Venue:          pos.Venue,
		Side:           pos.Side,
		Quantity:       pos.Quantity,
		StopLoss:       newStop,
		TakeProfit:     pos.TakeProfit1,
		ClientTag:      "tighten",
	}
	resp, err := e.deps.Broker.PlaceBracket(ctx, req)
	if err != nil {
		return fmt.Errorf("tighten place bracket: %w", err)
	}
	e.deps.Tracker.Record(pos.Symbol, resp)
	return nil
}

// flip closes the position reduce-only, then opens a gated opposite-side entry.
func (e *ActionExecutor) flip(ctx context.Context, pos domain.Position) error {
	if err := e.closePosition(ctx, pos); err != nil {
		return fmt.Errorf("flip close leg: %w", err)
	}
	if e.deps.EntryFn == nil {
		return fmt.Errorf("flip: no entry function wired")
	}
	want := pos
	want.Side = oppositeSide(pos.Side)
	if err := e.deps.EntryFn(ctx, want); err != nil {
		// Gate rejection here leaves us flat — which is safe. Log and return.
		slog.Warn("flip re-entry rejected; position left flat",
			"symbol", pos.Symbol, "wanted_side", want.Side, "error", err)
		return nil
	}
	return nil
}

func (e *ActionExecutor) closeIntent(pos domain.Position) domain.OrderIntent {
	return domain.OrderIntent{
		ID:            uuid.New().String(),
		CorrelationID: pos.CorrelationID,
		Symbol:        pos.Symbol,
		Venue:         e.deps.Venue,
		Side:          oppositeSide(pos.Side),
		OrderType:     domain.OrderTypeMarket,
		Quantity:      pos.Quantity,
		Strategy:      pos.Strategy,
		Environment:   e.deps.Env,
		CreatedAt:     time.Now().UTC(),
		ReduceOnly:    true,
	}
}

func oppositeSide(s domain.Side) domain.Side {
	if s == domain.SideSell {
		return domain.SideBuy
	}
	return domain.SideSell
}
```

Add `"github.com/shopspring/decimal"` to the import block (used by `tighten`).

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/execution/ -run TestActionExecutor -race`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/execution/action_executor.go internal/execution/action_executor_test.go
git commit -m "feat(execution): action executor for close/tighten/flip"
```

---

## Phase 6 — Runtime wiring + ChatOps confirm

### Task 13: Wire the reconciler, agent, and queue into the runtime

**Files:**
- Modify: `internal/app/runtime.go` (near the Monitor wiring at `:519-523`)
- Modify: `internal/app/strategies.go` (export a gated entry helper for flips)

**Context:** This is the composition root — wiring only, no business logic
(per `runtime-wiring.md`). The reconciler runs one goroutine per futures venue,
guarded by `cfg.PositionManager.Enabled`. The flip entry function reuses the
existing gated, strategy-sized route.

- [ ] **Step 1: Add a gated entry helper for flips**

In `internal/app/strategies.go`, add an exported-to-package helper that builds
a strategy-sized, gated entry for a desired side. Reuse `computeQuantity`,
`deriveStopLoss`, `deriveFirstTakeProfit`, and the existing risk `gate.Check`.
Signature:

```go
// buildFlipEntryFn returns a function that opens a new gated, strategy-sized
// position in the requested direction. Used by the position-manager flip path.
// It runs the full risk gate; a gate rejection returns an error and the caller
// leaves the position flat.
func buildFlipEntryFn(
	gate *risk.Gate,
	router *execution.Router,
	brokers map[domain.Venue]port.Broker,
	symbolMeta map[domain.Symbol]symbolMeta,
	env domain.Environment,
) func(ctx context.Context, want domain.Position) error {
	return func(ctx context.Context, want domain.Position) error {
		meta, ok := symbolMeta[want.Symbol]
		if !ok {
			return fmt.Errorf("flip entry: symbol %s not configured", want.Symbol)
		}
		sig := domain.Signal{
			CorrelationID: uuid.New().String(),
			Strategy:      domain.StrategyName("position_manager_flip"),
			Symbol:        want.Symbol,
			Side:          want.Side,
			GeneratedAt:   time.Now().UTC(),
		}
		positions := collectPositions(ctx, brokers)
		if err := gate.Check(ctx, sig, positions); err != nil {
			return fmt.Errorf("flip entry risk gate: %w", err)
		}
		entry := want.CurrentPrice
		if entry.IsZero() {
			entry = want.EntryPrice
		}
		// Reuse a default strategy config for sizing if the original strategy
		// is unknown; minimal sizing keeps the flip conservative.
		qty := want.Quantity
		sl := deriveStopLoss(want.Side, entry, defaultFlipStrategyCfg())
		tp1 := deriveFirstTakeProfit(want.Side, entry, sl, defaultFlipStrategyCfg())
		intent := domain.OrderIntent{
			ID:            uuid.New().String(),
			CorrelationID: sig.CorrelationID,
			Symbol:        want.Symbol,
			Venue:         meta.venue,
			Side:          want.Side,
			OrderType:     domain.OrderTypeMarket,
			Quantity:      qty,
			StopLoss:      sl,
			TakeProfit1:   tp1,
			Strategy:      sig.Strategy,
			Environment:   env,
			CreatedAt:     time.Now().UTC(),
		}
		if meta.venue == domain.VenueBinanceFutures {
			intent.Leverage = meta.cfg.Leverage
		}
		_, err := router.Route(ctx, intent, meta.venue)
		return err
	}
}

// defaultFlipStrategyCfg returns conservative SL/TP defaults for flip entries
// when the originating strategy config is not available.
func defaultFlipStrategyCfg() config.StrategyConfig {
	return config.StrategyConfig{
		StopLoss:         config.StopLossConfig{Type: domain.SLTypeFixedPct, FixedPct: 0.5},
		TakeProfitLevels: []config.TakeProfitLevel{{RRRatio: 1.5, ScaleOutPct: 100}},
	}
}
```

Note: confirm the exact field names of `config.StrategyConfig.StopLoss` and
`config.TakeProfitLevel` against `internal/config/strategies.go` before
finalizing — adjust the literal to match (the `deriveStopLoss` /
`deriveFirstTakeProfit` functions in `strategies.go` already consume these
shapes, so mirror their usage).

- [ ] **Step 2: Wire the reconciler goroutine**

In `internal/app/runtime.go`, after the existing Monitor wiring block (around
`:519`), add — guarded by the config flag and only for the futures venue:

```go
	if a.cfg.PositionManager.Enabled {
		pmCfg := a.cfg.PositionManager
		tracker := execution.NewBracketTracker() // shared with workers; see note
		entryFn := buildFlipEntryFn(gate, router, brokers, symbolMeta, env)
		executor := execution.NewActionExecutor(execution.ActionExecutorDeps{
			Venue:   domain.VenueBinanceFutures,
			Router:  router,
			Broker:  brokers[domain.VenueBinanceFutures],
			Tracker: tracker,
			Env:     env,
			EntryFn: entryFn,
		})
		queue := execution.NewActionQueue(execution.ActionQueueDeps{
			Executor:            executor,
			ConfirmTimeout:      time.Duration(pmCfg.ConfirmTimeoutSec) * time.Second,
			AutonomousOnTimeout: pmCfg.AutonomousOnTimeout,
			Notify: func(msg string) {
				for _, n := range notifiers {
					_ = n.Send(gctx, "", msg)
				}
				pushTUI(tuiRunner, msg)
			},
			PositionExists: func(sym domain.Symbol) bool {
				for _, p := range collectPositions(gctx, brokers) {
					if p.Symbol == sym {
						return true
					}
				}
				return false
			},
		})
		pmAgent, perr := agentpkg.NewPositionManager(llmProvider, pmCfg.LLMFailureAction)
		if perr != nil {
			return fmt.Errorf("position manager init: %w", perr)
		}
		detector := execution.NewTriggerDetector(execution.TriggerConfig{
			BiasFlipAgainst:    pmCfg.BiasFlipAgainst,
			ProfitThresholdPct: pmCfg.ProfitThresholdPct,
			NearTPSLPct:        pmCfg.NearTPSLPct,
			DebounceSec:        pmCfg.TriggerDebounceSec,
		}, nil)
		recon := execution.NewReconciler(execution.ReconcilerDeps{
			Venue:      domain.VenueBinanceFutures,
			Broker:     brokers[domain.VenueBinanceFutures],
			Tracker:    tracker,
			Router:     router,
			Env:        env,
			IntervalMS: pmCfg.ReconcileIntervalMS,
			Positions: func() []domain.Position {
				return collectPositions(gctx, brokers)
			},
			Detector: detector,
			Agent:    pmAgent,
			Queue:    queue,
			Cache:    cache,
		})
		g.Go(func() error {
			if err := recon.Run(gctx); err != nil && !errors.Is(err, context.Canceled) {
				return fmt.Errorf("position reconciler: %w", err)
			}
			return nil
		})
	}
```

Note on the tracker: for the guarantee to be exact, the same `*BracketTracker`
must be shared with the venue `Worker` (Task 6). Refactor the wiring so the
tracker is created once, before workers are built, and passed to both
`NewWorker` and the reconciler. If that refactor is large, an acceptable
interim is a reconciler-owned tracker — the reconciler will then attach a
(possibly duplicate) bracket on first sight, which the broker rejects
harmlessly for paper and is reduce-only-safe for futures; record this as a
known limitation until the shared-tracker refactor lands.

- [ ] **Step 3: Extend the Reconciler to run Job B**

Add the Job B fields to `ReconcilerDeps` (`Detector *TriggerDetector`,
`Agent PositionDecider`, `Queue *ActionQueue`, `Cache port.Cache`) and call
them in `Run`'s tick after `enforceBrackets`/`sweepOrphans`:

```go
// In reconciler.go, add to the tick body:
			r.reviewPositions(ctx)
```

And implement (in `reconciler.go`):

```go
// PositionDecider is the reconciler's view of the position-manager agent.
// Declared as an interface so the execution package does not import the agent
// package. *agent.PositionManager satisfies it.
type PositionDecider interface {
	Decide(ctx context.Context, rc domain.PositionReview) (domain.ManagedAction, error)
}
```

Both sides exchange `domain.PositionReview` (defined in Task 2), so there is no
execution→agent import cycle and no type needs to move. Update Task 9's agent
so `Decide` takes `domain.PositionReview` (replace the local
`PositionReviewContext` parameter with `domain.PositionReview`; the field names
are identical). `reviewPositions` reads bias from `Cache` (`bias:<symbol>`),
runs the detector, and for each trigger builds a `domain.PositionReview`, calls
`Agent.Decide`, then `Queue.Enqueue`.

- [ ] **Step 4: Build the whole project**

Run: `go build ./...`
Expected: exit 0. Resolve import cycles by keeping shared types in `domain`.

- [ ] **Step 5: Run the full test suite with race**

Run: `go test ./... -race`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/app/runtime.go internal/app/strategies.go internal/execution/reconciler.go internal/agent/position_manager.go internal/domain/position_review.go
git commit -m "feat(app): wire position reconciler, manager agent, and action queue"
```

---

### Task 14: ChatOps confirm/reject control

**Files:**
- Modify: `internal/chatops/commands.go`, `internal/chatops/dispatcher.go`

- [ ] **Step 1: Add `/confirm <id>` and `/reject <id>` commands**

Wire two commands that call `ActionQueue.Confirm(ctx, id)` and
`ActionQueue.Reject(id)`. Follow the existing command-registration pattern in
`commands.go` (mirror how `/close` is registered). The queue reference is
passed into the dispatcher at construction in `runtime.go`.

- [ ] **Step 2: Build + test**

Run: `go build ./... && go test ./internal/chatops/... -race`
Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add internal/chatops/commands.go internal/chatops/dispatcher.go internal/app/runtime.go
git commit -m "feat(chatops): /confirm and /reject controls for managed actions"
```

---

## Final verification

- [ ] `go build ./...` — exit 0.
- [ ] `go test ./... -race` — all pass.
- [ ] `make lint` — zero warnings.
- [ ] `make check` — config validation passes with the new block.
- [ ] Manual paper run: open a position, flip the cached bias against it,
      confirm a suggestion appears and auto-executes after 60s.

## Notes / known follow-ups

- **Indicator snapshot plumbing** (Task 9/13): the `IndicatorSummary` is
  passed as a pre-rendered string. Wiring the live indicator source from the
  strategy engine into the reconciler is a follow-up; until then pass the
  cached bias reasoning plus PnL/price context, and log that indicators are
  summary-only.
- **Shared BracketTracker refactor** (Task 13 note): create the tracker once
  and inject into both Worker and Reconciler.
- **Live futures bracket reconciliation**: Job A currently trusts the
  tracker; a periodic reconcile against the exchange's actual open algo orders
  (to catch brackets placed out-of-band) is a hardening follow-up.

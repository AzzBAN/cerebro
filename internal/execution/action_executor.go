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

// ActionExecutorDeps bundles the ActionExecutor's collaborators.
type ActionExecutorDeps struct {
	Venue   domain.Venue
	Broker  port.Broker
	Router  *Router
	Tracker *BracketTracker
	Env     domain.Environment
	// EntryFn opens a new gated, strategy-sized position in the direction of
	// the supplied Position.Side. Used only by ActionFlip after the existing
	// position is closed. When nil, a flip degrades to a plain close (logged),
	// so the position is never left reversed-but-naked.
	EntryFn func(ctx context.Context, want domain.Position) error
}

// ActionExecutor translates a ManagedAction decision into concrete broker
// operations. It is designed to be passed as the ExecuteFunc to ActionQueue.
//
// Decision semantics:
//   - ActionHold         → no-op.
//   - ActionClose        → reduce-only market close routed via Router.
//   - ActionTightenStop  → cancel existing bracket (if tracked) then place a
//     new bracket with the tighter stop from action.NewStopLoss.
//   - ActionFlip         → reduce-only close, then open a gated, strategy-sized
//     entry in the opposite direction via EntryFn. A gate rejection on the
//     re-entry leaves the position flat (never reversed-but-naked).
type ActionExecutor struct {
	deps ActionExecutorDeps
}

// NewActionExecutor creates an ActionExecutor.
func NewActionExecutor(deps ActionExecutorDeps) *ActionExecutor {
	return &ActionExecutor{deps: deps}
}

// Execute dispatches the QueuedAction to the appropriate handler.
// It satisfies the ExecuteFunc signature and is safe to call concurrently.
func (e *ActionExecutor) Execute(ctx context.Context, item QueuedAction) error {
	switch item.Action.Decision {
	case domain.ActionHold:
		return nil
	case domain.ActionClose:
		return e.routeClose(ctx, item)
	case domain.ActionFlip:
		return e.flip(ctx, item)
	case domain.ActionTightenStop:
		return e.tightenStop(ctx, item)
	default:
		return fmt.Errorf("executor: unknown decision %q", item.Action.Decision)
	}
}

// routeClose submits a reduce-only market order to close the position. When
// item.Action.CloseQuantity is positive it closes only that many units (a
// partial close), capped at the position size; otherwise it closes in full.
func (e *ActionExecutor) routeClose(ctx context.Context, item QueuedAction) error {
	pos := item.Position
	closeSide := domain.SideSell
	if pos.Side == domain.SideSell {
		closeSide = domain.SideBuy
	}
	qty := pos.Quantity
	if cq := item.Action.CloseQuantity; cq.IsPositive() && cq.LessThan(pos.Quantity) {
		qty = cq
	}
	intent := domain.OrderIntent{
		ID:            uuid.New().String(),
		CorrelationID: pos.CorrelationID,
		Symbol:        pos.Symbol,
		Venue:         e.deps.Venue,
		Side:          closeSide,
		OrderType:     domain.OrderTypeMarket,
		Quantity:      qty,
		Strategy:      pos.Strategy,
		Environment:   e.deps.Env,
		CreatedAt:     time.Now().UTC(),
		ReduceOnly:    true,
	}
	if _, err := e.deps.Router.Route(ctx, intent, e.deps.Venue); err != nil {
		return fmt.Errorf("executor: close route: %w", err)
	}
	slog.Info("executor: close routed",
		"symbol", pos.Symbol, "decision", item.Action.Decision,
		"qty", qty, "full", qty.Equal(pos.Quantity), "reason", item.Action.Reason)
	return nil
}

// flip closes the current position reduce-only, then opens a new gated,
// strategy-sized entry in the opposite direction via EntryFn. If EntryFn is
// nil the flip degrades to a plain close. A gate rejection (EntryFn error)
// leaves the position flat — never reversed-but-naked — and is logged, not
// propagated, since the protective close already succeeded.
func (e *ActionExecutor) flip(ctx context.Context, item QueuedAction) error {
	if err := e.routeClose(ctx, item); err != nil {
		return fmt.Errorf("executor: flip close leg: %w", err)
	}
	if e.deps.EntryFn == nil {
		slog.Warn("executor: flip requested but no EntryFn wired; left flat after close",
			"symbol", item.Position.Symbol)
		return nil
	}
	want := item.Position
	want.Side = domain.SideSell
	if item.Position.Side == domain.SideSell {
		want.Side = domain.SideBuy
	}
	if err := e.deps.EntryFn(ctx, want); err != nil {
		slog.Warn("executor: flip re-entry rejected; position left flat",
			"symbol", want.Symbol, "wanted_side", want.Side, "error", err)
		return nil
	}
	slog.Info("executor: flip complete",
		"symbol", want.Symbol, "new_side", want.Side, "reason", item.Action.Reason)
	return nil
}

// tightenStop cancels the existing bracket (if tracked) then places a new one
// with the tighter stop-loss from action.NewStopLoss.
func (e *ActionExecutor) tightenStop(ctx context.Context, item QueuedAction) error {
	pos := item.Position

	// Cancel existing bracket when tracked.
	if resp, ok := e.deps.Tracker.Get(pos.Symbol); ok {
		if err := e.deps.Broker.CancelBracket(ctx, resp); err != nil {
			slog.Warn("executor: cancel existing bracket failed; continuing with new bracket",
				"symbol", pos.Symbol, "error", err)
		}
		e.deps.Tracker.Remove(pos.Symbol)
	}

	req := domain.BracketRequest{
		CorrelationID: pos.CorrelationID,
		Symbol:        pos.Symbol,
		Venue:         pos.Venue,
		Side:          pos.Side,
		Quantity:      pos.Quantity,
		StopLoss:      item.Action.NewStopLoss,
		TakeProfit:    pos.TakeProfit1,
		ClientTag:     "executor_tighten",
	}
	resp, err := e.deps.Broker.PlaceBracket(ctx, req)
	if err != nil {
		return fmt.Errorf("executor: tighten_stop bracket: %w", err)
	}
	e.deps.Tracker.Record(pos.Symbol, resp)
	slog.Info("executor: stop tightened",
		"symbol", pos.Symbol, "new_stop", item.Action.NewStopLoss)
	return nil
}

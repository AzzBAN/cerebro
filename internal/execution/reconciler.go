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

// Reconciler enforces the hard TP/SL guarantee (Job A). Job B (review-trigger
// detection + agent) is wired in a later task. Job A is deterministic and runs
// even when the LLM is down.
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

// Run ticks Job A until ctx is cancelled.
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
// Missing -> attach; attach fails -> flatten (reduce-only).
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

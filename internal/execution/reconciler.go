package execution

import (
	"context"
	"log/slog"
	"time"

	"github.com/azhar/cerebro/internal/domain"
	"github.com/azhar/cerebro/internal/port"
	"github.com/google/uuid"
)

// PositionDecider is the reconciler's view of the position-manager agent.
// It is declared here as an interface so the execution package never imports
// the agent package — *agent.PositionManagerAgent satisfies it via its Review
// method, keeping the dependency direction one-way.
type PositionDecider interface {
	Review(ctx context.Context, review domain.PositionReview) (domain.ManagedAction, error)
}

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

	// ── Job B (optional) — review triggers + agent judgment ────────────────
	// When all three of Detector, Decider, and Queue are non-nil, the
	// reconciler runs Job B each tick: detect review triggers, ask the agent
	// for a decision, and enqueue the resulting action. When any is nil, only
	// the deterministic Job A (bracket guarantee + orphan sweep) runs.
	Detector *TriggerDetector
	Decider  PositionDecider
	Queue    *ActionQueue
	// Bias resolves the current bias for a symbol (e.g. from the Redis cache).
	// May be nil to skip the bias-flip trigger.
	Bias BiasFunc
	// BiasReason resolves an optional human-readable bias rationale for a
	// symbol, surfaced to the agent. May be nil.
	BiasReason func(domain.Symbol) string
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

// Run ticks Job A until ctx is cancelled. Job B (LLM-backed position review)
// runs on its OWN goroutine with its own ticker, so a slow or hung LLM can
// never delay Job A's deterministic bracket guarantee. Job B is only started
// when Detector, Decider, and Queue are all wired.
func (r *Reconciler) Run(ctx context.Context) error {
	interval := time.Duration(r.deps.IntervalMS) * time.Millisecond
	tick := time.NewTicker(interval)
	defer tick.Stop()
	slog.Info("position reconciler started", "venue", r.deps.Venue, "interval", interval)
	defer slog.Info("position reconciler stopping", "venue", r.deps.Venue)

	if r.deps.Detector != nil && r.deps.Decider != nil && r.deps.Queue != nil {
		go r.runReviewLoop(ctx, interval)
	}

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

// runReviewLoop ticks Job B independently of Job A. A single in-flight review
// pass blocks only this loop — Job A keeps its cadence regardless of LLM
// latency. Exits cleanly on ctx cancellation.
func (r *Reconciler) runReviewLoop(ctx context.Context, interval time.Duration) {
	tick := time.NewTicker(interval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			r.reviewPositions(ctx)
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
		if pos.ExternallyProtected {
			// Operator set SL/TP directly on the exchange. Respect it: do not
			// flatten and do not attach a duplicate Cerebro bracket.
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

// reviewPositions runs Job B: detect review triggers on open positions, ask
// the agent for a decision, and enqueue the resulting action. It is a no-op
// unless Detector, Decider, and Queue are all wired (Job B is optional).
func (r *Reconciler) reviewPositions(ctx context.Context) {
	if r.deps.Detector == nil || r.deps.Decider == nil || r.deps.Queue == nil {
		return
	}

	// Snapshot this venue's open positions and index them for trigger lookup.
	var positions []domain.Position
	bySymbol := make(map[domain.Symbol]domain.Position)
	for _, p := range r.deps.Positions() {
		if p.Venue != r.deps.Venue {
			continue
		}
		positions = append(positions, p)
		bySymbol[p.Symbol] = p
	}
	if len(positions) == 0 {
		return
	}

	for _, trig := range r.deps.Detector.Detect(positions, r.deps.Bias) {
		pos, ok := bySymbol[trig.Symbol]
		if !ok {
			continue
		}
		review := domain.PositionReview{
			Position: pos,
			Trigger:  trig,
		}
		if r.deps.Bias != nil {
			if score, found := r.deps.Bias(trig.Symbol); found {
				review.BiasScore = score
			}
		}
		if r.deps.BiasReason != nil {
			review.BiasReasoning = r.deps.BiasReason(trig.Symbol)
		}

		action, err := r.deps.Decider.Review(ctx, review)
		if err != nil {
			// Review already fails safe internally (returns a fallback action,
			// not an error), but guard anyway so a future change can't drop
			// the position silently.
			slog.Error("reconciler: position review failed; skipping",
				"symbol", trig.Symbol, "trigger", trig.Type, "error", err)
			continue
		}
		if action.Decision == domain.ActionHold {
			slog.Debug("reconciler: review decided HOLD",
				"symbol", trig.Symbol, "trigger", trig.Type)
			continue
		}
		id := r.deps.Queue.Enqueue(pos, trig, action)
		slog.Info("reconciler: queued managed action",
			"id", id, "symbol", trig.Symbol, "trigger", trig.Type, "action", action.Decision)
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

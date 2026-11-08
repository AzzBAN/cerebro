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

// ProtectionRefresher lets the reconciler re-check the exchange for an
// operator-set SL/TP on a single symbol right before it would flatten a
// position for "no protective levels". This closes the race where a position
// opened directly on the exchange arrives via the user-data WS (which carries
// no open-orders) before the periodic REST resync detects its protection.
// Implemented by the live futures/spot brokers; the paper matcher does not
// implement it (and paper positions are never externally protected).
type ProtectionRefresher interface {
	// RefreshProtection re-detects exchange-side protection for sym, updating
	// the broker's cache when found, and reports whether the symbol is now
	// externally protected.
	RefreshProtection(ctx context.Context, sym domain.Symbol) (bool, error)
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
	// FlattenGraceSec is how long (in seconds) the reconciler waits after
	// first seeing an unprotected, bracket-less position before it will
	// flatten for "no protective levels". This gives the REST open-orders
	// resync time to detect an SL/TP that was placed externally (e.g. on
	// the Binance website). 0 defaults to 30.
	FlattenGraceSec int

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

	// Propose (optional) routes an agent SL/TP adjustment on an externally-
	// protected position to the web confirmation store instead of the
	// autonomous action queue. When nil, such adjustments fall through to the
	// normal queue path. Only ActionTightenStop on an ExternallyProtected
	// position is routed here.
	Propose func(pos domain.Position, action domain.ManagedAction)
}

// Reconciler enforces the hard TP/SL guarantee (Job A). Job B (review-trigger
// detection + agent) is wired in a later task. Job A is deterministic and runs
// even when the LLM is down.
type Reconciler struct {
	deps ReconcilerDeps
	// firstSeen records when an unprotected, bracket-less position was first
	// encountered. Used by the flatten grace period to give the REST open-
	// orders resync time to detect externally-placed SL/TP before flattening.
	firstSeen map[domain.Symbol]time.Time
}

// NewReconciler builds a Reconciler.
func NewReconciler(deps ReconcilerDeps) *Reconciler {
	if deps.IntervalMS <= 0 {
		deps.IntervalMS = 5000
	}
	return &Reconciler{deps: deps, firstSeen: make(map[domain.Symbol]time.Time)}
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
			// Before flattening, re-check the exchange directly. A position
			// opened on the exchange arrives via the user-data WS (no open-
			// orders payload) ahead of the periodic REST resync that detects
			// its protection — without this check we'd flatten in that window.
			if refresher, ok := r.deps.Broker.(ProtectionRefresher); ok {
				protected, err := refresher.RefreshProtection(ctx, pos.Symbol)
				if err != nil {
					// Fail safe: an inconclusive check must not trigger a
					// destructive flatten. Skip this tick; retry next one.
					slog.WarnContext(ctx, "reconciler: protection re-check failed; deferring flatten",
						"symbol", pos.Symbol, "err", err)
					continue
				}
				if protected {
					slog.InfoContext(ctx, "reconciler: exchange-side SL/TP found on re-check; keeping position",
						"symbol", pos.Symbol)
					continue
				}
			}

			// Grace period: give the REST open-orders resync time to detect
			// externally-placed SL/TP before flattening. This runs after the
			// protection refresh so we can still detect protection during grace.
			firstSeen, ok := r.firstSeen[pos.Symbol]
			if !ok {
				r.firstSeen[pos.Symbol] = time.Now()
				slog.DebugContext(ctx, "reconciler: new unprotected position; starting grace period",
					"symbol", pos.Symbol, "grace_sec", r.deps.FlattenGraceSec)
				continue
			}
			elapsed := time.Since(firstSeen)
			if elapsed < time.Duration(r.deps.FlattenGraceSec)*time.Second {
				slog.DebugContext(ctx, "reconciler: position still in grace period; deferring flatten",
					"symbol", pos.Symbol, "elapsed_sec", int(elapsed.Seconds()),
					"grace_sec", r.deps.FlattenGraceSec)
				continue
			}
			delete(r.firstSeen, pos.Symbol)

			slog.Warn("reconciler: position has no SL/TP levels to attach; flattening",
				"symbol", pos.Symbol)
			r.flatten(ctx, pos, "no_protective_levels")
			continue
		}
		// Position has SL/TP — remove from firstSeen if present.
		delete(r.firstSeen, pos.Symbol)
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
		if pos.ExternallyProtected && action.Decision == domain.ActionTightenStop && r.deps.Propose != nil {
			r.deps.Propose(pos, action)
			slog.Info("reconciler: routed SL/TP adjustment to confirmation store",
				"symbol", trig.Symbol, "trigger", trig.Type, "new_stop", action.NewStopLoss)
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

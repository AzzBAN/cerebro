package execution

import (
	"context"
	"testing"
	"time"

	"github.com/azhar/cerebro/internal/domain"
	"github.com/azhar/cerebro/internal/port"
	"github.com/shopspring/decimal"
)

// stubReconBroker records bracket placements, flatten orders, and cancels.
type stubReconBroker struct {
	placeBracketErr error
	placedBrackets  []domain.BracketRequest
	placedOrders    []domain.OrderIntent
	cancelled       []domain.BracketResponse
	// refreshProtected / refreshErr drive the optional ProtectionRefresher
	// path; zero values (false, nil) preserve the pre-feature flatten behaviour.
	refreshProtected bool
	refreshErr       error
	refreshCalls     int
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

func (s *stubReconBroker) RefreshProtection(context.Context, domain.Symbol) (bool, error) {
	s.refreshCalls++
	return s.refreshProtected, s.refreshErr
}

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
		Router:    NewRouter([]domain.Venue{domain.VenueBinanceFutures}),
		Env:       domain.EnvironmentPaper,
		Positions: func() []domain.Position { return []domain.Position{longPos("BTCUSDT")} },
	})

	r.enforceBrackets(context.Background())

	if len(broker.placedBrackets) != 0 {
		t.Errorf("expected no new bracket, got %d", len(broker.placedBrackets))
	}
}

func TestReconciler_FlattensWhenBracketFails(t *testing.T) {
	broker := &stubReconBroker{placeBracketErr: errReconTest}
	tracker := NewBracketTracker()
	router := NewRouter([]domain.Venue{domain.VenueBinanceFutures})
	// Drain the router channel so Route doesn't block; respond to each request.
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
	// A reduce-only flatten order should have been routed (drained above).
}

// zeroProtPos is an open position with no Cerebro SL/TP and the external flag
// unset — the exact state that reaches the flatten branch.
func zeroProtPos(sym string) domain.Position {
	p := longPos(sym)
	p.StopLoss = decimal.Zero
	p.TakeProfit1 = decimal.Zero
	return p
}

func TestReconciler_RefreshFindsProtection_SkipsFlatten(t *testing.T) {
	broker := &stubReconBroker{refreshProtected: true}
	r := NewReconciler(ReconcilerDeps{
		Venue: domain.VenueBinanceFutures, Broker: broker, Tracker: NewBracketTracker(),
		Router:    nil, // flatten must NOT be reached (nil router would panic on Route)
		Env:       domain.EnvironmentPaper,
		Positions: func() []domain.Position { return []domain.Position{zeroProtPos("BTCUSDT")} },
	})

	r.enforceBrackets(context.Background())

	if broker.refreshCalls != 1 {
		t.Fatalf("expected one protection re-check, got %d", broker.refreshCalls)
	}
	if len(broker.placedBrackets) != 0 {
		t.Errorf("must not attach a bracket when exchange-side protection exists, got %d", len(broker.placedBrackets))
	}
}

func TestReconciler_RefreshErrors_DefersFlatten(t *testing.T) {
	broker := &stubReconBroker{refreshErr: errReconTest}
	r := NewReconciler(ReconcilerDeps{
		Venue: domain.VenueBinanceFutures, Broker: broker, Tracker: NewBracketTracker(),
		Router:    nil, // an inconclusive re-check must fail safe: no flatten this tick
		Env:       domain.EnvironmentPaper,
		Positions: func() []domain.Position { return []domain.Position{zeroProtPos("BTCUSDT")} },
	})

	r.enforceBrackets(context.Background())

	if broker.refreshCalls != 1 {
		t.Fatalf("expected one protection re-check, got %d", broker.refreshCalls)
	}
}

func TestReconciler_CancelsOrphanBracket(t *testing.T) {
	broker := &stubReconBroker{}
	tracker := NewBracketTracker()
	tracker.Record("ETHUSDT", domain.BracketResponse{StopOrderID: "orphan", Symbol: "ETHUSDT"})
	r := NewReconciler(ReconcilerDeps{
		Venue: domain.VenueBinanceFutures, Broker: broker, Tracker: tracker,
		Router:    NewRouter([]domain.Venue{domain.VenueBinanceFutures}),
		Env:       domain.EnvironmentPaper,
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

type errReconTestType string

func (e errReconTestType) Error() string { return string(e) }

var errReconTest = errReconTestType("boom")

// stubDecider returns a fixed ManagedAction, satisfying PositionDecider.
type stubDecider struct {
	action domain.ManagedAction
	calls  int
}

func (s *stubDecider) Review(_ context.Context, _ domain.PositionReview) (domain.ManagedAction, error) {
	s.calls++
	return s.action, nil
}

func TestReconciler_FlattenGracePeriod(t *testing.T) {
	t.Run("does not flatten within grace period", func(t *testing.T) {
		broker := &stubReconBroker{}
		r := NewReconciler(ReconcilerDeps{
			Venue:           domain.VenueBinanceFutures,
			Broker:          broker,
			Tracker:         NewBracketTracker(),
			Router:          nil, // nil would panic if flatten reached — proves it is NOT reached
			Env:             domain.EnvironmentPaper,
			Positions:       func() []domain.Position { return []domain.Position{zeroProtPos("BTCUSDT")} },
			FlattenGraceSec: 30,
		})

		// First call should record firstSeen but not flatten
		r.enforceBrackets(context.Background())
		if len(broker.placedOrders) != 0 {
			t.Errorf("expected no flatten within grace period, got %d orders", len(broker.placedOrders))
		}
		// firstSeen must be set
		if _, ok := r.firstSeen["BTCUSDT"]; !ok {
			t.Error("expected firstSeen to be recorded on first encounter")
		}

		// Second call still within grace period
		r.enforceBrackets(context.Background())
		if len(broker.placedOrders) != 0 {
			t.Errorf("expected no flatten within grace period, got %d orders", len(broker.placedOrders))
		}
	})

	t.Run("flattens after grace period expires", func(t *testing.T) {
		broker := &stubReconBroker{}
		router := NewRouter([]domain.Venue{domain.VenueBinanceFutures})
		ch, _ := router.Channel(domain.VenueBinanceFutures)
		go func() {
			for req := range ch {
				req.RespCh <- OrderResponse{BrokerOrderID: "flattened"}
			}
		}()
		r := NewReconciler(ReconcilerDeps{
			Venue:           domain.VenueBinanceFutures,
			Broker:          broker,
			Tracker:         NewBracketTracker(),
			Router:          router,
			Env:             domain.EnvironmentPaper,
			Positions:       func() []domain.Position { return []domain.Position{zeroProtPos("BTCUSDT")} },
			FlattenGraceSec: 30,
		})

		// Manually set firstSeen to the past
		r.firstSeen[domain.Symbol("BTCUSDT")] = time.Now().Add(-31 * time.Second)

		r.enforceBrackets(context.Background())
		// After grace period, it should reach the flatten path and route an order
		if len(broker.placedBrackets) != 0 {
			t.Errorf("expected no bracket after grace period, got %d", len(broker.placedBrackets))
		}
		// firstSeen must be cleared
		if _, ok := r.firstSeen[domain.Symbol("BTCUSDT")]; ok {
			t.Error("expected firstSeen to be cleared after flatten")
		}
	})

	t.Run("clears firstSeen when position gains protection", func(t *testing.T) {
		broker := &stubReconBroker{}
		r := NewReconciler(ReconcilerDeps{
			Venue:           domain.VenueBinanceFutures,
			Broker:          broker,
			Tracker:         NewBracketTracker(),
			Router:          nil,
			Env:             domain.EnvironmentPaper,
			Positions:       func() []domain.Position { return []domain.Position{zeroProtPos("BTCUSDT")} },
			FlattenGraceSec: 30,
		})

		// First call: record firstSeen
		r.enforceBrackets(context.Background())
		if _, ok := r.firstSeen[domain.Symbol("BTCUSDT")]; !ok {
			t.Fatal("expected firstSeen to be recorded")
		}

		// Now position has protection (SL/TP set)
		r.deps.Positions = func() []domain.Position { return []domain.Position{longPos("BTCUSDT")} }

		r.enforceBrackets(context.Background())
		if _, ok := r.firstSeen[domain.Symbol("BTCUSDT")]; ok {
			t.Error("expected firstSeen to be cleared when position has protection")
		}
	})
}

func TestReconciler_FlattenGracePeriod_ZeroMeansNoGrace(t *testing.T) {
	r := NewReconciler(ReconcilerDeps{
		Venue:      domain.VenueBinanceFutures,
		Broker:     &stubReconBroker{},
		Tracker:    NewBracketTracker(),
		Router:     NewRouter([]domain.Venue{domain.VenueBinanceFutures}),
		Env:        domain.EnvironmentPaper,
		Positions:  func() []domain.Position { return nil },
		IntervalMS: 0,
		// FlattenGraceSec not set (zero value) means no grace period
	})

	if r.deps.FlattenGraceSec != 0 {
		t.Errorf("expected FlattenGraceSec=0 when not set, got %d", r.deps.FlattenGraceSec)
	}
}

func TestReconciler_JobB_EnqueuesNonHoldDecision(t *testing.T) {
	broker := &stubReconBroker{}
	tracker := NewBracketTracker()
	tracker.Record("BTCUSDT", domain.BracketResponse{StopOrderID: "s", Symbol: "BTCUSDT"}) // protected, so Job A skips it
	router := NewRouter([]domain.Venue{domain.VenueBinanceFutures})

	decider := &stubDecider{action: domain.ManagedAction{Decision: domain.ActionClose, Reason: "bias flip"}}
	queue := NewActionQueue(60, true, func(context.Context, QueuedAction) error { return nil })
	detector := NewTriggerDetector(0, 0, 0, true)

	r := NewReconciler(ReconcilerDeps{
		Venue: domain.VenueBinanceFutures, Broker: broker, Tracker: tracker,
		Router: router, Env: domain.EnvironmentPaper,
		Positions: func() []domain.Position { return []domain.Position{longPos("BTCUSDT")} },
		Detector:  detector,
		Decider:   decider,
		Queue:     queue,
		Bias: func(domain.Symbol) (domain.BiasScore, bool) {
			return domain.BiasBearish, true // opposes the BUY position
		},
	})

	r.reviewPositions(context.Background())

	if decider.calls != 1 {
		t.Fatalf("expected decider consulted once, got %d", decider.calls)
	}
	if got := len(queue.Pending()); got != 1 {
		t.Fatalf("expected 1 queued action, got %d", got)
	}
}

func TestReconciler_JobB_HoldIsNotQueued(t *testing.T) {
	tracker := NewBracketTracker()
	tracker.Record("BTCUSDT", domain.BracketResponse{StopOrderID: "s", Symbol: "BTCUSDT"})
	decider := &stubDecider{action: domain.ManagedAction{Decision: domain.ActionHold}}
	queue := NewActionQueue(60, true, func(context.Context, QueuedAction) error { return nil })
	detector := NewTriggerDetector(0, 0, 0, true)

	r := NewReconciler(ReconcilerDeps{
		Venue: domain.VenueBinanceFutures, Broker: &stubReconBroker{}, Tracker: tracker,
		Router: NewRouter([]domain.Venue{domain.VenueBinanceFutures}), Env: domain.EnvironmentPaper,
		Positions: func() []domain.Position { return []domain.Position{longPos("BTCUSDT")} },
		Detector:  detector,
		Decider:   decider,
		Queue:     queue,
		Bias:      func(domain.Symbol) (domain.BiasScore, bool) { return domain.BiasBearish, true },
	})

	r.reviewPositions(context.Background())

	if got := len(queue.Pending()); got != 0 {
		t.Fatalf("HOLD must not be queued, got %d pending", got)
	}
}

func TestReconciler_JobB_NoopWhenUnwired(t *testing.T) {
	// No Detector/Decider/Queue → Job B is a no-op and must not panic.
	tracker := NewBracketTracker()
	tracker.Record("BTCUSDT", domain.BracketResponse{StopOrderID: "s", Symbol: "BTCUSDT"})
	r := NewReconciler(ReconcilerDeps{
		Venue: domain.VenueBinanceFutures, Broker: &stubReconBroker{}, Tracker: tracker,
		Router: NewRouter([]domain.Venue{domain.VenueBinanceFutures}), Env: domain.EnvironmentPaper,
		Positions: func() []domain.Position { return []domain.Position{longPos("BTCUSDT")} },
	})
	r.reviewPositions(context.Background()) // must not panic
}

func TestReconciler_JobB_ExternallyProtected_RoutesToPropose(t *testing.T) {
	pos := longPos("BTCUSDT")
	pos.ExternallyProtected = true
	pos.StopLoss = decimal.NewFromInt(60000)
	pos.TakeProfit1 = decimal.NewFromInt(70000)

	decider := &stubDecider{action: domain.ManagedAction{
		Decision: domain.ActionTightenStop, NewStopLoss: decimal.NewFromInt(62000), Reason: "lock profit"}}
	queue := NewActionQueue(60, true, func(context.Context, QueuedAction) error { return nil })
	detector := NewTriggerDetector(0, 0, 0, true)

	var proposed []domain.ManagedAction
	r := NewReconciler(ReconcilerDeps{
		Venue: domain.VenueBinanceFutures, Broker: &stubReconBroker{}, Tracker: NewBracketTracker(),
		Router: NewRouter([]domain.Venue{domain.VenueBinanceFutures}), Env: domain.EnvironmentPaper,
		Positions: func() []domain.Position { return []domain.Position{pos} },
		Detector:  detector, Decider: decider, Queue: queue,
		Bias: func(domain.Symbol) (domain.BiasScore, bool) { return domain.BiasBearish, true },
		Propose: func(p domain.Position, a domain.ManagedAction) {
			proposed = append(proposed, a)
		},
	})
	r.reviewPositions(context.Background())

	if len(proposed) != 1 {
		t.Fatalf("expected 1 proposal routed, got %d", len(proposed))
	}
	if !proposed[0].NewStopLoss.Equal(decimal.NewFromInt(62000)) {
		t.Fatalf("proposed stop = %s, want 62000", proposed[0].NewStopLoss)
	}
	if got := len(queue.Pending()); got != 0 {
		t.Fatalf("externally-protected adjustment must NOT enqueue; got %d queued", got)
	}
}

func TestEnforceBrackets_SkipsExternallyProtected(t *testing.T) {
	broker := &stubReconBroker{}
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

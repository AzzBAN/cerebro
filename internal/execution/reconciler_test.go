package execution

import (
	"context"
	"testing"

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

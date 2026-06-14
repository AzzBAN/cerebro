package execution

import (
	"context"
	"testing"

	"github.com/azhar/cerebro/internal/domain"
	"github.com/shopspring/decimal"
)

func makeExecItem(dec domain.ActionDecision, newStop ...decimal.Decimal) QueuedAction {
	action := makeAction(dec)
	if len(newStop) > 0 {
		action.NewStopLoss = newStop[0]
	}
	pos := makeQueuedPos("BTCUSDT")
	pos.TakeProfit1 = decimal.NewFromInt(115) // give TP so bracket has two legs
	return QueuedAction{
		ID:       "exec-test-id",
		Position: pos,
		Trigger:  domain.ReviewTrigger{Type: domain.TriggerProfitThreshold, Symbol: "BTCUSDT"},
		Action:   action,
	}
}

func TestActionExecutor_HoldIsNoop(t *testing.T) {
	broker := &stubReconBroker{}
	tracker := NewBracketTracker()
	router := NewRouter([]domain.Venue{domain.VenueBinanceFutures})
	ex := NewActionExecutor(ActionExecutorDeps{
		Venue: domain.VenueBinanceFutures, Broker: broker,
		Router: router, Tracker: tracker, Env: domain.EnvironmentPaper,
	})

	if err := ex.Execute(context.Background(), makeExecItem(domain.ActionHold)); err != nil {
		t.Fatalf("Execute(HOLD) error = %v", err)
	}
	if len(broker.placedBrackets) != 0 || len(broker.placedOrders) != 0 || len(broker.cancelled) != 0 {
		t.Error("expected no broker calls for HOLD")
	}
}

func TestActionExecutor_CloseRoutesReduceOnly(t *testing.T) {
	broker := &stubReconBroker{}
	tracker := NewBracketTracker()
	router := NewRouter([]domain.Venue{domain.VenueBinanceFutures})
	ch, _ := router.Channel(domain.VenueBinanceFutures)
	go func() {
		for req := range ch {
			req.RespCh <- OrderResponse{BrokerOrderID: "closed"}
		}
	}()
	ex := NewActionExecutor(ActionExecutorDeps{
		Venue: domain.VenueBinanceFutures, Broker: broker,
		Router: router, Tracker: tracker, Env: domain.EnvironmentPaper,
	})

	if err := ex.Execute(context.Background(), makeExecItem(domain.ActionClose)); err != nil {
		t.Fatalf("Execute(CLOSE) error = %v", err)
	}
	// order was routed through channel (drained above) — no direct broker.PlaceOrder call
	if len(broker.placedOrders) != 0 {
		t.Errorf("expected no direct broker orders, got %d", len(broker.placedOrders))
	}
}

func TestActionExecutor_TightenStop_CancelsThenPlaces(t *testing.T) {
	broker := &stubReconBroker{}
	tracker := NewBracketTracker()
	tracker.Record("BTCUSDT", domain.BracketResponse{StopOrderID: "old-stop", Symbol: "BTCUSDT"})
	router := NewRouter([]domain.Venue{domain.VenueBinanceFutures})
	ex := NewActionExecutor(ActionExecutorDeps{
		Venue: domain.VenueBinanceFutures, Broker: broker,
		Router: router, Tracker: tracker, Env: domain.EnvironmentPaper,
	})

	item := makeExecItem(domain.ActionTightenStop, decimal.NewFromInt(98))
	if err := ex.Execute(context.Background(), item); err != nil {
		t.Fatalf("Execute(TIGHTEN_STOP) error = %v", err)
	}
	if len(broker.cancelled) != 1 {
		t.Errorf("expected 1 bracket cancelled, got %d", len(broker.cancelled))
	}
	if len(broker.placedBrackets) != 1 {
		t.Errorf("expected 1 new bracket placed, got %d", len(broker.placedBrackets))
	}
	if !tracker.Has("BTCUSDT") {
		t.Error("expected tracker to record the new bracket")
	}
}

func TestActionExecutor_TightenStop_NoExistingBracket(t *testing.T) {
	broker := &stubReconBroker{}
	tracker := NewBracketTracker()
	router := NewRouter([]domain.Venue{domain.VenueBinanceFutures})
	ex := NewActionExecutor(ActionExecutorDeps{
		Venue: domain.VenueBinanceFutures, Broker: broker,
		Router: router, Tracker: tracker, Env: domain.EnvironmentPaper,
	})

	item := makeExecItem(domain.ActionTightenStop, decimal.NewFromInt(98))
	if err := ex.Execute(context.Background(), item); err != nil {
		t.Fatalf("Execute(TIGHTEN_STOP, no prior bracket) error = %v", err)
	}
	if len(broker.cancelled) != 0 {
		t.Errorf("expected no cancel when no prior bracket, got %d", len(broker.cancelled))
	}
	if len(broker.placedBrackets) != 1 {
		t.Errorf("expected 1 new bracket placed, got %d", len(broker.placedBrackets))
	}
}

func TestActionExecutor_FlipClosesThenReenters(t *testing.T) {
	broker := &stubReconBroker{}
	tracker := NewBracketTracker()
	router := NewRouter([]domain.Venue{domain.VenueBinanceFutures})
	ch, _ := router.Channel(domain.VenueBinanceFutures)
	go func() {
		for req := range ch {
			req.RespCh <- OrderResponse{BrokerOrderID: "flipped"}
		}
	}()

	var entries []domain.Position
	ex := NewActionExecutor(ActionExecutorDeps{
		Venue: domain.VenueBinanceFutures, Broker: broker,
		Router: router, Tracker: tracker, Env: domain.EnvironmentPaper,
		EntryFn: func(_ context.Context, want domain.Position) error {
			entries = append(entries, want)
			return nil
		},
	})

	// makeExecItem builds a long (BUY) position; flip should re-enter SHORT.
	if err := ex.Execute(context.Background(), makeExecItem(domain.ActionFlip)); err != nil {
		t.Fatalf("Execute(FLIP) error = %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected exactly one re-entry, got %d", len(entries))
	}
	if entries[0].Side != domain.SideSell {
		t.Errorf("flip of a long should re-enter SELL, got %s", entries[0].Side)
	}
}

func TestActionExecutor_FlipReentryRejectedLeavesFlat(t *testing.T) {
	broker := &stubReconBroker{}
	tracker := NewBracketTracker()
	router := NewRouter([]domain.Venue{domain.VenueBinanceFutures})
	ch, _ := router.Channel(domain.VenueBinanceFutures)
	go func() {
		for req := range ch {
			req.RespCh <- OrderResponse{BrokerOrderID: "closed"}
		}
	}()

	ex := NewActionExecutor(ActionExecutorDeps{
		Venue: domain.VenueBinanceFutures, Broker: broker,
		Router: router, Tracker: tracker, Env: domain.EnvironmentPaper,
		EntryFn: func(_ context.Context, _ domain.Position) error {
			return context.DeadlineExceeded // simulate gate rejection
		},
	})

	// A rejected re-entry must NOT propagate an error — the protective close
	// already succeeded and the position is safely flat.
	if err := ex.Execute(context.Background(), makeExecItem(domain.ActionFlip)); err != nil {
		t.Fatalf("flip with rejected re-entry should return nil, got %v", err)
	}
}

func TestActionExecutor_FlipNoEntryFnDegradesToClose(t *testing.T) {
	broker := &stubReconBroker{}
	tracker := NewBracketTracker()
	router := NewRouter([]domain.Venue{domain.VenueBinanceFutures})
	ch, _ := router.Channel(domain.VenueBinanceFutures)
	go func() {
		for req := range ch {
			req.RespCh <- OrderResponse{BrokerOrderID: "closed"}
		}
	}()
	ex := NewActionExecutor(ActionExecutorDeps{
		Venue: domain.VenueBinanceFutures, Broker: broker,
		Router: router, Tracker: tracker, Env: domain.EnvironmentPaper,
		// EntryFn nil → flip degrades to a plain close.
	})

	if err := ex.Execute(context.Background(), makeExecItem(domain.ActionFlip)); err != nil {
		t.Fatalf("Execute(FLIP, no EntryFn) error = %v", err)
	}
	if len(broker.placedOrders) != 0 {
		t.Errorf("expected no direct broker orders for FLIP, got %d", len(broker.placedOrders))
	}
}

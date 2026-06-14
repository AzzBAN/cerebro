package execution

import (
	"context"
	"testing"
	"time"

	"github.com/azhar/cerebro/internal/domain"
	"github.com/shopspring/decimal"
)

func makeQueuedPos(sym string) domain.Position {
	return domain.Position{
		Symbol:       domain.Symbol(sym),
		Venue:        domain.VenueBinanceFutures,
		Side:         domain.SideBuy,
		EntryPrice:   decimal.NewFromInt(100),
		CurrentPrice: decimal.NewFromInt(110),
		Quantity:     decimal.NewFromInt(1),
	}
}

func makeAction(dec domain.ActionDecision) domain.ManagedAction {
	return domain.ManagedAction{
		Decision:   dec,
		Reason:     "test reason",
		Confidence: 0.8,
	}
}

func TestActionQueue_EnqueueCreatesPendingItem(t *testing.T) {
	q := NewActionQueue(30, false, func(_ context.Context, _ QueuedAction) error { return nil })
	pos := makeQueuedPos("BTCUSDT")
	trigger := domain.ReviewTrigger{Type: domain.TriggerProfitThreshold, Symbol: "BTCUSDT"}
	action := makeAction(domain.ActionClose)

	id := q.Enqueue(pos, trigger, action)
	if id == "" {
		t.Fatal("Enqueue returned empty ID")
	}

	pending := q.Pending()
	if len(pending) != 1 {
		t.Fatalf("expected 1 pending item, got %d", len(pending))
	}
	if pending[0].ID != id {
		t.Errorf("pending item ID = %q, want %q", pending[0].ID, id)
	}
	if pending[0].Status != StatusPending {
		t.Errorf("status = %q, want pending", pending[0].Status)
	}
}

func TestActionQueue_GuardDropsWhenPositionGone(t *testing.T) {
	executed := 0
	q := NewActionQueue(30, false, func(_ context.Context, _ QueuedAction) error {
		executed++
		return nil
	})
	q.SetPositionExists(func(domain.Symbol) bool { return false }) // position vanished

	id := q.Enqueue(makeQueuedPos("BTCUSDT"),
		domain.ReviewTrigger{Type: domain.TriggerProfitThreshold, Symbol: "BTCUSDT"},
		makeAction(domain.ActionClose))

	if err := q.Confirm(context.Background(), id); err != nil {
		t.Fatalf("Confirm returned error: %v", err)
	}
	if executed != 0 {
		t.Errorf("expected guard to drop execution, but execute ran %d times", executed)
	}
}

func TestActionQueue_GuardAllowsWhenPositionPresent(t *testing.T) {
	executed := 0
	q := NewActionQueue(30, false, func(_ context.Context, _ QueuedAction) error {
		executed++
		return nil
	})
	q.SetPositionExists(func(domain.Symbol) bool { return true })

	id := q.Enqueue(makeQueuedPos("BTCUSDT"),
		domain.ReviewTrigger{Type: domain.TriggerProfitThreshold, Symbol: "BTCUSDT"},
		makeAction(domain.ActionClose))

	if err := q.Confirm(context.Background(), id); err != nil {
		t.Fatalf("Confirm returned error: %v", err)
	}
	if executed != 1 {
		t.Errorf("expected execution to run once, got %d", executed)
	}
}

func TestActionQueue_Confirm_ExecutesItem(t *testing.T) {
	executed := 0
	q := NewActionQueue(30, false, func(_ context.Context, _ QueuedAction) error {
		executed++
		return nil
	})
	pos := makeQueuedPos("BTCUSDT")
	trigger := domain.ReviewTrigger{Type: domain.TriggerProfitThreshold, Symbol: "BTCUSDT"}
	id := q.Enqueue(pos, trigger, makeAction(domain.ActionClose))

	if err := q.Confirm(context.Background(), id); err != nil {
		t.Fatalf("Confirm() error = %v", err)
	}
	if executed != 1 {
		t.Errorf("expected execute called once, got %d", executed)
	}
	if len(q.Pending()) != 0 {
		t.Error("expected item removed from pending after confirm")
	}
}

func TestActionQueue_Reject_DoesNotExecute(t *testing.T) {
	executed := 0
	q := NewActionQueue(30, false, func(_ context.Context, _ QueuedAction) error {
		executed++
		return nil
	})
	pos := makeQueuedPos("BTCUSDT")
	trigger := domain.ReviewTrigger{Type: domain.TriggerProfitThreshold, Symbol: "BTCUSDT"}
	id := q.Enqueue(pos, trigger, makeAction(domain.ActionClose))

	if err := q.Reject(id); err != nil {
		t.Fatalf("Reject() error = %v", err)
	}
	if executed != 0 {
		t.Errorf("expected execute NOT called on reject, got %d", executed)
	}
	if len(q.Pending()) != 0 {
		t.Error("expected item removed from pending after reject")
	}
}

func TestActionQueue_Confirm_UnknownIDReturnsError(t *testing.T) {
	q := NewActionQueue(30, false, func(_ context.Context, _ QueuedAction) error { return nil })
	err := q.Confirm(context.Background(), "nonexistent")
	if err == nil {
		t.Error("expected error for unknown ID, got nil")
	}
}

func TestActionQueue_Reject_UnknownIDReturnsError(t *testing.T) {
	q := NewActionQueue(30, false, func(_ context.Context, _ QueuedAction) error { return nil })
	err := q.Reject("nonexistent")
	if err == nil {
		t.Error("expected error for unknown ID, got nil")
	}
}

func TestActionQueue_Tick_AutonomousOnTimeout_Executes(t *testing.T) {
	executed := 0
	q := NewActionQueue(1, true, func(_ context.Context, _ QueuedAction) error {
		executed++
		return nil
	})
	pos := makeQueuedPos("BTCUSDT")
	trigger := domain.ReviewTrigger{Type: domain.TriggerProfitThreshold, Symbol: "BTCUSDT"}
	q.Enqueue(pos, trigger, makeAction(domain.ActionClose))

	// Backdate the expiry so the item is already expired.
	q.mu.Lock()
	for id := range q.items {
		q.items[id].ExpiresAt = time.Now().Add(-1 * time.Second)
	}
	q.mu.Unlock()

	q.Tick(context.Background())

	if executed != 1 {
		t.Errorf("expected autonomous execution on timeout, got %d", executed)
	}
	if len(q.Pending()) != 0 {
		t.Error("expected item removed after timeout execution")
	}
}

func TestActionQueue_Tick_NoAutonomous_DropOnTimeout(t *testing.T) {
	executed := 0
	q := NewActionQueue(1, false, func(_ context.Context, _ QueuedAction) error {
		executed++
		return nil
	})
	pos := makeQueuedPos("BTCUSDT")
	trigger := domain.ReviewTrigger{Type: domain.TriggerProfitThreshold, Symbol: "BTCUSDT"}
	q.Enqueue(pos, trigger, makeAction(domain.ActionClose))

	q.mu.Lock()
	for id := range q.items {
		q.items[id].ExpiresAt = time.Now().Add(-1 * time.Second)
	}
	q.mu.Unlock()

	q.Tick(context.Background())

	if executed != 0 {
		t.Errorf("expected no execution on timeout when autonomous=false, got %d", executed)
	}
	if len(q.Pending()) != 0 {
		t.Error("expected item discarded after timeout")
	}
}

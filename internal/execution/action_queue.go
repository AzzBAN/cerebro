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

// QueuedActionStatus tracks the lifecycle state of a queued action.
type QueuedActionStatus string

const (
	StatusPending   QueuedActionStatus = "pending"
	StatusConfirmed QueuedActionStatus = "confirmed"
	StatusRejected  QueuedActionStatus = "rejected"
	StatusTimedOut  QueuedActionStatus = "timed_out"
	StatusExecuted  QueuedActionStatus = "executed"
)

// QueuedAction is a pending ManagedAction awaiting operator confirmation
// (live) or autonomous execution (paper/demo).
type QueuedAction struct {
	ID         string
	Position   domain.Position
	Trigger    domain.ReviewTrigger
	Action     domain.ManagedAction
	EnqueuedAt time.Time
	ExpiresAt  time.Time
	Status     QueuedActionStatus
}

// ExecuteFunc is called when an action is confirmed or executed autonomously on timeout.
type ExecuteFunc func(context.Context, QueuedAction) error

// ActionQueue holds pending ManagedActions. In live mode items wait for
// operator confirmation; callers in paper/demo mode may bypass the queue and
// call ExecuteFunc directly. Tick must be called periodically to expire items.
//
// Safe for concurrent use.
type ActionQueue struct {
	mu                  sync.Mutex
	items               map[string]*QueuedAction
	confirmTimeoutSec   int
	autonomousOnTimeout bool
	execute             ExecuteFunc
}

// NewActionQueue creates an ActionQueue.
//   - confirmTimeoutSec: seconds before a pending item times out.
//   - autonomousOnTimeout: when true, expired items are executed autonomously;
//     when false they are discarded (fail safe).
//   - execute: invoked on confirm or autonomous timeout.
func NewActionQueue(confirmTimeoutSec int, autonomousOnTimeout bool, execute ExecuteFunc) *ActionQueue {
	return &ActionQueue{
		items:               make(map[string]*QueuedAction),
		confirmTimeoutSec:   confirmTimeoutSec,
		autonomousOnTimeout: autonomousOnTimeout,
		execute:             execute,
	}
}

// Enqueue adds a pending action and returns its ID.
func (q *ActionQueue) Enqueue(pos domain.Position, trigger domain.ReviewTrigger, action domain.ManagedAction) string {
	id := uuid.New().String()
	now := time.Now().UTC()
	item := &QueuedAction{
		ID:         id,
		Position:   pos,
		Trigger:    trigger,
		Action:     action,
		EnqueuedAt: now,
		ExpiresAt:  now.Add(time.Duration(q.confirmTimeoutSec) * time.Second),
		Status:     StatusPending,
	}
	q.mu.Lock()
	q.items[id] = item
	q.mu.Unlock()
	slog.Info("action_queue: enqueued",
		"id", id, "symbol", pos.Symbol, "action", action.Decision,
		"expires_at", item.ExpiresAt)
	return id
}

// Confirm marks the item confirmed and executes it synchronously.
// Returns an error if the ID is unknown or the item is no longer pending.
func (q *ActionQueue) Confirm(ctx context.Context, id string) error {
	q.mu.Lock()
	item, ok := q.items[id]
	if !ok {
		q.mu.Unlock()
		return fmt.Errorf("action_queue: unknown item %q", id)
	}
	if item.Status != StatusPending {
		q.mu.Unlock()
		return fmt.Errorf("action_queue: item %q is not pending (status=%s)", id, item.Status)
	}
	item.Status = StatusConfirmed
	snapshot := *item
	delete(q.items, id)
	q.mu.Unlock()

	slog.Info("action_queue: confirmed", "id", id, "symbol", snapshot.Position.Symbol)
	if err := q.execute(ctx, snapshot); err != nil {
		return fmt.Errorf("action_queue: execute confirmed %q: %w", id, err)
	}
	return nil
}

// Reject removes the item without executing. Returns an error if unknown.
func (q *ActionQueue) Reject(id string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	item, ok := q.items[id]
	if !ok {
		return fmt.Errorf("action_queue: unknown item %q", id)
	}
	if item.Status != StatusPending {
		return fmt.Errorf("action_queue: item %q is not pending (status=%s)", id, item.Status)
	}
	slog.Info("action_queue: rejected", "id", id, "symbol", item.Position.Symbol)
	delete(q.items, id)
	return nil
}

// Tick processes expired pending items. Call from a goroutine on a regular
// cadence (e.g. every second). Expired items are either executed autonomously
// or discarded depending on the autonomousOnTimeout flag.
func (q *ActionQueue) Tick(ctx context.Context) {
	now := time.Now().UTC()
	q.mu.Lock()
	var expired []*QueuedAction
	for _, item := range q.items {
		if item.Status == StatusPending && now.After(item.ExpiresAt) {
			item.Status = StatusTimedOut
			expired = append(expired, item)
		}
	}
	for _, item := range expired {
		delete(q.items, item.ID)
	}
	q.mu.Unlock()

	for _, item := range expired {
		if q.autonomousOnTimeout {
			slog.Info("action_queue: autonomous execution on timeout",
				"id", item.ID, "symbol", item.Position.Symbol)
			if err := q.execute(ctx, *item); err != nil {
				slog.Error("action_queue: autonomous execute failed",
					"id", item.ID, "symbol", item.Position.Symbol, "error", err)
			}
		} else {
			slog.Info("action_queue: discarded on timeout (autonomous=false)",
				"id", item.ID, "symbol", item.Position.Symbol)
		}
	}
}

// Pending returns a snapshot of all currently pending items.
func (q *ActionQueue) Pending() []*QueuedAction {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := make([]*QueuedAction, 0, len(q.items))
	for _, item := range q.items {
		if item.Status == StatusPending {
			cp := *item
			out = append(out, &cp)
		}
	}
	return out
}

package execution

import (
	"context"
	"log/slog"
	"time"

	"github.com/azhar/cerebro/internal/domain"
	"github.com/azhar/cerebro/internal/port"
	"github.com/google/uuid"
)

// OrderRequest is sent to the execution worker channel.
type OrderRequest struct {
	Intent domain.OrderIntent
	RespCh chan<- OrderResponse
}

// OrderResponse is the result returned to the sender.
type OrderResponse struct {
	BrokerOrderID string
	Err           error
}

// Worker serialises order submissions for a single venue.
// One goroutine per broker — this is the "one writer per venue" invariant.
type Worker struct {
	venue   domain.Venue
	broker  port.Broker
	store   port.TradeStore
	audit   port.AuditStore
	cache   port.Cache
	inputCh <-chan OrderRequest
}

// NewWorker creates a Worker. Call Run in a goroutine.
func NewWorker(
	venue domain.Venue,
	broker port.Broker,
	store port.TradeStore,
	audit port.AuditStore,
	cache port.Cache,
	inputCh <-chan OrderRequest,
) *Worker {
	return &Worker{
		venue:   venue,
		broker:  broker,
		store:   store,
		audit:   audit,
		cache:   cache,
		inputCh: inputCh,
	}
}

// Run processes order requests sequentially until ctx is cancelled.
func (w *Worker) Run(ctx context.Context) error {
	slog.Info("execution worker started", "venue", w.venue)
	for {
		select {
		case <-ctx.Done():
			slog.Info("execution worker stopping", "venue", w.venue)
			return nil
		case req, ok := <-w.inputCh:
			if !ok {
				return nil
			}
			resp := w.process(ctx, req.Intent)
			if req.RespCh != nil {
				select {
				case req.RespCh <- resp:
				case <-ctx.Done():
				}
			}
		}
	}
}

func (w *Worker) process(ctx context.Context, intent domain.OrderIntent) OrderResponse {
	log := slog.With("intent_id", intent.ID, "symbol", intent.Symbol, "side", intent.Side)

	// 1. Idempotency check.
	fresh, err := DeduplicateOrder(ctx, w.cache, intent.ID)
	if err != nil {
		log.Error("idempotency check failed", "error", err)
		return OrderResponse{Err: err}
	}
	if !fresh {
		log.Warn("duplicate order intent detected; skipping")
		return OrderResponse{Err: domain.ErrDuplicateSignal}
	}

	// 2. Persist intent as pending BEFORE submission (fail-safe ordering).
	if err := w.store.SaveIntent(ctx, intent); err != nil {
		log.Error("save intent failed", "error", err)
		return OrderResponse{Err: err}
	}

	// 3. Submit to broker.
	brokerID, err := w.broker.PlaceOrder(ctx, intent)
	if err != nil {
		log.Error("place order failed", "error", err)
		_ = w.store.UpdateIntentStatus(ctx, intent.ID, domain.OrderStatusRejected, "")
		_ = w.saveAudit(ctx, "order_rejected", map[string]any{
			"intent_id": intent.ID,
			"error":     err.Error(),
		})
		return OrderResponse{Err: err}
	}

	// 4. Update intent to submitted.
	if err := w.store.UpdateIntentStatus(ctx, intent.ID, domain.OrderStatusSubmitted, brokerID); err != nil {
		log.Error("update intent status failed", "error", err)
	}

	log.Info("order submitted", "broker_order_id", brokerID)
	_ = w.saveAudit(ctx, "order_submitted", map[string]any{
		"intent_id":       intent.ID,
		"broker_order_id": brokerID,
		"symbol":          string(intent.Symbol),
		"side":            string(intent.Side),
		"quantity":        intent.Quantity.String(),
	})

	return OrderResponse{BrokerOrderID: brokerID}
}

func (w *Worker) saveAudit(ctx context.Context, evtType string, payload map[string]any) error {
	return w.audit.SaveEvent(ctx, domain.AuditEvent{
		ID:        uuid.New().String(),
		EventType: evtType,
		Actor:     "system",
		Payload:   payload,
		CreatedAt: time.Now().UTC(),
	})
}

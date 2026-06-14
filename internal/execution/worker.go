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
	// Bracket is populated when the intent carried StopLoss / TakeProfit1
	// and the post-entry bracket placement succeeded. An empty Bracket with
	// a nil Err means the entry went through but no bracket was requested.
	Bracket domain.BracketResponse
	// BracketErr, when non-nil, signals that the entry order submitted but
	// the protective bracket did not. Operators must treat this as an
	// unprotected position: either retry bracket placement, fall back to
	// the client-side Monitor, or flatten the position immediately.
	BracketErr error
	Err        error
}

// Worker serialises order submissions for a single venue.
// One goroutine per broker — this is the "one writer per venue" invariant.
type Worker struct {
	venue   domain.Venue
	broker  port.Broker
	store   port.TradeStore
	audit   port.AuditStore
	cache   port.Cache
	tracker *BracketTracker
	inputCh <-chan OrderRequest
}

// NewWorker creates a Worker. Call Run in a goroutine.
func NewWorker(
	venue domain.Venue,
	broker port.Broker,
	store port.TradeStore,
	audit port.AuditStore,
	cache port.Cache,
	tracker *BracketTracker,
	inputCh <-chan OrderRequest,
) *Worker {
	return &Worker{
		venue:   venue,
		broker:  broker,
		store:   store,
		audit:   audit,
		cache:   cache,
		tracker: tracker,
		inputCh: inputCh,
	}
}

// Run processes order requests sequentially until ctx is cancelled.
func (w *Worker) Run(ctx context.Context) error {
	slog.Info("execution worker started", "venue", w.venue)
	// Always emit a "stopping" message so log analysis can pair Run-entry
	// with Run-exit. Previously the input-channel-closed path returned
	// silently, producing a `started > stopping` mismatch in production
	// logs (~6 leaked terminations across 126 sessions).
	defer slog.Info("execution worker stopping", "venue", w.venue)
	for {
		select {
		case <-ctx.Done():
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
		"order_type":      string(intent.OrderTypeOrDefault()),
	})

	// 5. Attach protective bracket when the intent requested one.
	//
	// This runs sequentially inside the per-venue worker goroutine so we
	// preserve the single-writer invariant. On spot/live the bracket is a
	// Binance OCO attached to the position; on futures it's a pair of
	// STOP_MARKET + TAKE_PROFIT_MARKET algo orders. Paper brokers simulate
	// OCO-style triggers against future candles.
	//
	// For MARKET entries the position is effectively open the moment the
	// broker accepts the order, so we can place the bracket immediately.
	// For LIMIT / STOP_LIMIT entries the fill may be delayed; Phase D.2
	// will move bracket placement onto the user-data fill event. For now,
	// the safety net is the client-side Monitor, which still checks
	// intent.StopLoss each tick.
	if !intent.HasBracket() {
		return OrderResponse{BrokerOrderID: brokerID}
	}
	if intent.OrderTypeOrDefault() != domain.OrderTypeMarket {
		log.Debug("bracket placement deferred for non-market entry; Monitor acts as fallback",
			"order_type", intent.OrderType)
		return OrderResponse{BrokerOrderID: brokerID}
	}

	bracketReq := domain.BracketRequest{
		ParentIntentID: intent.ID,
		CorrelationID:  intent.CorrelationID,
		Symbol:         intent.Symbol,
		Venue:          intent.Venue,
		Side:           intent.Side,
		Quantity:       intent.Quantity,
		StopLoss:       intent.StopLoss,
		TakeProfit:     intent.TakeProfit1,
		ScaleOutPct:    intent.ScaleOutPct,
		TIF:            intent.TIF,
		PositionSide:   intent.PositionSide,
	}
	bracket, berr := w.broker.PlaceBracket(ctx, bracketReq)
	if berr != nil {
		log.Error("bracket placement failed; position may be unprotected",
			"error", berr,
			"stop", intent.StopLoss.String(), "tp", intent.TakeProfit1.String())
		_ = w.saveAudit(ctx, "bracket_failed", map[string]any{
			"intent_id":       intent.ID,
			"broker_order_id": brokerID,
			"symbol":          string(intent.Symbol),
			"error":           berr.Error(),
		})
		return OrderResponse{
			BrokerOrderID: brokerID,
			Bracket:       bracket, // may be partial (stop OK, TP failed)
			BracketErr:    berr,
		}
	}

	log.Info("bracket attached",
		"stop_order_id", bracket.StopOrderID,
		"tp_order_id", bracket.TakeProfitOrderID,
		"list_id", bracket.ListID,
	)
	_ = w.saveAudit(ctx, "bracket_attached", map[string]any{
		"intent_id":         intent.ID,
		"stop_order_id":     bracket.StopOrderID,
		"tp_order_id":       bracket.TakeProfitOrderID,
		"list_id":           bracket.ListID,
		"symbol":            string(intent.Symbol),
		"stop_price":        intent.StopLoss.String(),
		"take_profit_price": intent.TakeProfit1.String(),
	})

	if w.tracker != nil {
		w.tracker.Record(intent.Symbol, bracket)
	}

	return OrderResponse{
		BrokerOrderID: brokerID,
		Bracket:       bracket,
	}
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

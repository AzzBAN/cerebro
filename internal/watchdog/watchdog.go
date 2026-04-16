package watchdog

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/azhar/cerebro/internal/domain"
	"github.com/azhar/cerebro/internal/port"
	"github.com/google/uuid"
)

// Watchdog runs once at startup — before any WebSocket is opened — to reconcile
// the broker's live open positions against Cerebro's local Redis state.
// Any mismatch is logged to audit_events and the discrepant position is handed
// to the Order Monitor before normal trading resumes.
type Watchdog struct {
	brokers []port.Broker
	cache   port.Cache
	audit   port.AuditStore
}

// New creates a Watchdog.
func New(brokers []port.Broker, cache port.Cache, audit port.AuditStore) *Watchdog {
	return &Watchdog{brokers: brokers, cache: cache, audit: audit}
}

// Reconcile performs the startup state reconciliation.
// Returns an error only if the broker API is unreachable and the operator
// has not passed --skip-reconcile.
func (w *Watchdog) Reconcile(ctx context.Context) error {
	slog.Info("watchdog: starting startup reconciliation")

	for _, broker := range w.brokers {
		if err := w.reconcileVenue(ctx, broker); err != nil {
			return fmt.Errorf("watchdog: venue %s: %w", broker.Venue(), err)
		}
	}

	slog.Info("watchdog: reconciliation complete")
	return nil
}

func (w *Watchdog) reconcileVenue(ctx context.Context, broker port.Broker) error {
	venue := broker.Venue()

	// Fetch broker truth.
	brokerPositions, err := broker.Positions(ctx)
	if err != nil {
		return fmt.Errorf("fetch broker positions: %w", err)
	}

	// Fetch local Redis state.
	pattern := fmt.Sprintf("open_position:%s:*", venue)
	keys, err := w.cache.Keys(ctx, pattern)
	if err != nil {
		return fmt.Errorf("fetch redis positions: %w", err)
	}

	localPositions := make(map[domain.Symbol]domain.Position)
	for _, key := range keys {
		b, err := w.cache.Get(ctx, key)
		if err != nil || b == nil {
			continue
		}
		var p domain.Position
		if err := json.Unmarshal(b, &p); err == nil {
			localPositions[p.Symbol] = p
		}
	}

	// Build broker position map.
	brokerMap := make(map[domain.Symbol]domain.Position, len(brokerPositions))
	for _, p := range brokerPositions {
		brokerMap[p.Symbol] = p
	}

	// Detect mismatches.
	mismatches := 0
	for sym, local := range localPositions {
		brokerPos, ok := brokerMap[sym]
		if !ok {
			// Position in Redis but NOT at broker — likely closed externally.
			slog.Warn("watchdog: position in Redis but not at broker; removing",
				"symbol", sym, "venue", venue)
			_ = w.cache.Delete(ctx, positionKey(venue, sym))
			w.logMismatch(ctx, "position_vanished", venue, sym, &local, nil)
			mismatches++
			continue
		}

		if !local.Quantity.Equal(brokerPos.Quantity) {
			slog.Warn("watchdog: quantity mismatch",
				"symbol", sym, "venue", venue,
				"local_qty", local.Quantity, "broker_qty", brokerPos.Quantity)
			// Reconcile Redis to broker truth.
			brokerPos.Strategy = local.Strategy
			brokerPos.CorrelationID = local.CorrelationID
			w.upsertRedis(ctx, brokerPos)
			w.logMismatch(ctx, "quantity_mismatch", venue, sym, &local, &brokerPos)
			mismatches++
		}
	}

	// Positions at broker but NOT in Redis — orphaned.
	for sym, brokerPos := range brokerMap {
		if _, ok := localPositions[sym]; !ok {
			slog.Warn("watchdog: orphaned position at broker; importing to Redis",
				"symbol", sym, "venue", venue, "qty", brokerPos.Quantity)
			w.upsertRedis(ctx, brokerPos)
			w.logMismatch(ctx, "orphaned_position", venue, sym, nil, &brokerPos)
			mismatches++
		}
	}

	if mismatches > 0 {
		slog.Warn("watchdog: reconciliation found mismatches",
			"venue", venue, "count", mismatches)
	} else {
		slog.Info("watchdog: venue state consistent", "venue", venue)
	}
	return nil
}

func (w *Watchdog) upsertRedis(ctx context.Context, p domain.Position) {
	b, err := json.Marshal(p)
	if err != nil {
		slog.Error("watchdog: marshal position", "error", err)
		return
	}
	if err := w.cache.Set(ctx, positionKey(p.Venue, p.Symbol), b, 0); err != nil {
		slog.Error("watchdog: write position to Redis", "error", err)
	}
}

func (w *Watchdog) logMismatch(ctx context.Context, evtType string, venue domain.Venue, sym domain.Symbol, local, broker *domain.Position) {
	payload := map[string]any{
		"venue":  string(venue),
		"symbol": string(sym),
	}
	if local != nil {
		payload["local_qty"] = local.Quantity.String()
	}
	if broker != nil {
		payload["broker_qty"] = broker.Quantity.String()
	}
	_ = w.audit.SaveEvent(ctx, domain.AuditEvent{
		ID:        uuid.New().String(),
		EventType: evtType,
		Actor:     "watchdog",
		Payload:   payload,
		CreatedAt: time.Now().UTC(),
	})
}

func positionKey(venue domain.Venue, symbol domain.Symbol) string {
	return fmt.Sprintf("open_position:%s:%s", venue, symbol)
}

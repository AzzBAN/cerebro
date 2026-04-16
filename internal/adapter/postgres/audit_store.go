package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/azhar/cerebro/internal/domain"
	"github.com/jackc/pgx/v5/pgxpool"
)

// AuditStore implements port.AuditStore using pgx.
type AuditStore struct {
	pool *pgxpool.Pool
}

// NewAuditStore creates an AuditStore from an existing pool.
func NewAuditStore(pool *pgxpool.Pool) *AuditStore {
	return &AuditStore{pool: pool}
}

// SaveEvent persists an operator command or system state change.
func (s *AuditStore) SaveEvent(ctx context.Context, e domain.AuditEvent) error {
	var payloadJSON []byte
	if e.Payload != nil {
		var err error
		payloadJSON, err = json.Marshal(e.Payload)
		if err != nil {
			return fmt.Errorf("postgres: marshal audit payload: %w", err)
		}
	}

	var createdAt time.Time
	if t, ok := e.CreatedAt.(time.Time); ok {
		createdAt = t
	} else {
		createdAt = time.Now().UTC()
	}

	_, err := s.pool.Exec(ctx, `
		INSERT INTO audit_events (id, event_type, actor, payload, created_at)
		VALUES ($1,$2,$3,$4,$5)
		ON CONFLICT (id) DO NOTHING`,
		e.ID, e.EventType, e.Actor, payloadJSON, createdAt,
	)
	if err != nil {
		return fmt.Errorf("postgres: save audit event %s: %w", e.ID, err)
	}
	return nil
}

package chatops

import (
	"context"
	"time"

	"github.com/azhar/cerebro/internal/domain"
	"github.com/azhar/cerebro/internal/port"
	"github.com/google/uuid"
)

// logCommand writes every dispatched command to the audit trail.
func logCommand(ctx context.Context, store port.AuditStore, actor, cmd, arg, result string) {
	_ = store.SaveEvent(ctx, domain.AuditEvent{
		ID:        uuid.New().String(),
		EventType: "command",
		Actor:     actor,
		Payload: map[string]any{
			"command": cmd,
			"arg":     arg,
			"result":  result,
		},
		CreatedAt: time.Now().UTC(),
	})
}

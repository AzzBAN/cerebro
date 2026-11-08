package port

import (
	"context"
	"time"

	"github.com/azhar/cerebro/internal/domain"
)

// TradeStore persists order intents and filled trades.
type TradeStore interface {
	SaveIntent(ctx context.Context, i domain.OrderIntent) error
	UpdateIntentStatus(ctx context.Context, id string, status domain.OrderStatus, brokerID string) error
	SaveTrade(ctx context.Context, t domain.Trade) error
	TradesByWindow(ctx context.Context, from, to time.Time) ([]domain.Trade, error)
	// IntentsByWindow returns every order intent whose created_at falls in
	// [from, to). Returned newest-first so the dashboard can render the
	// most recent activity at the top. Used by the order history view to
	// show pending/submitted/filled/rejected/cancelled intent lifecycle
	// alongside the closed-trade list. When the underlying store has no
	// intent log (e.g. legacy backends) it returns nil, nil.
	IntentsByWindow(ctx context.Context, from, to time.Time) ([]domain.OrderIntent, error)
}

// TradeSink receives a push notification every time a fill is recorded. The
// composition root wires the web dashboard as a sink so it can broadcast a
// "trade" frame to connected clients without polling. The push is best-effort
// and never blocks the caller; sinks must not perform I/O on the hot path.
type TradeSink interface {
	SendTrade(t domain.Trade)
}

// AgentLogStore persists agent run records and conversation messages.
type AgentLogStore interface {
	SaveRun(ctx context.Context, r domain.AgentRun) error
	RunsByWindow(ctx context.Context, agent string, from, to time.Time) ([]domain.AgentRun, error)
	SaveMessage(ctx context.Context, m domain.AgentMessage) error
}

// AuditStore persists operator commands and system state changes.
type AuditStore interface {
	SaveEvent(ctx context.Context, e domain.AuditEvent) error
}

// LogArchiver moves old records to archive tables and purges them from hot tables.
type LogArchiver interface {
	ArchiveAndPurge(ctx context.Context, agentLogsDays, auditEventsDays int) (archived, purged int, err error)
}

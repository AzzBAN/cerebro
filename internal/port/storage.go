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

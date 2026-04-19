package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// LogArchiver implements port.LogArchiver using pgx.
type LogArchiver struct {
	pool               *pgxpool.Pool
	archiveBeforePurge bool
}

// NewLogArchiver creates a LogArchiver from an existing pool.
func NewLogArchiver(pool *pgxpool.Pool, archiveBeforePurge bool) *LogArchiver {
	return &LogArchiver{pool: pool, archiveBeforePurge: archiveBeforePurge}
}

// ArchiveAndPurge moves old records to archived_* tables and deletes them
// from the hot tables. Returns the total number of rows archived and purged.
func (a *LogArchiver) ArchiveAndPurge(ctx context.Context, agentLogsDays, auditEventsDays int) (archived, purged int, err error) {
	tx, err := a.pool.Begin(ctx)
	if err != nil {
		return 0, 0, fmt.Errorf("postgres: log archiver begin: %w", err)
	}
	defer tx.Rollback(ctx)

	agentCutoff := fmt.Sprintf("%d days", agentLogsDays)
	auditCutoff := fmt.Sprintf("%d days", auditEventsDays)

	if a.archiveBeforePurge {
		ar, err := a.archiveRows(ctx, tx,
			"archived_agent_runs", "agent_runs", agentCutoff)
		if err != nil {
			return 0, 0, err
		}

		am, err := a.archiveRows(ctx, tx,
			"archived_agent_messages", "agent_messages", agentCutoff,
			"WHERE run_id IN (SELECT id FROM agent_runs WHERE created_at < NOW() - $1::interval)")
		if err != nil {
			return 0, 0, err
		}

		ae, err := a.archiveRows(ctx, tx,
			"archived_audit_events", "audit_events", auditCutoff)
		if err != nil {
			return 0, 0, err
		}

		archived = ar + am + ae
	}

	// Purge agent_messages first (FK dependency), then agent_runs, then audit_events.
	pm, err := a.purgeRows(ctx, tx, "agent_messages", agentCutoff,
		"WHERE run_id IN (SELECT id FROM agent_runs WHERE created_at < NOW() - $1::interval)")
	if err != nil {
		return 0, 0, err
	}

	pr, err := a.purgeRows(ctx, tx, "agent_runs", agentCutoff)
	if err != nil {
		return 0, 0, err
	}

	pe, err := a.purgeRows(ctx, tx, "audit_events", auditCutoff)
	if err != nil {
		return 0, 0, err
	}

	purged = pm + pr + pe

	if err := tx.Commit(ctx); err != nil {
		return 0, 0, fmt.Errorf("postgres: log archiver commit: %w", err)
	}

	return archived, purged, nil
}

func (a *LogArchiver) archiveRows(ctx context.Context, tx pgx.Tx, dst, src, cutoff string, extraCondition ...string) (int, error) {
	cond := "WHERE created_at < NOW() - $1::interval"
	if len(extraCondition) > 0 {
		cond = extraCondition[0]
	}

	q := fmt.Sprintf("INSERT INTO %s SELECT * FROM %s %s ON CONFLICT (id) DO NOTHING", dst, src, cond)
	tag, err := tx.Exec(ctx, q, cutoff)
	if err != nil {
		return 0, fmt.Errorf("postgres: archive %s → %s: %w", src, dst, err)
	}
	return int(tag.RowsAffected()), nil
}

func (a *LogArchiver) purgeRows(ctx context.Context, tx pgx.Tx, table, cutoff string, extraCondition ...string) (int, error) {
	cond := "WHERE created_at < NOW() - $1::interval"
	if len(extraCondition) > 0 {
		cond = extraCondition[0]
	}

	q := fmt.Sprintf("DELETE FROM %s %s", table, cond)
	tag, err := tx.Exec(ctx, q, cutoff)
	if err != nil {
		return 0, fmt.Errorf("postgres: purge %s: %w", table, err)
	}
	return int(tag.RowsAffected()), nil
}

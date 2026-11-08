-- Archival tables for old agent/audit data.
-- Rows are moved here before being purged from the hot tables.

CREATE TABLE IF NOT EXISTS archived_agent_runs (
    LIKE agent_runs INCLUDING ALL
);

CREATE TABLE IF NOT EXISTS archived_agent_messages (
    id              TEXT PRIMARY KEY,
    run_id          TEXT,
    role            TEXT NOT NULL,
    content         TEXT NOT NULL,
    tool_name       TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

ALTER TABLE archived_agent_messages DROP CONSTRAINT IF EXISTS agent_messages_run_id_fkey;
ALTER TABLE archived_agent_messages DROP CONSTRAINT IF EXISTS archived_agent_messages_run_id_fkey;

CREATE TABLE IF NOT EXISTS archived_audit_events (
    LIKE audit_events INCLUDING ALL
);

CREATE INDEX IF NOT EXISTS idx_archived_agent_runs_created
    ON archived_agent_runs(created_at DESC);

CREATE INDEX IF NOT EXISTS idx_archived_agent_messages_run
    ON archived_agent_messages(run_id, created_at);

CREATE INDEX IF NOT EXISTS idx_archived_audit_events_created
    ON archived_audit_events(created_at DESC);

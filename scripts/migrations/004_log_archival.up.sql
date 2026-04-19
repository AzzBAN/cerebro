-- Archival tables for old agent/audit data.
-- Rows are moved here before being purged from the hot tables.

CREATE TABLE IF NOT EXISTS archived_agent_runs (
    LIKE agent_runs INCLUDING ALL
);

CREATE TABLE IF NOT EXISTS archived_agent_messages (
    LIKE agent_messages INCLUDING ALL
);

CREATE TABLE IF NOT EXISTS archived_audit_events (
    LIKE audit_events INCLUDING ALL
);

CREATE INDEX IF NOT EXISTS idx_archived_agent_runs_created
    ON archived_agent_runs(created_at DESC);

CREATE INDEX IF NOT EXISTS idx_archived_agent_messages_run
    ON archived_agent_messages(run_id, created_at);

CREATE INDEX IF NOT EXISTS idx_archived_audit_events_created
    ON archived_audit_events(created_at DESC);

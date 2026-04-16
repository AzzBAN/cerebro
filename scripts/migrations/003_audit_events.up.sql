CREATE TABLE IF NOT EXISTS audit_events (
    id              TEXT PRIMARY KEY,
    event_type      TEXT NOT NULL,              -- command|halt|config_reload|reconcile|mismatch
    actor           TEXT,                       -- telegram_user_id | cli | system
    payload         JSONB,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_audit_events_created ON audit_events(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_audit_events_type ON audit_events(event_type, created_at DESC);

CREATE TABLE IF NOT EXISTS agent_runs (
    id              TEXT PRIMARY KEY,
    agent           TEXT NOT NULL,              -- screening|risk|copilot|reviewer
    model           TEXT NOT NULL,
    provider        TEXT NOT NULL,
    input_tokens    INT,
    output_tokens   INT,
    cost_usd_cents  INT,
    latency_ms      INT,
    outcome         TEXT,                       -- bias_score | approved | rejected | reviewer_recommendation
    error           TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS agent_messages (
    id              TEXT PRIMARY KEY,
    run_id          TEXT REFERENCES agent_runs(id) ON DELETE CASCADE,
    role            TEXT NOT NULL,              -- system|user|assistant|tool
    content         TEXT NOT NULL,              -- raw API keys must NEVER appear here
    tool_name       TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_agent_runs_agent ON agent_runs(agent, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_agent_runs_created ON agent_runs(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_agent_messages_run ON agent_messages(run_id, created_at);

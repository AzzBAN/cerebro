CREATE TABLE IF NOT EXISTS order_intents (
    id              TEXT PRIMARY KEY,
    correlation_id  TEXT NOT NULL,
    symbol          TEXT NOT NULL,
    side            TEXT NOT NULL,              -- buy | sell
    quantity        NUMERIC(30, 10) NOT NULL,
    stop_loss       NUMERIC(30, 10),
    take_profit_1   NUMERIC(30, 10),
    strategy        TEXT NOT NULL,
    environment     TEXT NOT NULL,              -- paper | live
    status          TEXT NOT NULL DEFAULT 'pending', -- pending|submitted|filled|rejected|cancelled
    broker_order_id TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS trades (
    id              TEXT PRIMARY KEY,
    intent_id       TEXT REFERENCES order_intents(id),
    correlation_id  TEXT NOT NULL,
    symbol          TEXT NOT NULL,
    side            TEXT NOT NULL,
    quantity        NUMERIC(30, 10) NOT NULL,
    fill_price      NUMERIC(30, 10) NOT NULL,
    fees            NUMERIC(30, 10) NOT NULL DEFAULT 0,
    pnl             NUMERIC(30, 10),            -- null until position closes
    strategy        TEXT NOT NULL,
    venue           TEXT NOT NULL,
    closed_at       TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_order_intents_correlation ON order_intents(correlation_id);
CREATE INDEX IF NOT EXISTS idx_order_intents_created ON order_intents(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_order_intents_status ON order_intents(status);
CREATE INDEX IF NOT EXISTS idx_trades_correlation ON trades(correlation_id);
CREATE INDEX IF NOT EXISTS idx_trades_created ON trades(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_trades_symbol ON trades(symbol, created_at DESC);

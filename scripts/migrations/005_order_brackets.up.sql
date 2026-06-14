-- 005_order_brackets: extend order_intents with the full execution contract.
--
-- This migration widens order_intents to persist the new OrderIntent fields
-- (entry order type, limit / stop prices, TIF, reduce-only, position side,
-- leverage) plus a pointer from bracket-leg rows back to their parent entry
-- intent. The order_brackets table records the broker-assigned identifiers
-- for each protective leg so CancelBracket has everything it needs.

-- Entry-order metadata -------------------------------------------------------
ALTER TABLE order_intents
    ADD COLUMN IF NOT EXISTS order_type     TEXT        NOT NULL DEFAULT 'market',
    ADD COLUMN IF NOT EXISTS limit_price    NUMERIC(30, 10),
    ADD COLUMN IF NOT EXISTS stop_price     NUMERIC(30, 10),
    ADD COLUMN IF NOT EXISTS time_in_force  TEXT,
    ADD COLUMN IF NOT EXISTS reduce_only    BOOLEAN     NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS position_side  TEXT,
    ADD COLUMN IF NOT EXISTS leverage       INTEGER,
    ADD COLUMN IF NOT EXISTS scale_out_pct  DOUBLE PRECISION NOT NULL DEFAULT 0;

-- Protective bracket recording ----------------------------------------------
-- One row per bracket attached to a filled entry. For Binance spot the
-- list_id is the OCO list id; for futures it is null and each leg's order
-- id is populated directly. Either leg may be null if the broker only
-- placed a single leg (SL-only spot fallback, or partial-bracket failure
-- on futures).
CREATE TABLE IF NOT EXISTS order_brackets (
    id                   TEXT PRIMARY KEY,
    parent_intent_id     TEXT NOT NULL REFERENCES order_intents(id),
    correlation_id       TEXT NOT NULL,
    symbol               TEXT NOT NULL,
    venue                TEXT NOT NULL,
    entry_side           TEXT NOT NULL,       -- buy | sell (side of the protected position)
    quantity             NUMERIC(30, 10) NOT NULL,
    stop_price           NUMERIC(30, 10),
    take_profit_price    NUMERIC(30, 10),
    list_id              TEXT,                -- spot OCO list id; null on futures
    stop_order_id        TEXT,                -- broker-assigned id for the stop leg
    take_profit_order_id TEXT,                -- broker-assigned id for the TP leg
    status               TEXT NOT NULL DEFAULT 'active', -- active | cancelled | triggered | failed
    created_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_order_brackets_parent ON order_brackets(parent_intent_id);
CREATE INDEX IF NOT EXISTS idx_order_brackets_symbol ON order_brackets(symbol, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_order_brackets_status ON order_brackets(status);

-- Reverse of 005_order_brackets.up.sql.

DROP TABLE IF EXISTS order_brackets;

ALTER TABLE order_intents
    DROP COLUMN IF EXISTS scale_out_pct,
    DROP COLUMN IF EXISTS leverage,
    DROP COLUMN IF EXISTS position_side,
    DROP COLUMN IF EXISTS reduce_only,
    DROP COLUMN IF EXISTS time_in_force,
    DROP COLUMN IF EXISTS stop_price,
    DROP COLUMN IF EXISTS limit_price,
    DROP COLUMN IF EXISTS order_type;

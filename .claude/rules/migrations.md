# Database Migrations

## Tool: golang-migrate

Migrations are managed with **golang-migrate** (`migrate` CLI). Use `migrate-up` / `migrate-down` Makefile targets for local development.

```bash
make migrate-up    # Applies all pending migrations
make migrate-down  # Rolls back one step
```

## File Naming

```
NNN_descriptive_name.up.sql
NNN_descriptive_name.down.sql
```

- `NNN` is zero-padded sequential (e.g. `004`, `005`).
- Name describes **intent**, not mechanism: `004_add_strategy_snapshots` not `004_alter_table`.
- Every `.up.sql` must have a matching `.down.sql` that fully reverses it.

## SQL Conventions

```sql
-- GOOD: idempotent, explicit types, named constraints
CREATE TABLE IF NOT EXISTS order_intents (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    symbol TEXT NOT NULL,
    side TEXT NOT NULL CHECK (side IN ('BUY', 'SELL')),
    quantity NUMERIC(20,8) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_order_intents_symbol ON order_intents (symbol);
```

- Use `TIMESTAMPTZ` (never `TIMESTAMP`) for all time columns.
- Use `NUMERIC(20,8)` for prices and quantities — **never** `FLOAT` or `DOUBLE PRECISION`.
- Use `IF NOT EXISTS` / `IF EXISTS` for idempotency.
- Name all foreign keys and check constraints explicitly.

## Schema Baseline (5 Migrations, 10 Tables)

| Migration | Tables Created | Purpose |
|-----------|----------------|---------|
| `001_initial_schema` | `order_intents`, `trades` | Core trading tables |
| `002_agent_tables` | `agent_runs`, `agent_messages` | Agent run tracking and message logging |
| `003_audit_events` | `audit_events` | System audit trail (commands, events) |
| `004_log_archival` | `archived_agent_runs`, `archived_agent_messages`, `archived_audit_events` | Cold-storage archive tables |
| `005_order_brackets` | `order_brackets` (+ ALTER `order_intents`) | Bracket orders (SL/TP legs), extended entry metadata |

### Migration Details

#### 001: Initial Schema

| Table | Purpose |
|-------|---------|
| `order_intents` | Tracks every order request before execution. Stores symbol, side, quantity, stop-loss, take-profit, strategy, environment, status lifecycle (`pending → submitted → filled → rejected → cancelled`), broker order ID. |
| `trades` | Records actual fills/executions. Links back to intent via `intent_id`. Stores fill price, fees, PnL (null until position closes), venue, `closed_at` timestamp. |

Indexes: `correlation_id`, `created_at DESC`, `status` on intents; `correlation_id`, `created_at DESC`, `symbol+created_at` on trades.

#### 002: Agent Tables

| Table | Purpose |
|-------|---------|
| `agent_runs` | Logs every agent invocation. Records model, provider, token usage, cost, latency, outcome, errors. |
| `agent_messages` | Stores full conversation per agent run (system/user/assistant/tool messages). Cascading delete on `run_id`. |

Indexes: `agent+created_at`, `created_at DESC` on runs; `run_id+created_at` on messages.

#### 003: Audit Events

| Table | Purpose |
|-------|---------|
| `audit_events` | Immutable audit log of system actions: commands, halts, config reloads, reconciliations, mismatches. Tracks actor and JSONB payload. |

Indexes: `created_at DESC`, `event_type+created_at DESC`.

#### 004: Log Archival

| Table | Purpose |
|-------|---------|
| `archived_agent_runs` | Cold-storage clone of `agent_runs` (same schema). Rows moved here before purging hot tables. |
| `archived_agent_messages` | Cold-storage clone of `agent_messages`. |
| `archived_audit_events` | Cold-storage clone of `audit_events`. |

#### 005: Order Brackets

| Change | Purpose |
|--------|---------|
| ALTER `order_intents` | Adds: `order_type`, `limit_price`, `stop_price`, `time_in_force`, `reduce_only`, `position_side`, `leverage`, `scale_out_pct` |
| `order_brackets` | Records protective bracket legs attached to filled entries. One row per bracket with parent intent reference, stop/TP prices, broker-assigned order IDs per leg, OCO list (spot), status (`active → cancelled/triggered/failed`). |

Indexes: `parent_intent_id`, `symbol+created_at DESC`, `status` on brackets.

## Known Inconsistency

**⚠️ docker-compose uses Goose** (`ghcr.io/pressly/goose:latest`) for migrations in the `migrate` profile, while Makefile uses `golang-migrate`. This is an inconsistency that should be resolved.

## Rules

- **Never modify** a migration that has been applied to any environment. Write a new migration instead.
- Always test both `.up.sql` and `.down.sql` before committing.
- New tables need corresponding Go types in `internal/domain/` and store implementations in `internal/adapter/postgres/`.

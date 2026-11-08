# Database Migrations

## Migration Tool

Uses `golang-migrate/migrate` CLI + library.

```bash
# Apply migrations
make migrate-up

# Rollback last migration
make migrate-down

# Create new migration
migrate create -ext sql -dir scripts/migrations -seq <name>
```

## 5 Migrations (10 Tables)

| # | File | Tables Created | Purpose |
|---|------|----------------|---------|
| 001 | `001_create_order_intents.up.sql` | `order_intents` | Pre-execution order proposals |
| 002 | `002_create_trades.up.sql` | `trades` | Executed trade records |
| 003 | `003_create_agent_runs.up.sql` | `agent_runs`, `agent_messages` | Agent execution logs |
| 004 | `004_create_audit_events.up.sql` | `audit_events` | System audit trail |
| 005 | `005_create_order_brackets.up.sql` | `order_brackets` | Bracket order tracking |

Plus 5 archive tables (`archived_*`) for historical data.

## Table Schemas

### `order_intents`

```sql
CREATE TABLE order_intents (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    symbol TEXT NOT NULL,
    side TEXT NOT NULL CHECK (side IN ('BUY', 'SELL')),
    qty NUMERIC(24, 8) NOT NULL,
    price NUMERIC(24, 8),
    order_type TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_order_intents_status ON order_intents(status);
CREATE INDEX idx_order_intents_symbol ON order_intents(symbol);
```

### `trades`

```sql
CREATE TABLE trades (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    order_intent_id UUID REFERENCES order_intents(id),
    symbol TEXT NOT NULL,
    side TEXT NOT NULL,
    qty NUMERIC(24, 8) NOT NULL,
    entry_price NUMERIC(24, 8) NOT NULL,
    exit_price NUMERIC(24, 8),
    pnl NUMERIC(24, 8),
    fees NUMERIC(24, 8) NOT NULL DEFAULT 0,
    status TEXT NOT NULL DEFAULT 'open',
    opened_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    closed_at TIMESTAMPTZ
);

CREATE INDEX idx_trades_status ON trades(status);
CREATE INDEX idx_trades_symbol ON trades(symbol);
```

### `agent_runs`

```sql
CREATE TABLE agent_runs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    agent_name TEXT NOT NULL,
    started_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at TIMESTAMPTZ,
    status TEXT NOT NULL DEFAULT 'running',
    input_tokens INT NOT NULL DEFAULT 0,
    output_tokens INT NOT NULL DEFAULT 0,
    cost_usd NUMERIC(10, 6) NOT NULL DEFAULT 0,
    error TEXT
);

CREATE INDEX idx_agent_runs_agent ON agent_runs(agent_name);
CREATE INDEX idx_agent_runs_started ON agent_runs(started_at);
```

### `agent_messages`

```sql
CREATE TABLE agent_messages (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    agent_run_id UUID NOT NULL REFERENCES agent_runs(id) ON DELETE CASCADE,
    role TEXT NOT NULL CHECK (role IN ('system', 'user', 'assistant', 'tool')),
    content TEXT NOT NULL,
    tool_calls JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_agent_messages_run ON agent_messages(agent_run_id);
```

### `audit_events`

```sql
CREATE TABLE audit_events (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    event_type TEXT NOT NULL,
    actor TEXT NOT NULL,
    resource_type TEXT NOT NULL,
    resource_id TEXT NOT NULL,
    details JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_audit_events_type ON audit_events(event_type);
CREATE INDEX idx_audit_events_resource ON audit_events(resource_type, resource_id);
CREATE INDEX idx_audit_events_created ON audit_events(created_at);
```

### `order_brackets`

```sql
CREATE TABLE order_brackets (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    trade_id UUID NOT NULL REFERENCES trades(id) ON DELETE CASCADE,
    stop_loss_order_id TEXT,
    take_profit_order_id TEXT,
    stop_loss_price NUMERIC(24, 8) NOT NULL,
    take_profit_price NUMERIC(24, 8) NOT NULL,
    status TEXT NOT NULL DEFAULT 'active',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    triggered_at TIMESTAMPTZ
);

CREATE INDEX idx_order_brackets_trade ON order_brackets(trade_id);
CREATE INDEX idx_order_brackets_status ON order_brackets(status);
```

## Archive Tables

5 `archived_*` tables mirror main tables for historical data:
- `archived_order_intents`
- `archived_trades`
- `archived_agent_runs`
- `archived_agent_messages`
- `archived_audit_events`

Archival job runs daily, moves records older than 30 days.

## Conventions

- Always use `UUID` for primary keys (never serial)
- Use `NUMERIC(24, 8)` for monetary values (never FLOAT)
- Use `TIMESTAMPTZ` for timestamps (always UTC)
- Add indexes on foreign keys and frequently queried columns
- Use `ON DELETE CASCADE` for child tables
- Name migrations: `{seq}_{action}_{table}.up.sql` / `.down.sql`

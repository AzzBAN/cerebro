# Cerebro — Project Rules

@.claude/rules/project-overview.md
@.claude/rules/hexagonal-architecture.md
@.claude/rules/go-standards.md
@.claude/rules/config-conventions.md
@.claude/rules/agent-llm.md
@.claude/rules/runtime-wiring.md
@.claude/rules/migrations.md
@.claude/rules/testing-standards.md

## Quick Commands

```bash
make build          # Build binary
make test           # Run unit tests
make test-int       # Run integration tests (requires DB)
make lint           # golangci-lint
make tidy           # go mod tidy
make migrate-up     # Apply pending migrations
make migrate-down   # Rollback one migration
make check          # Dry-run config validation
```

## Local Development

1. Copy `configs/secrets.env.example` to `configs/secrets.env` and fill in values.
2. Ensure `DATABASE_URL` is set in environment or `.env` file.
3. `make build && ./cerebro run --config-dir=configs`

## Supabase Project

- **Project Name:** Cerebro
- **Project ID:** `azzplqrjsmeueedehmpm`
- **Region:** ap-southeast-1
- Migrations applied via `make migrate-up` using `golang-migrate`.

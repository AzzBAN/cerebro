# Cerebro — Agent / Operator Notes

Quick commands and tribal-knowledge notes that don't fit inside per-package docs.
See `CLAUDE.md` for the canonical architecture + conventions.

## Verification

```bash
make build         # build binary
make test          # unit tests
make lint          # golangci-lint
make check         # dry-run config validation
```

## News scraper

The news feed lives at `internal/ingest/scrape/financialjuice.go` — a pure-Go
RSS scraper (no API key required). The ingest scheduler writes to Redis on each
tick under `news:latest` (global newest-first list, TTL = interval × 3). The
agent `fetch_latest_news` tool reads this key first and only falls back to a
live scrape on cache miss.

## graphify

This project has a knowledge graph at graphify-out/ with god nodes, community structure, and cross-file relationships.

When the user types `/graphify`, use the installed graphify skill or instructions before doing anything else.

Rules:
- For codebase questions, first run `graphify query "<question>"` when graphify-out/graph.json exists. Use `graphify path "<A>" "<B>"` for relationships and `graphify explain "<concept>"` for focused concepts. These return a scoped subgraph, usually much smaller than GRAPH_REPORT.md or raw grep output.
- Dirty graphify-out/ files are expected after hooks or incremental updates; dirty graph files are not a reason to skip graphify. Only skip graphify if the task is about stale or incorrect graph output, or the user explicitly says not to use it.
- If graphify-out/wiki/index.md exists, use it for broad navigation instead of raw source browsing.
- Read graphify-out/GRAPH_REPORT.md only for broad architecture review or when query/path/explain do not surface enough context.
- After modifying code, run `graphify update .` to keep the graph current (AST-only, no API cost).

package postgres

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// NewPool creates a pgx connection pool from a DATABASE_URL.
func NewPool(ctx context.Context, databaseURL string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("postgres: parse pool config: %w", err)
	}

	cfg.MaxConns = 10
	cfg.MinConns = 2
	cfg.MaxConnLifetime = 1 * time.Hour
	cfg.MaxConnIdleTime = 30 * time.Minute

	if cfg.ConnConfig.Port == 6543 || strings.Contains(databaseURL, ":6543") || strings.Contains(databaseURL, "pgbouncer=true") {
		cfg.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeExec
	}

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("postgres: create pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("postgres: ping: %w", err)
	}
	return pool, nil
}

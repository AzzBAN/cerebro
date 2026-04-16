package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/azhar/cerebro/internal/domain"
	"github.com/jackc/pgx/v5/pgxpool"
)

// AgentLogStore implements port.AgentLogStore using pgx.
type AgentLogStore struct {
	pool *pgxpool.Pool
}

// NewAgentLogStore creates an AgentLogStore from an existing pool.
func NewAgentLogStore(pool *pgxpool.Pool) *AgentLogStore {
	return &AgentLogStore{pool: pool}
}

// SaveRun persists an agent invocation record.
func (s *AgentLogStore) SaveRun(ctx context.Context, r domain.AgentRun) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO agent_runs
			(id, agent, model, provider, input_tokens, output_tokens,
			 cost_usd_cents, latency_ms, outcome, error, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
		ON CONFLICT (id) DO NOTHING`,
		r.ID, string(r.Agent), r.Model, r.Provider,
		r.InputTokens, r.OutputTokens, r.CostUSDCents, r.LatencyMS,
		r.Outcome, r.Error, r.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("postgres: save agent run %s: %w", r.ID, err)
	}
	return nil
}

// RunsByWindow returns agent runs within the given UTC window.
func (s *AgentLogStore) RunsByWindow(ctx context.Context, agent string, from, to time.Time) ([]domain.AgentRun, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, agent, model, provider, input_tokens, output_tokens,
		       cost_usd_cents, latency_ms, outcome, error, created_at
		FROM agent_runs
		WHERE agent=$1 AND created_at >= $2 AND created_at < $3
		ORDER BY created_at ASC`,
		agent, from, to,
	)
	if err != nil {
		return nil, fmt.Errorf("postgres: runs by window: %w", err)
	}
	defer rows.Close()

	var out []domain.AgentRun
	for rows.Next() {
		var r domain.AgentRun
		if err := rows.Scan(
			&r.ID, &r.Agent, &r.Model, &r.Provider,
			&r.InputTokens, &r.OutputTokens, &r.CostUSDCents, &r.LatencyMS,
			&r.Outcome, &r.Error, &r.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("postgres: scan agent run: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// SaveMessage persists a single agent conversation message.
// Raw API keys must NEVER appear in content — enforced by the agent runtime.
func (s *AgentLogStore) SaveMessage(ctx context.Context, m domain.AgentMessage) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO agent_messages
			(id, run_id, role, content, tool_name, created_at)
		VALUES ($1,$2,$3,$4,$5,$6)
		ON CONFLICT (id) DO NOTHING`,
		m.ID, m.RunID, m.Role, m.Content, m.ToolName, m.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("postgres: save message %s: %w", m.ID, err)
	}
	return nil
}

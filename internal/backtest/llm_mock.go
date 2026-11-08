package backtest

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/azhar/cerebro/internal/domain"
	"github.com/azhar/cerebro/internal/port"
)

var _ port.LLM = (*LLMMock)(nil)

// LLMMock implements port.LLM using pre-recorded fixture files or tool scripts.
// Each call sequence is deterministic.
type LLMMock struct {
	mu        sync.Mutex
	responses []string
	idx       int
	toolCall  string
	toolInput json.RawMessage
	err       error
}

// NewLLMMock creates a fixture-backed LLM mock.
// fixtureFile should be a JSON array of response strings.
func NewLLMMock(fixtureFile string) (*LLMMock, error) {
	data, err := os.ReadFile(fixtureFile)
	if err != nil {
		return nil, fmt.Errorf("llm_mock: read fixture %s: %w", fixtureFile, err)
	}
	var responses []string
	if err := json.Unmarshal(data, &responses); err != nil {
		return nil, fmt.Errorf("llm_mock: parse fixture: %w", err)
	}
	return &LLMMock{responses: responses}, nil
}

// NewLLMMockFromSlice creates a mock from a pre-loaded slice.
func NewLLMMockFromSlice(responses []string) *LLMMock {
	return &LLMMock{responses: responses}
}

// WithToolCall configures the mock to invoke a specific tool on Complete.
func (m *LLMMock) WithToolCall(name string, input json.RawMessage) *LLMMock {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.toolCall = name
	m.toolInput = input
	return m
}

// WithError configures the mock to return an error on Complete.
func (m *LLMMock) WithError(err error) *LLMMock {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.err = err
	return m
}

func (m *LLMMock) Provider() string { return "mock" }
func (m *LLMMock) ModelID() string  { return "mock-v1" }

// Complete returns the next fixture response or runs the configured tool call.
func (m *LLMMock) Complete(
	ctx context.Context,
	_ string,
	_ string,
	tools map[string]port.Tool,
) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.toolCall != "" {
		if t, ok := tools[m.toolCall]; ok {
			_, _ = t.Handler(ctx, m.toolInput)
		}
	}

	if m.err != nil {
		return "", m.err
	}

	if len(m.responses) == 0 {
		return `{"bias":"Neutral","reasoning":"mock"}`, nil
	}
	resp := m.responses[m.idx%len(m.responses)]
	m.idx++
	return resp, nil
}

// NeutralBiasFixture returns a bias fixture always returning Neutral.
func NeutralBiasFixture() string {
	return `{"bias":"Neutral","confidence":0.5,"reasoning":"Mock: Neutral bias for all backtest runs","high_impact_event_soon":false,"avoid_entry_minutes":0}`
}

// ApproveAllFixture returns a risk agent fixture that always approves.
func ApproveAllFixture() string {
	return `{"approved":true}`
}

var _ port.AgentLogStore = (*NoopLogStore)(nil)

// NoopLogStore satisfies port.AgentLogStore without persisting.
type NoopLogStore struct{}

func (NoopLogStore) SaveRun(_ context.Context, _ domain.AgentRun) error { return nil }
func (NoopLogStore) RunsByWindow(_ context.Context, _ string, _, _ time.Time) ([]domain.AgentRun, error) {
	return nil, nil
}
func (NoopLogStore) SaveMessage(_ context.Context, _ domain.AgentMessage) error { return nil }

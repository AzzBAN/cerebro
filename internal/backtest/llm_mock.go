package backtest

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/azhar/cerebro/internal/port"
)

// LLMMock implements port.LLM using pre-recorded fixture files.
// Each call sequence is deterministic: responses are served in round-robin order.
// Used by the backtest engine to test strategy performance without live LLM costs.
type LLMMock struct {
	responses []string
	idx       int
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

func (m *LLMMock) Provider() string { return "mock" }
func (m *LLMMock) ModelID() string  { return "mock-v1" }

// Complete returns the next fixture response in sequence (round-robin).
func (m *LLMMock) Complete(
	_ context.Context,
	_ string,
	_ string,
	tools map[string]port.ToolHandler,
) (string, error) {
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

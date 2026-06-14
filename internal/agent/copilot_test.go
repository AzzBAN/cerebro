package agent

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/azhar/cerebro/internal/config"
	"github.com/azhar/cerebro/internal/domain"
	"github.com/azhar/cerebro/internal/port"
)

// captureLLM records every userMessage it receives and returns a scripted reply
// per call so the test can assert what context the Copilot folded in.
type captureLLM struct {
	mu       sync.Mutex
	messages []string
	replies  []string
	calls    int
}

func (l *captureLLM) Complete(ctx context.Context, _ string, userMessage string, _ map[string]port.Tool) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.messages = append(l.messages, userMessage)
	reply := "ok"
	if l.calls < len(l.replies) {
		reply = l.replies[l.calls]
	}
	l.calls++
	return reply, nil
}

func (l *captureLLM) Provider() string { return "stub" }
func (l *captureLLM) ModelID() string  { return "stub-model" }

func (l *captureLLM) messageAt(i int) string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.messages[i]
}

// noopLogStore satisfies port.AgentLogStore without persisting anything.
type noopLogStore struct{}

func (noopLogStore) SaveRun(context.Context, domain.AgentRun) error { return nil }
func (noopLogStore) RunsByWindow(context.Context, string, time.Time, time.Time) ([]domain.AgentRun, error) {
	return nil, nil
}
func (noopLogStore) SaveMessage(context.Context, domain.AgentMessage) error { return nil }

func newTestCopilot(llm port.LLM) *Copilot {
	rt := NewRuntime(llm, noopLogStore{}, config.AgentConfig{MaxTurns: 1, TimeoutTotalSeconds: 5})
	return NewCopilot(rt, nil)
}

func TestCopilot_FirstQuery_SendsRawMessage(t *testing.T) {
	llm := &captureLLM{}
	c := newTestCopilot(llm)

	if _, err := c.Ask(context.Background(), "what are my positions?"); err != nil {
		t.Fatalf("Ask: %v", err)
	}

	got := llm.messageAt(0)
	if got != "what are my positions?" {
		t.Errorf("first message should be the raw query, got %q", got)
	}
}

func TestCopilot_FollowUp_IncludesPriorTurn(t *testing.T) {
	llm := &captureLLM{replies: []string{"You hold 2 BTC and 5 ETH.", "BTC is the bigger position."}}
	c := newTestCopilot(llm)

	if _, err := c.Ask(context.Background(), "what are my positions?"); err != nil {
		t.Fatalf("first Ask: %v", err)
	}
	if _, err := c.Ask(context.Background(), "which is bigger?"); err != nil {
		t.Fatalf("second Ask: %v", err)
	}

	second := llm.messageAt(1)
	for _, want := range []string{
		"what are my positions?",   // prior operator message
		"You hold 2 BTC and 5 ETH.", // prior copilot reply
		"which is bigger?",          // current message
	} {
		if !strings.Contains(second, want) {
			t.Errorf("follow-up message missing %q\n--- message ---\n%s", want, second)
		}
	}
}

func TestCopilot_HistoryTrimmedToCap(t *testing.T) {
	llm := &captureLLM{}
	c := newTestCopilot(llm)

	// One more than the cap so the oldest turn must be evicted.
	total := copilotMaxHistoryTurns + 1
	for i := range total {
		if _, err := c.Ask(context.Background(), "q-"+string(rune('a'+i))); err != nil {
			t.Fatalf("Ask %d: %v", i, err)
		}
	}

	c.mu.Lock()
	n := len(c.history)
	oldest := c.history[0].query
	c.mu.Unlock()

	if n != copilotMaxHistoryTurns {
		t.Errorf("history length = %d, want %d", n, copilotMaxHistoryTurns)
	}
	if oldest == "q-a" {
		t.Error("oldest turn (q-a) should have been evicted")
	}
}

func TestCopilot_FailedTurn_NotRecorded(t *testing.T) {
	llm := &captureLLM{}
	c := newTestCopilot(llm)

	// Seed one good turn.
	if _, err := c.Ask(context.Background(), "first"); err != nil {
		t.Fatalf("Ask: %v", err)
	}

	// A cancelled context forces the invocation to fail; history must not grow.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.Ask(ctx, "second"); err == nil {
		t.Fatal("expected error from cancelled context")
	}

	c.mu.Lock()
	n := len(c.history)
	c.mu.Unlock()
	if n != 1 {
		t.Errorf("history length = %d, want 1 (failed turn must not be recorded)", n)
	}
}

func TestTruncateHistory(t *testing.T) {
	short := "abc"
	if got := truncateHistory(short); got != short {
		t.Errorf("short string altered: %q", got)
	}
	long := strings.Repeat("x", copilotMaxHistoryCharsPerEntry+50)
	got := truncateHistory(long)
	if len(got) != copilotMaxHistoryCharsPerEntry+len("…") {
		t.Errorf("truncated length = %d, want %d", len(got), copilotMaxHistoryCharsPerEntry+len("…"))
	}
	if !strings.HasSuffix(got, "…") {
		t.Error("truncated string should end with ellipsis")
	}
}

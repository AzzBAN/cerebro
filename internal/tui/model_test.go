package tui

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/azhar/cerebro/internal/domain"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/shopspring/decimal"
)

// stripANSI removes ANSI escape sequences for height/width measurement.
func stripANSI(s string) string {
	var b strings.Builder
	i := 0
	for i < len(s) {
		if s[i] == '\033' && i+1 < len(s) && s[i+1] == '[' {
			i += 2
			for i < len(s) && s[i] != 'm' {
				i++
			}
			if i < len(s) {
				i++ // skip 'm'
			}
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

// countLines returns the number of newlines + 1 in s (ignoring trailing newline).
func countLines(s string) int {
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return 0
	}
	return strings.Count(s, "\n") + 1
}

func TestView_HeightExactlyMatchesTerminal(t *testing.T) {
	tests := []struct {
		name   string
		width  int
		height int
	}{
		{"small 80x24", 80, 24},
		{"medium 100x30", 100, 30},
		{"wide 120x40", 120, 40},
		{"narrow 60x20", 60, 20},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := New(500)
			m.width = tt.width
			m.height = tt.height
			m.now = time.Now()

			for i, sym := range []string{"BTC/USDT", "ETH/USDT", "SOL/USDT"} {
				m.quotes[sym] = quoteState{
					symbol:      sym,
					last:        100.0 + float64(i)*50,
					bid:         99.0 + float64(i)*50,
					ask:         101.0 + float64(i)*50,
					priceChange: 1.5,
					volume24h:   1e9,
				}
			}
			m.recalculateLayout()

			for i := 0; i < 20; i++ {
				m.appendLog(logEntry{
					ts:    time.Now().Add(-time.Duration(i) * time.Second),
					level: "INFO",
					text:  "test log line",
				})
			}

			m.heartbeat = "state=running halted=false pos=0"
			m.heartbeatAt = time.Now()

			view := m.View()
			viewLines := countLines(view)

			if viewLines != tt.height {
				t.Errorf("View() height = %d lines, want exactly %d (diff=%d)",
					viewLines, tt.height, viewLines-tt.height)
				plain := stripANSI(view)
				lines := strings.Split(strings.TrimRight(plain, "\n"), "\n")
				for i, l := range lines {
					t.Logf("  line %3d: %q", i+1, l)
				}
			}
		})
	}
}

func TestView_HeightDoesNotExceedTerminal(t *testing.T) {
	tests := []struct {
		name   string
		width  int
		height int
	}{
		{"small 80x24", 80, 24},
		{"medium 100x30", 100, 30},
		{"wide 120x40", 120, 40},
		{"narrow 60x20", 60, 20},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := New(500)
			m.width = tt.width
			m.height = tt.height
			m.now = time.Now()

			// Simulate having some quotes
			for i, sym := range []string{"BTC/USDT", "ETH/USDT", "SOL/USDT"} {
				m.quotes[sym] = quoteState{
					symbol:      sym,
					last:        100.0 + float64(i)*50,
					bid:         99.0 + float64(i)*50,
					ask:         101.0 + float64(i)*50,
					priceChange: 1.5,
					volume24h:   1e9,
				}
			}
			m.recalculateLayout()

			// Simulate some log entries
			for i := 0; i < 20; i++ {
				m.appendLog(logEntry{
					ts:    time.Now().Add(-time.Duration(i) * time.Second),
					level: "INFO",
					text:  "test log line",
				})
			}

			m.heartbeat = "state=running halted=false pos=0"
			m.heartbeatAt = time.Now()

			view := m.View()
			viewLines := countLines(view)

			if viewLines > tt.height {
				t.Errorf("View() height = %d lines, exceeds terminal height %d",
					viewLines, tt.height)
				// Show which section is overflowing
				plain := stripANSI(view)
				lines := strings.Split(strings.TrimRight(plain, "\n"), "\n")
				for i, l := range lines {
					t.Logf("  line %3d: %q", i+1, l)
				}
			}
		})
	}
}

func TestView_WidthDoesNotExceedTerminal(t *testing.T) {
	m := New(500)
	m.width = 80
	m.height = 24
	m.now = time.Now()

	for i, sym := range []string{"BTC/USDT", "ETH/USDT", "SOL/USDT", "DOGE/USDT", "ADA/USDT"} {
		m.quotes[sym] = quoteState{
			symbol:      sym,
			last:        100.0 + float64(i)*50,
			bid:         99.0 + float64(i)*50,
			ask:         101.0 + float64(i)*50,
			priceChange: 1.5,
			volume24h:   1e9,
		}
	}
	m.recalculateLayout()

	for i := 0; i < 10; i++ {
		m.appendLog(logEntry{
			ts:    time.Now().Add(-time.Duration(i) * time.Second),
			level: "INFO",
			text:  "some activity log entry here",
		})
	}

	m.heartbeat = "state=running halted=false pos=0 spot=0 futures=0 candles=500 signals=12 orders=3"
	m.heartbeatAt = time.Now()

	view := m.View()
	plain := stripANSI(view)
	lines := strings.Split(strings.TrimRight(plain, "\n"), "\n")

	for i, line := range lines {
		lineW := lipgloss.Width(line)
		if lineW > m.width {
			t.Errorf("line %d width = %d, exceeds terminal width %d",
				i+1, lineW, m.width)
		}
	}
}

func TestView_LogEntriesAppearInView(t *testing.T) {
	m := New(500)
	m.width = 100
	m.height = 30
	m.now = time.Now()

	// No quotes → watch panel is small
	m.recalculateLayout()

	// Add log entries
	testLines := []string{
		"engine starting",
		"strategy signal fired",
		"order placed BTC/USDT",
	}
	for _, l := range testLines {
		m.appendLog(logEntry{
			ts:    time.Now(),
			level: "INFO",
			text:  l,
		})
	}

	view := m.View()
	for _, l := range testLines {
		if !strings.Contains(view, l) {
			t.Errorf("log entry %q not found in View() output", l)
		}
	}
}

func TestView_HeartbeatAppearsOnce(t *testing.T) {
	m := New(500)
	m.width = 80
	m.height = 24
	m.now = time.Now()
	m.heartbeat = "state=running halted=false pos=0"
	m.heartbeatAt = time.Now()
	m.recalculateLayout()

	view := m.View()
	plain := stripANSI(view)
	count := strings.Count(plain, "state=running")
	if count != 1 {
		t.Errorf("heartbeat text 'state=running' appears %d times, want 1", count)
	}
}

func TestView_HeaderVisible(t *testing.T) {
	m := New(500)
	m.width = 80
	m.height = 24
	m.now = time.Now()
	m.recalculateLayout()

	view := m.View()
	if !strings.Contains(view, "Cerebro") {
		t.Error("header text 'Cerebro' not found in View() output")
	}
}

func TestView_MarketWatchFullWidth(t *testing.T) {
	m := New(500)
	m.width = 80
	m.height = 24
	m.now = time.Now()
	m.quotes["BTC/USDT"] = quoteState{symbol: "BTC/USDT", last: 50000, bid: 49999, ask: 50001, priceChange: 200, priceChangePercent: 0.4, volume24h: 1.2e9}
	m.recalculateLayout()

	view := m.View()
	plain := stripANSI(view)
	lines := strings.Split(strings.TrimRight(plain, "\n"), "\n")

	// The watch panel should contain the symbol
	found := false
	for _, line := range lines {
		if strings.Contains(line, "BTC/USDT") {
			found = true
			break
		}
	}
	if !found {
		t.Error("BTC/USDT not found in market watch panel")
	}
}

func TestView_PositionsAndLogSideBySide(t *testing.T) {
	m := New(500)
	m.width = 100
	m.height = 30
	m.now = time.Now()
	m.recalculateLayout()

	// Add a position
	m.positionRows = []domain.Position{
		{
			Venue:        domain.VenueBinanceSpot,
			Symbol:       domain.Symbol("BTC/USDT"),
			Side:         domain.SideBuy,
			Quantity:     decimal.RequireFromString("0.001"),
			EntryPrice:   decimal.RequireFromString("50000"),
			CurrentPrice: decimal.RequireFromString("51000"),
		},
	}

	// Add log entries
	for i := 0; i < 5; i++ {
		m.appendLog(logEntry{
			ts:    time.Now(),
			level: "INFO",
			text:  "test log entry",
		})
	}

	view := m.View()
	plain := stripANSI(view)

	if !strings.Contains(plain, "Active Positions") {
		t.Error("positions panel not found in view")
	}
	if !strings.Contains(plain, "Activity & Log") {
		t.Error("log panel not found in view")
	}
	if !strings.Contains(plain, "BTC/USDT") {
		t.Error("position content not found in view")
	}
	if !strings.Contains(plain, "test log entry") {
		t.Error("log entry not found in view")
	}
}

func TestView_ManyQuotes(t *testing.T) {
	m := New(500)
	m.width = 80
	m.height = 24
	m.now = time.Now()

	symbols := []string{"BTC/USDT", "ETH/USDT", "SOL/USDT", "DOGE/USDT", "ADA/USDT", "XRP/USDT", "DOT/USDT", "AVAX/USDT"}
	for i, sym := range symbols {
		m.quotes[sym] = quoteState{
			symbol:      sym,
			last:        100.0 + float64(i)*50,
			bid:         99.0 + float64(i)*50,
			ask:         101.0 + float64(i)*50,
			priceChange: 1.5,
			volume24h:   1e9,
		}
	}
	m.recalculateLayout()

	// Add logs
	for i := 0; i < 15; i++ {
		m.appendLog(logEntry{
			ts:    time.Now().Add(-time.Duration(i) * time.Second),
			level: "INFO",
			text:  "log line",
		})
	}
	m.heartbeat = "state=running"
	m.heartbeatAt = time.Now()

	view := m.View()
	viewLines := countLines(view)

	if viewLines > m.height {
		t.Errorf("with 8 symbols, View() height = %d, exceeds terminal %d",
			viewLines, m.height)
	}
}

func TestRenderLogPanel_NoEmptyLines(t *testing.T) {
	m := Model{
		width:  80,
		height: 30,
		now:    time.Now(),
		logs: []logEntry{
			{ts: time.Now(), level: "INFO", text: "first log"},
			{ts: time.Now(), level: "INFO", text: "second log"},
			{ts: time.Now(), level: "INFO", text: "third log"},
		},
		maxLogLines: 500,
	}
	m.recalculateLayout()

	contentH := m.middleHeight() - 2
	rendered := m.renderLogPanel(contentH)
	plain := stripANSI(rendered)
	lines := strings.Split(strings.TrimRight(plain, "\n"), "\n")

	// First line is the header, rest should be log content (no empty lines between)
	for i, line := range lines {
		if i == 0 {
			if !strings.Contains(line, "Activity") {
				t.Errorf("line 0 should be header, got: %q", line)
			}
			continue
		}
		if strings.TrimSpace(line) == "" {
			t.Errorf("unexpected empty line at position %d in log panel", i)
		}
	}
}

func TestRenderStatusBar_NoWrap(t *testing.T) {
	m := Model{
		width:       80,
		height:      24,
		now:         time.Now(),
		heartbeat:   "state=running halted=false pos=0 spot=0 futures=0 candles=500 signals=12 orders=3",
		heartbeatAt: time.Now(),
	}

	bar := m.renderStatusBar()
	barLines := countLines(bar)

	if barLines != 1 {
		t.Errorf("status bar rendered as %d lines, want 1", barLines)
	}

	w := lipgloss.Width(bar)
	if w > m.width {
		t.Errorf("status bar width = %d, exceeds terminal %d", w, m.width)
	}
}

func TestView_AgentPanelAppears(t *testing.T) {
	m := New(500)
	m.width = 100
	m.height = 30
	m.now = time.Now()
	m.recalculateLayout()

	view := m.View()
	plain := stripANSI(view)
	if !strings.Contains(plain, "Agent Activity") {
		t.Error("agent panel header not found in view")
	}
	if !strings.Contains(plain, "No agent activity") {
		t.Error("agent panel empty state not found")
	}
}

func TestView_AgentPanelWithActiveAgents(t *testing.T) {
	m := New(500)
	m.width = 100
	m.height = 30
	m.now = time.Now()
	m.recalculateLayout()

	now := time.Now()
	m.Update(AgentStateMsg{
		Agent:       "screening",
		RunID:       "run-1",
		Step:        StepThinking,
		Provider:    "anthropic",
		Model:       "claude-sonnet-4-6",
		Description: "Analyzing BTC/USDT market conditions",
		Symbol:      "BTC/USDT",
		At:          now,
	})
	m.Update(AgentStateMsg{
		Agent:       "copilot",
		RunID:       "run-2",
		Step:        StepTool,
		ToolName:    "get_positions",
		Provider:    "gemini",
		Model:       "gemini-2.5-flash",
		Description: "Fetching open positions",
		At:          now,
	})

	view := m.View()
	plain := stripANSI(view)

	if !strings.Contains(plain, "screening") {
		t.Error("screening agent not visible in view")
	}
	if !strings.Contains(plain, "Analyzing BTC/USDT market conditions") {
		t.Error("description not visible in view")
	}
	if !strings.Contains(plain, "Fetching open positions") {
		t.Error("tool description not visible")
	}
	if !strings.Contains(plain, "copilot") {
		t.Error("copilot agent not visible")
	}
}

func TestView_AgentPanelCompletedResult(t *testing.T) {
	m := New(500)
	m.width = 100
	m.height = 30
	m.now = time.Now()
	m.recalculateLayout()

	now := time.Now()
	m.Update(AgentStateMsg{
		Agent:    "screening",
		RunID:    "run-1",
		Step:     StepComplete,
		Provider: "anthropic",
		Model:    "claude-sonnet-4-6",
		Symbol:   "BTC/USDT",
		Content:  "Bias: Bullish. Strong momentum with rising OBV.",
		At:       now.Add(-2 * time.Second),
	})

	view := m.View()
	plain := stripANSI(view)

	if !strings.Contains(plain, "screening") {
		t.Error("completed screening agent not in view")
	}
	if !strings.Contains(plain, "BTC/USDT") {
		t.Error("symbol not in completed agent result")
	}
}

func TestView_ConcurrentAgents(t *testing.T) {
	m := New(500)
	m.width = 120
	m.height = 40
	m.now = time.Now()
	m.recalculateLayout()

	now := time.Now()
	m.Update(AgentStateMsg{
		Agent: "screening", RunID: "r1", Step: StepThinking,
		Provider: "anthropic", Model: "claude-sonnet-4-6", Symbol: "BTC/USDT",
		Description: "Analyzing BTC/USDT market conditions", At: now,
	})
	m.Update(AgentStateMsg{
		Agent: "copilot", RunID: "r2", Step: StepObserving,
		Provider: "gemini", Model: "gemini-2.5-flash",
		Description: "Fetching open positions", At: now,
	})
	m.Update(AgentStateMsg{
		Agent: "reviewer", RunID: "r3", Step: StepComplete,
		Provider: "openai_compatible", Model: "gpt-4o",
		Content: "Weekly review complete", At: now.Add(-5 * time.Second),
	})

	view := m.View()
	plain := stripANSI(view)

	if !strings.Contains(plain, "screening") {
		t.Error("active screening agent missing")
	}
	if !strings.Contains(plain, "copilot") {
		t.Error("active copilot agent missing")
	}
	if !strings.Contains(plain, "Fetching open positions") {
		t.Error("observing step description missing")
	}
	if !strings.Contains(plain, "reviewer") {
		t.Error("completed reviewer missing")
	}

	viewLines := countLines(view)
	if viewLines > m.height {
		t.Errorf("view height %d exceeds terminal %d with concurrent agents",
			viewLines, m.height)
	}
}

func TestView_LongLogLinesNoOverflow(t *testing.T) {
	m := New(500)
	m.width = 100
	m.height = 30
	m.now = time.Now()
	m.quotes["BTC/USDT"] = quoteState{symbol: "BTC/USDT", last: 50000, bid: 49999, ask: 50001}
	m.recalculateLayout()

	for i := 0; i < 30; i++ {
		m.appendLog(logEntry{
			ts:    time.Now(),
			level: "INFO",
			text:  strings.Repeat("very long log message that should be truncated ", 5),
		})
	}

	m.heartbeat = "state=running"
	m.heartbeatAt = time.Now()

	view := m.View()
	viewLines := countLines(view)
	if viewLines > m.height {
		t.Errorf("with long log lines, view height = %d, exceeds terminal %d",
			viewLines, m.height)
	}
}

func TestView_AgentPanelError(t *testing.T) {
	m := New(500)
	m.width = 100
	m.height = 30
	m.now = time.Now()
	m.recalculateLayout()

	m.Update(AgentStateMsg{
		Agent:   "screening",
		RunID:   "r1",
		Step:    StepError,
		Content: "LLM timeout: context deadline exceeded",
		At:      time.Now(),
	})

	view := m.View()
	plain := stripANSI(view)
	if !strings.Contains(plain, "error") {
		t.Error("error state not shown in agent panel")
	}
}

// ── /ask copilot tests ─────────────────────────────────────────────────────────

func TestAsk_EnterKeyWithCopilot_TriggersLoading(t *testing.T) {
	m := New(500)
	m.width = 100
	m.height = 30
	m.now = time.Now()
	m.recalculateLayout()

	m.SetCopilotFn(func(ctx context.Context, query string) (string, error) {
		return "test response", nil
	})

	m.inputActive = true
	m.input = "what are my positions"

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m2 := updated.(*Model)

	if !m2.askLoading {
		t.Error("expected askLoading=true after Enter")
	}
	if m2.input != "" {
		t.Error("expected input to be cleared after Enter")
	}
	if m2.inputActive {
		t.Error("expected inputActive=false after Enter")
	}
	if m2.askQuery != "what are my positions" {
		t.Errorf("expected askQuery='what are my positions', got %q", m2.askQuery)
	}
	if cmd == nil {
		t.Error("expected non-nil tea.Cmd to be returned")
	}

	if cmd != nil {
		msg := cmd()
		resp, ok := msg.(AskResponseMsg)
		if !ok {
			t.Fatalf("expected AskResponseMsg, got %T", msg)
		}
		if resp.Response != "test response" {
			t.Errorf("expected response 'test response', got %q", resp.Response)
		}
	}
}

func TestAsk_ResponseSetsContent(t *testing.T) {
	m := New(500)
	m.width = 100
	m.height = 30
	m.now = time.Now()
	m.recalculateLayout()

	m.askLoading = true
	m.askQuery = "test query"
	m.Update(AskResponseMsg{
		Query:    "test query",
		Response: "You have 2 open positions: BTC/USDT and ETH/USDT",
	})

	if m.askLoading {
		t.Error("expected askLoading=false after response")
	}
	if m.askResponse != "You have 2 open positions: BTC/USDT and ETH/USDT" {
		t.Errorf("unexpected askResponse: %q", m.askResponse)
	}
}

func TestAsk_ErrorResponse(t *testing.T) {
	m := New(500)
	m.width = 100
	m.height = 30
	m.now = time.Now()
	m.recalculateLayout()

	m.askLoading = true
	m.Update(AskResponseMsg{
		Query: "broken query",
		Err:   context.DeadlineExceeded,
	})

	if m.askLoading {
		t.Error("expected askLoading=false after error response")
	}
	if !strings.Contains(m.askResponse, "Error:") {
		t.Errorf("expected error prefix in response, got %q", m.askResponse)
	}
}

func TestAsk_ResponseVisibleInView(t *testing.T) {
	m := New(500)
	m.width = 100
	m.height = 40
	m.now = time.Now()
	m.recalculateLayout()

	m.askQuery = "what are my positions"
	m.askResponse = "You have 2 open positions: BTC/USDT LONG and ETH/USDT SHORT"

	view := m.View()
	plain := stripANSI(view)

	if !strings.Contains(plain, "BTC/USDT") {
		t.Error("response content not visible in view")
	}
	if !strings.Contains(plain, "Q:") {
		t.Error("query label not visible in view")
	}
}

func TestAsk_LoadingSpinnerInView(t *testing.T) {
	m := New(500)
	m.width = 100
	m.height = 30
	m.now = time.Now()
	m.recalculateLayout()

	m.askLoading = true

	view := m.View()
	plain := stripANSI(view)

	if !strings.Contains(plain, "thinking...") {
		t.Error("loading indicator not visible in view")
	}
}

func TestAsk_NoCopilotShowsPlaceholder(t *testing.T) {
	m := New(500)
	m.width = 100
	m.height = 30
	m.now = time.Now()
	m.recalculateLayout()

	m.inputActive = true
	m.input = "hello"

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m2 := updated.(*Model)

	if m2.lastResponse != "(Copilot not available — no LLM configured)" {
		t.Errorf("expected placeholder, got %q", m2.lastResponse)
	}
	if cmd != nil {
		t.Error("expected nil cmd when copilotFn is nil")
	}
}

func TestAsk_ViewHeightWithResponse(t *testing.T) {
	m := New(500)
	m.width = 100
	m.height = 30
	m.now = time.Now()
	m.recalculateLayout()

	m.askQuery = "test"
	m.askResponse = "Line 1\nLine 2\nLine 3\nLine 4\nLine 5\nLine 6"

	view := m.View()
	viewLines := countLines(view)

	if viewLines > m.height {
		t.Errorf("view height %d exceeds terminal %d with ask response", viewLines, m.height)
	}
}

func TestAsk_CloseButtonDismissesOnEsc(t *testing.T) {
	m := New(500)
	m.width = 100
	m.height = 30
	m.now = time.Now()
	m.recalculateLayout()

	m.askQuery = "what are my positions"
	m.askResponse = "You have 2 open positions"

	// Verify close button is visible
	view := m.View()
	plain := stripANSI(view)
	if !strings.Contains(plain, "×") {
		t.Error("close button not visible in response panel")
	}

	// Press Esc to dismiss
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m2 := updated.(*Model)

	if m2.askResponse != "" {
		t.Error("expected askResponse to be cleared after Esc")
	}
	if m2.askQuery != "" {
		t.Error("expected askQuery to be cleared after Esc")
	}

	// Verify panel no longer appears in view
	view2 := m2.View()
	plain2 := stripANSI(view2)
	if strings.Contains(plain2, "Q:") {
		t.Error("response panel should not be visible after dismiss")
	}
}

func TestAsk_EscWithoutResponseQuits(t *testing.T) {
	m := New(500)
	m.width = 100
	m.height = 30

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m2 := updated.(*Model)

	// Without a response, Esc should quit
	if cmd == nil {
		t.Error("expected quit command when pressing Esc without response")
	}
	_ = m2
}

func TestAsk_ScrollResponse(t *testing.T) {
	m := New(500)
	m.width = 100
	m.height = 50
	m.now = time.Now()
	m.recalculateLayout()

	// Simulate a long response via AskResponseMsg to populate askLines.
	m.Update(AskResponseMsg{
		Query:    "summary",
		Response: strings.Repeat("line\n", 30),
	})

	if m.askLines == 0 {
		t.Fatal("askLines should be populated after AskResponseMsg")
	}
	if m.askScrollY != 0 {
		t.Fatalf("askScrollY should start at 0, got %d", m.askScrollY)
	}

	// Arrow keys should scroll when ask response is visible.
	model, _ := m.Update(tea.KeyMsg{Type: tea.KeyUp})
	m2 := model.(*Model)
	if m2.askScrollY != 1 {
		t.Fatalf("askScrollY should be 1 after Up, got %d", m2.askScrollY)
	}

	// Page Up.
	model, _ = m2.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	m3 := model.(*Model)
	if m3.askScrollY <= 1 {
		t.Fatalf("askScrollY should be >1 after PgUp, got %d", m3.askScrollY)
	}

	// Down.
	prevScroll := m3.askScrollY
	model, _ = m3.Update(tea.KeyMsg{Type: tea.KeyDown})
	m4 := model.(*Model)
	if m4.askScrollY >= prevScroll {
		t.Fatalf("askScrollY should decrease after Down, got %d (was %d)", m4.askScrollY, prevScroll)
	}

	// Esc dismisses.
	model, _ = m4.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m5 := model.(*Model)
	if m5.askResponse != "" {
		t.Error("Esc should dismiss ask response")
	}
	if m5.askScrollY != 0 {
		t.Error("askScrollY should reset on dismiss")
	}
}

// ── Watch scroll / focus tests ─────────────────────────────────────────────────

func TestWatchScroll_Vertical(t *testing.T) {
	m := New(500)
	m.width = 120
	m.height = 40
	m.now = time.Now()

	symbols := []string{"AAA/USDT", "BBB/USDT", "CCC/USDT", "DDD/USDT", "EEE/USDT",
		"FFF/USDT", "GGG/USDT", "HHH/USDT", "III/USDT", "JJJ/USDT"}
	for i, sym := range symbols {
		m.quotes[sym] = quoteState{
			symbol: sym,
			last:   float64(i) * 100,
			bid:    float64(i)*100 - 1,
			ask:    float64(i)*100 + 1,
		}
	}
	m.recalculateLayout()

	if m.watchScrollY != 0 {
		t.Fatalf("initial watchScrollY should be 0, got %d", m.watchScrollY)
	}

	// Focus watch panel via Tab
	m.Update(tea.KeyMsg{Type: tea.KeyTab})
	if m.focusedPanel != focusWatch {
		t.Fatalf("expected focusWatch after 1 Tab, got %d", m.focusedPanel)
	}

	// Scroll down
	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	if m.watchScrollY != 0 {
		t.Fatalf("watchScrollY should still be 0 (scrolled down = closer to bottom), got %d", m.watchScrollY)
	}

	// Scroll up
	m.Update(tea.KeyMsg{Type: tea.KeyUp})
	if m.watchScrollY != 1 {
		t.Fatalf("watchScrollY should be 1 after Up, got %d", m.watchScrollY)
	}

	// Verify the view shows different content
	view := m.View()
	plain := stripANSI(view)
	if strings.Contains(plain, "AAA/USDT") {
		t.Error("AAA should not be visible after scrolling down by 1")
	}
}

func TestWatchScroll_ClampsAtEnd(t *testing.T) {
	m := New(500)
	m.width = 120
	m.height = 40
	m.now = time.Now()

	for i := 0; i < 8; i++ {
		sym := fmt.Sprintf("SYM%d/USDT", i)
		m.quotes[sym] = quoteState{symbol: sym, last: float64(i) * 100}
	}
	m.recalculateLayout()
	m.focusedPanel = focusWatch

	m.watchScrollY = 100
	m.clampWatchScrollY()

	expected := 8 - maxWatchLines
	if m.watchScrollY != expected {
		t.Errorf("watchScrollY should clamp to %d, got %d", expected, m.watchScrollY)
	}

	m.watchScrollY = -5
	m.clampWatchScrollY()
	if m.watchScrollY != 0 {
		t.Errorf("watchScrollY should clamp to 0, got %d", m.watchScrollY)
	}
}

func TestWatchFocus_TabCycles(t *testing.T) {
	m := New(500)
	m.width = 80
	m.height = 24
	m.now = time.Now()
	m.recalculateLayout()

	if m.focusedPanel != focusNone {
		t.Fatalf("initial focus should be none, got %d", m.focusedPanel)
	}

	m.Update(tea.KeyMsg{Type: tea.KeyTab})
	if m.focusedPanel != focusWatch {
		t.Fatalf("after 1 Tab, expected focusWatch, got %d", m.focusedPanel)
	}

	m.Update(tea.KeyMsg{Type: tea.KeyTab})
	if m.focusedPanel != focusLog {
		t.Fatalf("after 2 Tabs, expected focusLog, got %d", m.focusedPanel)
	}

	m.Update(tea.KeyMsg{Type: tea.KeyTab})
	if m.focusedPanel != focusNone {
		t.Fatalf("after 3 Tabs, expected focusNone, got %d", m.focusedPanel)
	}
}

func TestWatchFocus_ArrowsScrollWhenFocused(t *testing.T) {
	m := New(500)
	m.width = 120
	m.height = 40
	m.now = time.Now()

	for i := 0; i < 10; i++ {
		sym := fmt.Sprintf("SYM%d/USDT", i)
		m.quotes[sym] = quoteState{symbol: sym, last: float64(i) * 100}
	}
	m.recalculateLayout()

	// Without focus, arrow keys scroll log — add enough logs to exceed viewport
	for i := 0; i < 30; i++ {
		m.appendLog(logEntry{ts: time.Now(), level: "INFO", text: "log line"})
	}
	m.clampLogScroll()
	m.Update(tea.KeyMsg{Type: tea.KeyUp})
	if m.logScrollY != 1 {
		t.Fatalf("without focus, Up should scroll log, logScrollY=%d", m.logScrollY)
	}
	if m.watchScrollY != 0 {
		t.Fatalf("without focus, Up should not scroll watch, watchScrollY=%d", m.watchScrollY)
	}

	// With watch focus, arrow keys scroll watch
	m.focusedPanel = focusWatch
	m.logScrollY = 0
	m.Update(tea.KeyMsg{Type: tea.KeyUp})
	if m.watchScrollY != 1 {
		t.Fatalf("with watch focus, Up should scroll watch, watchScrollY=%d", m.watchScrollY)
	}
	if m.logScrollY != 0 {
		t.Fatalf("with watch focus, Up should not scroll log, logScrollY=%d", m.logScrollY)
	}
}

func TestWatchScroll_Horizontal(t *testing.T) {
	m := New(500)
	m.width = 60
	m.height = 24
	m.now = time.Now()

	m.quotes["BTC/USDT"] = quoteState{
		symbol:      "BTC/USDT",
		last:        50000,
		bid:         49999,
		ask:         50001,
		priceChange: 200,
		volume24h:   1.2e9,
	}
	m.recalculateLayout()
	m.focusedPanel = focusWatch

	totalW := watchTotalContentWidth()
	availW := m.watchContentWidth()
	if totalW <= availW {
		t.Skip("content fits, no horizontal scroll needed")
	}

	if m.watchScrollX != 0 {
		t.Fatalf("initial watchScrollX should be 0, got %d", m.watchScrollX)
	}

	m.Update(tea.KeyMsg{Type: tea.KeyRight})
	if m.watchScrollX != watchScrollXStep {
		t.Fatalf("watchScrollX should be %d after Right, got %d", watchScrollXStep, m.watchScrollX)
	}

	view := m.View()
	plain := stripANSI(view)
	if !strings.Contains(plain, "←") {
		t.Error("expected ← scroll indicator after scrolling right")
	}

	m.Update(tea.KeyMsg{Type: tea.KeyLeft})
	if m.watchScrollX != 0 {
		t.Fatalf("watchScrollX should be 0 after Left, got %d", m.watchScrollX)
	}
}

func TestWatchFocus_MouseClick(t *testing.T) {
	m := New(500)
	m.width = 100
	m.height = 30
	m.now = time.Now()

	m.quotes["BTC/USDT"] = quoteState{symbol: "BTC/USDT", last: 50000}
	m.heartbeat = "state=running"
	m.heartbeatAt = time.Now()
	m.recalculateLayout()

	if m.focusedPanel != focusNone {
		t.Fatalf("initial focus should be none, got %d", m.focusedPanel)
	}

	watchH := m.computedWatchH()
	m.Update(tea.MouseMsg{Type: tea.MouseLeft, X: 10, Y: 2})
	if m.focusedPanel != focusWatch {
		t.Errorf("clicking in watch area (y=2, watchH=%d) should set focusWatch, got %d", watchH, m.focusedPanel)
	}

	middleStart := 1 + watchH
	m.Update(tea.MouseMsg{Type: tea.MouseLeft, X: 10, Y: middleStart + 1})
	if m.focusedPanel != focusLog {
		t.Errorf("clicking in middle area (y=%d) should set focusLog, got %d", middleStart+1, m.focusedPanel)
	}
}

func TestWatchFocus_MouseWheel(t *testing.T) {
	m := New(500)
	m.width = 120
	m.height = 40
	m.now = time.Now()

	for i := 0; i < 10; i++ {
		sym := fmt.Sprintf("SYM%d/USDT", i)
		m.quotes[sym] = quoteState{symbol: sym, last: float64(i) * 100}
	}
	m.recalculateLayout()
	m.focusedPanel = focusWatch

	m.Update(tea.MouseMsg{Type: tea.MouseWheelUp, X: 50, Y: 3})
	if m.watchScrollY != 1 {
		t.Fatalf("mouse wheel up on focused watch: expected watchScrollY=1, got %d", m.watchScrollY)
	}

	m.Update(tea.MouseMsg{Type: tea.MouseWheelDown, X: 50, Y: 3})
	if m.watchScrollY != 0 {
		t.Fatalf("mouse wheel down on focused watch: expected watchScrollY=0, got %d", m.watchScrollY)
	}
}

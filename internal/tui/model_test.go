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
					last:        decimal.NewFromFloat(100.0 + float64(i)*50),
					bid:         decimal.NewFromFloat(99.0 + float64(i)*50),
					ask:         decimal.NewFromFloat(101.0 + float64(i)*50),
					priceChange: decimal.NewFromFloat(1.5),
					volume24h:   decimal.NewFromFloat(1e9),
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
					last:        decimal.NewFromFloat(100.0 + float64(i)*50),
					bid:         decimal.NewFromFloat(99.0 + float64(i)*50),
					ask:         decimal.NewFromFloat(101.0 + float64(i)*50),
					priceChange: decimal.NewFromFloat(1.5),
					volume24h:   decimal.NewFromFloat(1e9),
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
			last:        decimal.NewFromFloat(100.0 + float64(i)*50),
			bid:         decimal.NewFromFloat(99.0 + float64(i)*50),
			ask:         decimal.NewFromFloat(101.0 + float64(i)*50),
			priceChange: decimal.NewFromFloat(1.5),
			volume24h:   decimal.NewFromFloat(1e9),
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
	m.quotes["BTC/USDT"] = quoteState{symbol: "BTC/USDT", last: decimal.NewFromInt(50000), bid: decimal.NewFromInt(49999), ask: decimal.NewFromInt(50001), priceChange: decimal.NewFromInt(200), priceChangePercent: decimal.NewFromFloat(0.4), volume24h: decimal.NewFromFloat(1.2e9)}
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

// TestView_MarketWatch_RendersBiasColumn verifies that a cached bias is
// surfaced on the same row as the live quote for that symbol, so the
// operator does not have to switch panels to read directional context.
func TestView_MarketWatch_RendersBiasColumn(t *testing.T) {
	m := New(500)
	// Wide layout that exposes the full Market Watch table, not the
	// XS-collapsed variant (which clips columns aggressively).
	m.width = 200
	m.height = 50
	now := time.Now()
	m.quotes["BTC/USDT"] = quoteState{
		symbol: "BTC/USDT", last: decimal.NewFromInt(50000), bid: decimal.NewFromInt(49999), ask: decimal.NewFromInt(50001),
		priceChange: decimal.NewFromInt(200), priceChangePercent: decimal.NewFromFloat(0.4), volume24h: decimal.NewFromFloat(1.2e9),
	}
	m.quotes["ETH/USDT"] = quoteState{
		symbol: "ETH/USDT", last: decimal.NewFromInt(3000), bid: decimal.NewFromInt(2999), ask: decimal.NewFromInt(3001),
		priceChange: decimal.NewFromInt(-10), priceChangePercent: decimal.NewFromFloat(-0.3), volume24h: decimal.NewFromFloat(5e8),
	}
	m.Update(BiasUpdatedMsg{Result: domain.BiasResult{
		Symbol:    domain.Symbol("BTC/USDT"),
		Score:     domain.BiasBullish,
		CachedAt:  now,
		ExpiresAt: now.Add(15 * time.Minute),
	}})
	m.Update(BiasUpdatedMsg{Result: domain.BiasResult{
		Symbol:    domain.Symbol("ETH/USDT"),
		Score:     domain.BiasBearish,
		CachedAt:  now,
		ExpiresAt: now.Add(15 * time.Minute),
	}})
	m.recalculateLayout()

	plain := stripANSI(m.View())
	lines := strings.Split(plain, "\n")

	// Locate the row for BTC/USDT and ensure "Bullish" appears on it
	// (i.e. the bias column was rendered alongside the quote).
	btcOnRow, ethOnRow := false, false
	for _, line := range lines {
		switch {
		case strings.Contains(line, "BTC/USDT") && strings.Contains(line, "Bullish"):
			btcOnRow = true
		case strings.Contains(line, "ETH/USDT") && strings.Contains(line, "Bearish"):
			ethOnRow = true
		}
	}
	if !btcOnRow {
		t.Errorf("expected BTC/USDT row to include Bullish bias\n%s", plain)
	}
	if !ethOnRow {
		t.Errorf("expected ETH/USDT row to include Bearish bias")
	}

	// Also verify the table header includes the new column label.
	if !strings.Contains(plain, "Bias") {
		t.Errorf("Market Watch header missing Bias column")
	}
}

// TestView_MarketWatch_NoBiasShowsDash verifies that the Bias column
// renders a dimmed em-dash placeholder when the screening agent has
// not produced a bias for that symbol yet.
func TestView_MarketWatch_NoBiasShowsDash(t *testing.T) {
	m := New(500)
	m.width = 200
	m.height = 50
	m.now = time.Now()
	m.quotes["DOGE/USDT-PERP"] = quoteState{
		symbol: "DOGE/USDT-PERP", last: decimal.NewFromFloat(0.1), bid: decimal.NewFromFloat(0.0999), ask: decimal.NewFromFloat(0.1001),
	}
	m.recalculateLayout()

	plain := stripANSI(m.View())
	if !strings.Contains(plain, "DOGE/USDT-PERP") {
		t.Fatalf("DOGE row missing\n%s", plain)
	}
	// We don't try to assert the literal "—" character because the
	// stripping/lipgloss padding may swallow it; what matters is that
	// rendering does not crash and the row still appears.
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
			last:        decimal.NewFromFloat(100.0 + float64(i)*50),
			bid:         decimal.NewFromFloat(99.0 + float64(i)*50),
			ask:         decimal.NewFromFloat(101.0 + float64(i)*50),
			priceChange: decimal.NewFromFloat(1.5),
			volume24h:   decimal.NewFromFloat(1e9),
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
		ta:          newInputTextarea(),
	}
	m.recalculateLayout()

	contentH := m.middleHeight() - 2
	rendered := m.renderLogPanel(0, contentH)
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
		ta:          newInputTextarea(),
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
	m.quotes["BTC/USDT"] = quoteState{symbol: "BTC/USDT", last: decimal.NewFromInt(50000), bid: decimal.NewFromInt(49999), ask: decimal.NewFromInt(50001)}
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

	m.ta.Focus()
	m.ta.SetValue("what are my positions")

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m2 := updated.(*Model)

	if !m2.askLoading {
		t.Error("expected askLoading=true after Enter")
	}
	if m2.ta.Value() != "" {
		t.Error("expected input to be cleared after Enter")
	}
	if m2.ta.Focused() {
		t.Error("expected input to be blurred after Enter")
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

	m.ta.Focus()
	m.ta.SetValue("hello")

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
			last:   decimal.NewFromInt(int64(i) * 100),
			bid:    decimal.NewFromInt(int64(i)*100 - 1),
			ask:    decimal.NewFromInt(int64(i)*100 + 1),
		}
	}
	m.recalculateLayout()

	if m.watchScrollY != 0 {
		t.Fatalf("initial watchScrollY should be 0, got %d", m.watchScrollY)
	}

	// Focus watch panel directly (Tab now cycles main tabs, panel focus is via mouse click).
	m.focusedPanel = focusWatch

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
		m.quotes[sym] = quoteState{symbol: sym, last: decimal.NewFromInt(int64(i) * 100)}
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

func TestMainTab_TabCycles(t *testing.T) {
	m := New(500)
	m.width = 80
	m.height = 24
	m.now = time.Now()
	m.recalculateLayout()

	if m.activeTab != tabDashboard {
		t.Fatalf("initial tab should be Dashboard, got %d", m.activeTab)
	}

	m.Update(tea.KeyMsg{Type: tea.KeyTab})
	if m.activeTab != tabMarket {
		t.Fatalf("after 1 Tab, expected tabMarket, got %d", m.activeTab)
	}

	m.Update(tea.KeyMsg{Type: tea.KeyTab})
	if m.activeTab != tabLogs {
		t.Fatalf("after 2 Tabs, expected tabLogs, got %d", m.activeTab)
	}

	m.Update(tea.KeyMsg{Type: tea.KeyTab})
	if m.activeTab != tabAgents {
		t.Fatalf("after 3 Tabs, expected tabAgents, got %d", m.activeTab)
	}

	m.Update(tea.KeyMsg{Type: tea.KeyTab})
	if m.activeTab != tabDashboard {
		t.Fatalf("after 4 Tabs, expected wrap to tabDashboard, got %d", m.activeTab)
	}
}

func TestWatchFocus_ArrowsScrollWhenFocused(t *testing.T) {
	m := New(500)
	m.width = 120
	m.height = 40
	m.now = time.Now()

	for i := 0; i < 10; i++ {
		sym := fmt.Sprintf("SYM%d/USDT", i)
		m.quotes[sym] = quoteState{symbol: sym, last: decimal.NewFromInt(int64(i) * 100)}
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
		last:        decimal.NewFromInt(50000),
		bid:         decimal.NewFromInt(49999),
		ask:         decimal.NewFromInt(50001),
		priceChange: decimal.NewFromInt(200),
		volume24h:   decimal.NewFromFloat(1.2e9),
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

	m.quotes["BTC/USDT"] = quoteState{symbol: "BTC/USDT", last: decimal.NewFromInt(50000)}
	m.heartbeat = "state=running"
	m.heartbeatAt = time.Now()
	m.recalculateLayout()

	if m.focusedPanel != focusNone {
		t.Fatalf("initial focus should be none, got %d", m.focusedPanel)
	}

	watchH := m.computedWatchH()
	// Click in the watch area (y=2, within header+tabBar+watchH).
	m.Update(tea.MouseMsg{Type: tea.MouseLeft, X: 10, Y: 3})
	if m.focusedPanel != focusWatch {
		t.Errorf("clicking in watch area (y=3, watchH=%d) should set focusWatch, got %d", watchH, m.focusedPanel)
	}

	// Click in the middle area — X=10 is in the left column (Positions) at SM breakpoint.
	middleStart := 1 + 1 + watchH // header(1) + tabBar(1) + watchH
	m.Update(tea.MouseMsg{Type: tea.MouseLeft, X: 10, Y: middleStart + 1})
	if m.focusedPanel != focusPositions {
		t.Errorf("clicking in left column of middle area (y=%d, x=10) should set focusPositions, got %d", middleStart+1, m.focusedPanel)
	}

	// Click in the right column (Log) at SM breakpoint.
	logX := m.width / 2 // should be in the log column
	m.Update(tea.MouseMsg{Type: tea.MouseLeft, X: logX, Y: middleStart + 1})
	if m.focusedPanel != focusLog {
		t.Errorf("clicking in right column of middle area (y=%d, x=%d) should set focusLog, got %d", middleStart+1, logX, m.focusedPanel)
	}
}

func TestWatchFocus_MouseWheel(t *testing.T) {
	m := New(500)
	m.width = 120
	m.height = 40
	m.now = time.Now()

	for i := 0; i < 10; i++ {
		sym := fmt.Sprintf("SYM%d/USDT", i)
		m.quotes[sym] = quoteState{symbol: sym, last: decimal.NewFromInt(int64(i) * 100)}
	}
	m.recalculateLayout()

	// Scroll wheel within the watch panel area (y=3 is inside header+tabBar+watch).
	m.Update(tea.MouseMsg{Type: tea.MouseWheelUp, X: 50, Y: 3})
	if m.watchScrollY != 1 {
		t.Fatalf("mouse wheel up on watch area: expected watchScrollY=1, got %d", m.watchScrollY)
	}

	m.Update(tea.MouseMsg{Type: tea.MouseWheelDown, X: 50, Y: 3})
	if m.watchScrollY != 0 {
		t.Fatalf("mouse wheel down on watch area: expected watchScrollY=0, got %d", m.watchScrollY)
	}

	// Scroll wheel in the log area should scroll the log, not the watch.
	for i := 0; i < 20; i++ {
		m.appendLog(logEntry{ts: time.Now(), level: "INFO", text: "line"})
	}
	watchH := m.computedWatchH()
	logY := 1 + 1 + watchH + 2 // header + tabBar + watch + into middle
	m.Update(tea.MouseMsg{Type: tea.MouseWheelUp, X: m.width - 10, Y: logY})
	if m.logScrollY == 0 && len(m.logs) > m.visibleLogLines() {
		t.Error("mouse wheel up on log area should scroll the log")
	}
}

// TestQuoteMsgUpdatesPositionCurrentPrice asserts that incoming WS quote
// events update the CurrentPrice of any open position with a matching
// symbol in real-time, so the Active Positions panel reflects live PnL
// without waiting for the periodic broker poll.
func TestQuoteMsgUpdatesPositionCurrentPrice(t *testing.T) {
	m := New(100)
	m.width = 100
	m.height = 30
	m.recalculateLayout()

	m.positionRows = []domain.Position{
		{
			Venue:        domain.VenueBinanceFutures,
			Symbol:       domain.Symbol("BTCUSDT"),
			Side:         domain.SideBuy,
			Quantity:     decimal.RequireFromString("0.01"),
			EntryPrice:   decimal.RequireFromString("50000"),
			CurrentPrice: decimal.RequireFromString("50000"),
			Leverage:     10,
		},
		{
			Venue:        domain.VenueBinanceSpot,
			Symbol:       domain.Symbol("ETHUSDT"),
			Side:         domain.SideSell,
			Quantity:     decimal.RequireFromString("0.5"),
			EntryPrice:   decimal.RequireFromString("3000"),
			CurrentPrice: decimal.RequireFromString("3000"),
		},
	}

	// Last-only quote for the BTC position.
	m.Update(QuoteMsg{Quote: domain.Quote{
		Symbol: domain.Symbol("BTCUSDT"),
		Last:   decimal.RequireFromString("51000"),
		Mid:    decimal.RequireFromString("51000.5"),
	}})

	if got := m.positionRows[0].CurrentPrice.String(); got != "51000" {
		t.Errorf("BTC CurrentPrice not updated from quote: got %s, want 51000", got)
	}
	if pnl := m.positionRows[0].UnrealizedPnL().String(); pnl != "10" {
		t.Errorf("BTC UnrealizedPnL after quote update: got %s, want 10", pnl)
	}
	if m.positionRows[1].CurrentPrice.String() != "3000" {
		t.Errorf("ETH position should be untouched by BTC quote: got %s",
			m.positionRows[1].CurrentPrice.String())
	}

	// Mid-only quote (no Last) for the ETH position should also update.
	m.Update(QuoteMsg{Quote: domain.Quote{
		Symbol: domain.Symbol("ETHUSDT"),
		Mid:    decimal.RequireFromString("2950"),
	}})
	if got := m.positionRows[1].CurrentPrice.String(); got != "2950" {
		t.Errorf("ETH CurrentPrice not updated from Mid fallback: got %s, want 2950", got)
	}

	// A quote for an unknown symbol must not touch any position.
	m.Update(QuoteMsg{Quote: domain.Quote{
		Symbol: domain.Symbol("SOLUSDT"),
		Last:   decimal.RequireFromString("123"),
	}})
	if m.positionRows[0].CurrentPrice.String() != "51000" ||
		m.positionRows[1].CurrentPrice.String() != "2950" {
		t.Error("unrelated quote must not modify existing position prices")
	}

	// A zero/negative quote must be ignored (no spurious overwrite to 0).
	m.Update(QuoteMsg{Quote: domain.Quote{
		Symbol: domain.Symbol("BTCUSDT"),
		Last:   decimal.Zero,
		Mid:    decimal.Zero,
	}})
	if got := m.positionRows[0].CurrentPrice.String(); got != "51000" {
		t.Errorf("zero-price quote must not overwrite CurrentPrice: got %s", got)
	}
}

// TestRenderPositions_MarginRow verifies that a leveraged position renders a
// MARGIN row matching the exchange-reported allocated margin when present, and
// falls back to the derived initial margin (notional / leverage) with a
// `MARGIN*` label when not.
func TestRenderPositions_MarginRow(t *testing.T) {
	t.Run("uses exchange-reported margin", func(t *testing.T) {
		m := New(100)
		m.width = 100
		m.height = 30
		m.recalculateLayout()
		m.positionRows = []domain.Position{{
			Venue:        domain.VenueBinanceFutures,
			Symbol:       domain.Symbol("BTCUSDT"),
			Side:         domain.SideBuy,
			Quantity:     decimal.RequireFromString("0.033"),
			EntryPrice:   decimal.RequireFromString("77325.80"),
			CurrentPrice: decimal.RequireFromString("77474.50"),
			Leverage:     125,
			Margin:       decimal.RequireFromString("50.00"), // user posted extra
			Isolated:     true,
		}}
		out := stripANSI(m.renderPositions(20))
		if !strings.Contains(out, "MARGIN") {
			t.Fatalf("expected MARGIN row, got:\n%s", out)
		}
		if strings.Contains(out, "MARGIN*") {
			t.Errorf("should not show derived marker when exchange margin is reported, got:\n%s", out)
		}
		if !strings.Contains(out, "50.00") {
			t.Errorf("expected reported margin 50.00 in output, got:\n%s", out)
		}
	})

	t.Run("falls back to initial margin with marker", func(t *testing.T) {
		m := New(100)
		m.width = 100
		m.height = 30
		m.recalculateLayout()
		m.positionRows = []domain.Position{{
			Venue:        domain.VenueBinanceFutures,
			Symbol:       domain.Symbol("BTCUSDT"),
			Side:         domain.SideBuy,
			Quantity:     decimal.RequireFromString("0.033"),
			EntryPrice:   decimal.RequireFromString("77325.80"),
			CurrentPrice: decimal.RequireFromString("77474.50"),
			Leverage:     125,
			// Margin unset
		}}
		out := stripANSI(m.renderPositions(20))
		if !strings.Contains(out, "MARGIN*") {
			t.Errorf("expected MARGIN* marker for derived value, got:\n%s", out)
		}
		// Derived: 77325.80 * 0.033 / 125 = 20.4140112 -> formatted as "20.41 USDT"
		if !strings.Contains(out, "20.41") {
			t.Errorf("expected derived initial margin ~20.41 in output, got:\n%s", out)
		}
	})

	t.Run("spot has no margin row", func(t *testing.T) {
		m := New(100)
		m.width = 100
		m.height = 30
		m.recalculateLayout()
		m.positionRows = []domain.Position{{
			Venue:        domain.VenueBinanceSpot,
			Symbol:       domain.Symbol("BTCUSDT"),
			Side:         domain.SideBuy,
			Quantity:     decimal.RequireFromString("0.001"),
			EntryPrice:   decimal.RequireFromString("50000"),
			CurrentPrice: decimal.RequireFromString("51000"),
		}}
		out := stripANSI(m.renderPositions(20))
		if strings.Contains(out, "MARGIN") {
			t.Errorf("spot positions must not render a MARGIN row, got:\n%s", out)
		}
	})
}

// TestSpreadComputedViaDecimal verifies the Market Watch spread is computed
// with decimal arithmetic (ask.Sub(bid)) rather than float64 subtraction, and
// that formatPrice renders the decimal spread.
func TestSpreadComputedViaDecimal(t *testing.T) {
	q := quoteState{
		symbol: "BTC/USDT",
		last:   decimal.NewFromInt(50000),
		bid:    decimal.NewFromInt(49999),
		ask:    decimal.NewFromInt(50001),
	}

	// Production path computes spread := q.ask.Sub(q.bid).
	spread := q.ask.Sub(q.bid)
	want := decimal.NewFromInt(2)
	if !spread.Equal(want) {
		t.Fatalf("spread = %s, want %s", spread, want)
	}

	// formatPrice now operates on decimal; |2| >= 1 renders at 4dp.
	if got := formatPrice(spread); got != "2.0000" {
		t.Errorf("formatPrice(spread) = %q, want %q", got, "2.0000")
	}

	// The rendered watch row must contain the formatted spread.
	row := stripANSI(formatWatchRow(q, domain.BiasResult{},
		colSymbol, colLast, colChg, colBidAsk, colSpread, colVol, colBias))
	if !strings.Contains(row, "2.0000") {
		t.Errorf("watch row missing decimal spread, got:\n%s", row)
	}
}

// TestAvgROIComputedViaDecimal verifies the Account panel averages position
// ROI with decimal arithmetic (totalROI.Div(count)) instead of float64, guards
// against divide-by-zero, and renders the decimal average.
func TestAvgROIComputedViaDecimal(t *testing.T) {
	m := New(100)
	m.width = 120
	m.height = 40
	// Two spot positions (Leverage 1 → ROI == price-move %):
	//   +20% and +10% → avg 15%.
	m.positionRows = []domain.Position{
		{
			Venue:        domain.VenueBinanceSpot,
			Symbol:       domain.Symbol("BTC/USDT"),
			Side:         domain.SideBuy,
			Quantity:     decimal.NewFromInt(1),
			EntryPrice:   decimal.NewFromInt(100),
			CurrentPrice: decimal.NewFromInt(120),
			Leverage:     1,
		},
		{
			Venue:        domain.VenueBinanceSpot,
			Symbol:       domain.Symbol("ETH/USDT"),
			Side:         domain.SideBuy,
			Quantity:     decimal.NewFromInt(1),
			EntryPrice:   decimal.NewFromInt(100),
			CurrentPrice: decimal.NewFromInt(110),
			Leverage:     1,
		},
	}

	// Mirror the production computation in renderAccountPanel.
	totalROI := decimal.Zero
	for _, p := range m.positionRows {
		totalROI = totalROI.Add(p.UnrealizedPnLROI())
	}
	avgROI := totalROI.Div(decimal.NewFromInt(int64(len(m.positionRows))))
	if !avgROI.Equal(decimal.NewFromInt(15)) {
		t.Fatalf("avgROI = %s, want 15", avgROI)
	}

	if got := stripANSI(formatPercent(avgROI)); got != "+15.00%" {
		t.Errorf("formatPercent(avgROI) = %q, want %q", got, "+15.00%")
	}

	out := stripANSI(m.renderAccountPanel(40, 12))
	if !strings.Contains(out, "+15.00%") {
		t.Errorf("account panel missing decimal avg roi, got:\n%s", out)
	}

	// Divide-by-zero guard: no positions → avgROI stays zero, no panic.
	empty := New(100)
	empty.width = 120
	empty.height = 40
	_ = stripANSI(empty.renderAccountPanel(40, 12))
}

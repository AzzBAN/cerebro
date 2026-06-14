package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/azhar/cerebro/internal/domain"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/shopspring/decimal"
)

func TestBreakpoint_Selection(t *testing.T) {
	tests := []struct {
		name   string
		width  int
		height int
		want   breakpoint
	}{
		{"tiny 50x16", 50, 16, bpXS},
		{"sm 80x24 (current default)", 80, 24, bpSM},
		{"sm 120x40", 120, 40, bpSM},
		{"md 140x35", 140, 35, bpMD},
		{"md 180x45", 180, 45, bpMD},
		{"lg 200x50", 200, 50, bpLG},
		{"lg 230x60", 230, 60, bpLG},
		{"xl 260x65", 260, 65, bpXL},
		{"xl 300x80", 300, 80, bpXL},
		// Falls back when only one dimension qualifies.
		{"wide but short 250x30", 250, 30, bpSM},
		{"tall but narrow 80x80", 80, 80, bpSM},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := New(500)
			m.width = tt.width
			m.height = tt.height
			if got := m.breakpoint(); got != tt.want {
				t.Errorf("breakpoint(%d,%d) = %d, want %d", tt.width, tt.height, got, tt.want)
			}
		})
	}
}

func TestView_AllBreakpointsHonourTerminalHeight(t *testing.T) {
	tests := []struct {
		name   string
		width  int
		height int
	}{
		{"xs 50x16", 50, 16},
		{"sm 100x30", 100, 30},
		{"md 160x40", 160, 40},
		{"lg 220x55", 220, 55},
		{"xl 280x70", 280, 70},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := seededModel(tt.width, tt.height)
			view := m.View()
			got := countLines(view)
			if got != tt.height {
				t.Errorf("View() height = %d, want exactly %d", got, tt.height)
			}
		})
	}
}

func TestView_AllBreakpointsHonourTerminalWidth(t *testing.T) {
	tests := []struct {
		name   string
		width  int
		height int
	}{
		{"sm 100x30", 100, 30},
		{"md 160x40", 160, 40},
		{"lg 220x55", 220, 55},
		{"xl 280x70", 280, 70},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := seededModel(tt.width, tt.height)
			view := m.View()
			plain := stripANSI(view)
			for i, line := range strings.Split(strings.TrimRight(plain, "\n"), "\n") {
				if w := visibleRuneCount(line); w > tt.width {
					t.Errorf("line %d width %d > terminal width %d: %q", i, w, tt.width, line)
				}
			}
		})
	}
}

func TestView_MD_Has3PanelColumns(t *testing.T) {
	m := seededModel(160, 45)
	view := stripANSI(m.View())
	for _, want := range []string{"Active Positions", "Macro", "Bias / Signals", "Agent Runs", "Activity & Log"} {
		if !strings.Contains(view, want) {
			t.Errorf("MD view missing panel %q", want)
		}
	}
}

func TestView_LG_HasFourColumnPanels(t *testing.T) {
	m := seededModel(220, 55)
	view := stripANSI(m.View())
	for _, want := range []string{"Active Positions", "Account", "Bias / Signals", "Agent Runs", "Activity & Log", "Macro", "News"} {
		if !strings.Contains(view, want) {
			t.Errorf("LG view missing panel %q", want)
		}
	}
}

func TestView_XL_HasFiveColumnPanels(t *testing.T) {
	m := seededModel(280, 75)
	view := stripANSI(m.View())
	for _, want := range []string{"Active Positions", "Account", "Bias / Signals", "Agent Runs", "Activity & Log", "Macro", "News", "Health"} {
		if !strings.Contains(view, want) {
			t.Errorf("XL view missing panel %q", want)
		}
	}
}

func TestView_XS_ShowsTabsAndOnePanelAtATime(t *testing.T) {
	// Narrow width: tabs use abbreviated labels.
	m := seededModel(50, 16)
	view := stripANSI(m.View())
	for _, want := range xsTabShort {
		if !strings.Contains(view, want) {
			t.Errorf("XS narrow view missing short tab label %q\n%s", want, view)
		}
	}
	// Only the active tab's content panel should appear; other middle panels are hidden.
	if strings.Contains(view, "Active Positions") {
		t.Errorf("XS view should not show Positions panel when Market tab is active")
	}

	// Wider XS (still short on height): full labels.
	m2 := seededModel(90, 16)
	view2 := stripANSI(m2.View())
	for _, want := range xsTabLabels {
		if !strings.Contains(view2, want) {
			t.Errorf("XS wide view missing full tab label %q", want)
		}
	}
}

func TestXSTabCyclesWithTab(t *testing.T) {
	m := New(500)
	m.width = 50
	m.height = 16
	if got := m.breakpoint(); got != bpXS {
		t.Fatalf("expected bpXS, got %d", got)
	}
	for i := 0; i < xsTabCount; i++ {
		if m.xsTab != i {
			t.Errorf("after %d Tab presses, xsTab=%d, want %d", i, m.xsTab, i)
		}
		next, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
		nm := next.(*Model)
		m = *nm
	}
	if m.xsTab != 0 {
		t.Errorf("xsTab should wrap to 0, got %d", m.xsTab)
	}
}

func TestBiasPanel_RendersScores(t *testing.T) {
	m := New(500)
	m.width = 160
	m.height = 45
	now := time.Now()
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

	view := stripANSI(m.View())
	if !strings.Contains(view, "BTC/USDT") || !strings.Contains(view, "Bullish") {
		t.Errorf("bias panel missing BTC/USDT Bullish row\n%s", view)
	}
	if !strings.Contains(view, "ETH/USDT") || !strings.Contains(view, "Bearish") {
		t.Errorf("bias panel missing ETH/USDT Bearish row")
	}
}

func TestMacroPanel_RendersIndicators(t *testing.T) {
	m := New(500)
	m.width = 160
	m.height = 45
	now := time.Now()
	m.Update(MacroSnapshotMsg{Snapshot: MacroSnapshot{
		FearGreed: domain.FearGreedIndex{Value: 29, Category: "Fear", FetchedAt: now},
		BTCFundingRate: domain.FundingRate{
			Symbol: domain.Symbol("BTC/USDT-PERP"), Rate: -0.000033, FetchedAt: now,
		},
		BTCOpenInterest: domain.OpenInterest{
			Symbol:   domain.Symbol("BTC/USDT-PERP"),
			TotalUSD: decimal.NewFromInt(54_000_000_000), Change24h: 1.2, FetchedAt: now,
		},
		BTCLongShort: domain.LongShortRatio{
			Symbol: domain.Symbol("BTC/USDT-PERP"), GlobalRatio: 0.98, FetchedAt: now,
		},
		UpdatedAt: now,
	}})

	view := stripANSI(m.View())
	for _, want := range []string{"F&G", "29", "Fund.", "OI", "L/S"} {
		if !strings.Contains(view, want) {
			t.Errorf("macro panel missing %q\n%s", want, view)
		}
	}
}

func TestAgentRunsPanel_ShowsCompletedRuns(t *testing.T) {
	m := New(500)
	m.width = 160
	m.height = 45

	now := time.Now()
	m.Update(AgentStateMsg{
		Agent: "screening", RunID: "r1", Symbol: "BTC/USDT",
		Step: StepThinking, At: now.Add(-30 * time.Second),
	})
	m.Update(AgentStateMsg{
		Agent: "screening", RunID: "r1", Symbol: "BTC/USDT",
		Step: StepComplete, Content: "ok", At: now,
	})

	view := stripANSI(m.View())
	if !strings.Contains(view, "Agent Runs") {
		t.Fatalf("missing Agent Runs panel")
	}
	if !strings.Contains(view, "screening") {
		t.Errorf("Agent Runs panel missing run for screening agent\n%s", view)
	}
}

// ─── helpers ──────────────────────────────────────────────────────────────────

// seededModel returns a Model populated with sample data so that all panels
// have something to render at any breakpoint.
func seededModel(width, height int) Model {
	m := New(500)
	m.width = width
	m.height = height
	m.now = time.Now()

	for i, sym := range []string{"BTC/USDT", "ETH/USDT", "SOL/USDT", "XAU/USDT-PERP"} {
		m.quotes[sym] = quoteState{
			symbol:             sym,
			last:               decimal.NewFromFloat(100 + float64(i)*50),
			bid:                decimal.NewFromFloat(99 + float64(i)*50),
			ask:                decimal.NewFromFloat(101 + float64(i)*50),
			priceChange:        decimal.NewFromFloat(1.5),
			priceChangePercent: decimal.NewFromFloat(0.75),
			volume24h:          decimal.NewFromFloat(1e9),
		}
	}

	for i := 0; i < 20; i++ {
		m.appendLog(logEntry{
			ts:    time.Now().Add(-time.Duration(i) * time.Second),
			level: "INFO",
			text:  "screening: bias updated symbol=BTC/USDT score=Bullish",
		})
	}

	now := time.Now()
	m.biasResults[domain.Symbol("BTC/USDT")] = domain.BiasResult{
		Symbol: domain.Symbol("BTC/USDT"), Score: domain.BiasBullish,
		CachedAt: now, ExpiresAt: now.Add(15 * time.Minute),
	}
	m.biasOrder = []domain.Symbol{domain.Symbol("BTC/USDT")}

	m.macro = MacroSnapshot{
		FearGreed:       domain.FearGreedIndex{Value: 29, Category: "Fear", FetchedAt: now},
		BTCFundingRate:  domain.FundingRate{Rate: -0.000033, FetchedAt: now},
		BTCOpenInterest: domain.OpenInterest{TotalUSD: decimal.NewFromInt(54_000_000_000), Change24h: 1.2, FetchedAt: now},
		BTCLongShort:    domain.LongShortRatio{GlobalRatio: 0.98, FetchedAt: now},
		UpdatedAt:       now,
	}
	m.macroSet = true

	m.heartbeat = "state=running halted=false pos=0 spot=0 futures=0 candles=0 signals=0 orders=0"
	m.heartbeatAt = time.Now()

	m.recalculateLayout()
	return m
}

// visibleRuneCount returns the number of runes in s (after ANSI strip).
func visibleRuneCount(s string) int {
	return len([]rune(s))
}

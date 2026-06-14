package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/azhar/cerebro/internal/domain"
	"github.com/shopspring/decimal"
)

// TestRepro_LayoutFitsManyAgents asserts the layout stays bounded across the
// whole range of plausible agent activity counts. Regression guard: the
// previous version overflowed once concurrent screening agents exceeded the
// Agent Runs panel's content height (column wrap + MaxHeight clipped the
// bottom border, then the joined center column exceeded `middleH`).
func TestRepro_LayoutFitsManyAgents(t *testing.T) {
	for _, agents := range []int{0, 1, 3, 5, 7, 10, 15, 20} {
		for _, dim := range []struct{ w, h int }{
			{100, 30}, {160, 40}, {220, 55}, {280, 70}, {156, 45},
		} {
			name := fmt.Sprintf("%dagents_%dx%d", agents, dim.w, dim.h)
			t.Run(name, func(t *testing.T) {
				m := New(500)
				m.width = dim.w
				m.height = dim.h
				m.now = time.Now()
				now := time.Now()
				for i := 0; i < agents; i++ {
					m.Update(AgentStateMsg{
						Agent: "screening", RunID: fmt.Sprintf("r%d", i),
						Step: StepTool, Provider: "openai_compatible",
						Model: "minimax-m2.5", Description: "Fetching latest news",
						Symbol: "BTC/USDT", StepNum: 4, MaxSteps: 10,
						At: now.Add(-time.Duration(i) * time.Second),
					})
				}
				m.heartbeat = "state=running"
				m.heartbeatAt = now
				m.recalculateLayout()

				view := m.View()
				plain := stripANSI(view)
				lines := strings.Split(strings.TrimRight(plain, "\n"), "\n")
				if got := len(lines); got != dim.h {
					t.Errorf("agents=%d view height = %d, want %d", agents, got, dim.h)
				}
				for i, line := range lines {
					if w := visibleRuneCount(line); w > dim.w {
						t.Errorf("line %d width %d > %d: %q", i, w, dim.w, line)
					}
				}
				topBorders := strings.Count(plain, "╭")
				bottomBorders := strings.Count(plain, "╰")
				if topBorders != bottomBorders {
					t.Errorf("agents=%d unbalanced borders top=%d bot=%d",
						agents, topBorders, bottomBorders)
				}
			})
		}
	}
}

// TestRepro_DemoStateLayoutFits replicates the "many active agents + many
// quotes + multiple positions" state shown in the user's screenshot, and
// asserts the rendered TUI fits within the terminal bounds with no overflow.
func TestRepro_DemoStateLayoutFits(t *testing.T) {
	cases := []struct {
		name   string
		width  int
		height int
	}{
		{"sm 100x30", 100, 30},
		{"md 160x40", 160, 40},
		{"lg 220x55", 220, 55},
		{"xl 280x70", 280, 70},
		{"actual screenshot 156x45", 156, 45},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := New(500)
			m.width = c.width
			m.height = c.height
			m.now = time.Now()

			// 7 quotes (matches screenshot).
			for i, sym := range []string{"BTC/USDT", "BTC/USDT-PERP", "ETH/USDT", "XAU/USDT-PERP", "SOL/USDT", "BNB/USDT", "DOGE/USDT"} {
				m.quotes[sym] = quoteState{
					symbol: sym, last: decimal.NewFromFloat(100 + float64(i)*50),
					bid: decimal.NewFromFloat(99 + float64(i)*50), ask: decimal.NewFromFloat(101 + float64(i)*50),
					priceChange: decimal.NewFromFloat(1.5), priceChangePercent: decimal.NewFromFloat(0.75), volume24h: decimal.NewFromFloat(1e9),
				}
			}

			// 1 open position (matches screenshot).
			m.positionRows = []domain.Position{
				{
					Venue:        domain.VenueBinanceFutures,
					Symbol:       "BTC/USDT-PERP",
					Side:         domain.SideBuy,
					Quantity:     decimal.RequireFromString("0.033"),
					EntryPrice:   decimal.RequireFromString("77325.80"),
					CurrentPrice: decimal.RequireFromString("77311.70"),
				},
			}

			// 5 concurrent active agents (matches screenshot).
			now := time.Now()
			descriptions := []string{
				"Fetching latest news",
				"Fetching latest news",
				"Fetching latest news",
				"Fetching derivatives data",
				"Fetching latest news",
			}
			for i, desc := range descriptions {
				m.Update(AgentStateMsg{
					Agent: "screening", RunID: fmt.Sprintf("r%d", i),
					Step: StepTool, Provider: "openai_compatible",
					Model: "minimax-m2.5", Description: desc,
					Symbol: "BTC/USDT", StepNum: 4, MaxSteps: 10,
					At: now.Add(-time.Duration(33-i) * time.Second),
				})
			}

			// 50+ log entries.
			for i := 0; i < 50; i++ {
				m.appendLog(logEntry{
					ts:    time.Now().Add(-time.Duration(50-i) * time.Second),
					level: "INFO",
					text:  "strategy warmup: fetched historical klines symbol=BTC/USDT-PERP",
				})
			}

			m.heartbeat = "state=running halted=false pos=1 spot=0 futures=1 candles=0 signals=0 orders=0"
			m.heartbeatAt = time.Now()
			m.recalculateLayout()

			view := m.View()
			plain := stripANSI(view)
			lines := strings.Split(strings.TrimRight(plain, "\n"), "\n")

			// CHECK 1: total height fits.
			if got := len(lines); got != c.height {
				t.Errorf("total view lines = %d, want exactly %d\n%s", got, c.height, plain)
			}

			// CHECK 2: no line exceeds terminal width.
			for i, line := range lines {
				if w := visibleRuneCount(line); w > c.width {
					t.Errorf("line %d width %d > terminal width %d: %q", i, w, c.width, line)
				}
			}

			// CHECK 3: every panel border that appears has matching top + bottom.
			topBorders := strings.Count(plain, "╭")
			bottomBorders := strings.Count(plain, "╰")
			if topBorders != bottomBorders {
				t.Errorf("unbalanced rounded borders: top=%d, bottom=%d (some panel had its bottom border clipped by MaxHeight)\n%s",
					topBorders, bottomBorders, plain)
			}
		})
	}
}

// TestRepro_DashboardLogShowsLatestEntries asserts that the Activity & Log
// panel on the Dashboard tab shows the most recently appended log lines, even
// when individual entries are long enough to wrap inside the (narrow) log
// column. Previously, renderLogPanel selected the latest N entries by count,
// joined them, and let the caller's truncateLines crop from the top — which
// silently dropped the most recent (bottom) wrapped lines and left only old
// entries visible. The Logs tab uses the full terminal width so wrapping
// rarely happened there, hiding the bug.
func TestRepro_DashboardLogShowsLatestEntries(t *testing.T) {
	for _, c := range []struct {
		name   string
		width  int
		height int
	}{
		{"sm 100x30", 100, 30},
		{"md 160x40", 160, 40},
		{"lg 220x55", 220, 55},
	} {
		t.Run(c.name, func(t *testing.T) {
			m := New(500)
			m.width = c.width
			m.height = c.height
			m.now = time.Now()

			// Append many long entries that will wrap when rendered
			// inside the narrow Dashboard log column. The marker token
			// `entry-NN` lets us assert which entries are visible.
			const total = 30
			longText := strings.Repeat("strategy warmup fetched klines symbol=BTC/USDT-PERP ", 3)
			for i := 0; i < total; i++ {
				m.appendLog(logEntry{
					ts:    time.Now().Add(time.Duration(i) * time.Second),
					level: "INFO",
					text:  fmt.Sprintf("entry-%02d %s", i, longText),
				})
			}
			m.heartbeat = "state=running"
			m.heartbeatAt = time.Now()
			m.recalculateLayout()

			plain := stripANSI(m.View())

			// The most recent entry must appear somewhere on the
			// Dashboard. If it doesn't, the log panel cropped from
			// the top and lost the latest line.
			latest := fmt.Sprintf("entry-%02d", total-1)
			if !strings.Contains(plain, latest) {
				t.Errorf("Dashboard log panel does not show most recent entry %q\n%s",
					latest, plain)
			}
		})
	}
}

package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbletea"
)

func TestClampToRect(t *testing.T) {
	r := rect{x0: 5, y0: 2, x1: 15, y1: 8} // cells x:5..14, y:2..7

	tests := []struct {
		name string
		in   point
		want point
	}{
		{"inside", point{8, 4}, point{8, 4}},
		{"left edge", point{5, 4}, point{5, 4}},
		{"right edge", point{14, 4}, point{14, 4}},
		{"top edge", point{8, 2}, point{8, 2}},
		{"bottom edge", point{8, 7}, point{8, 7}},
		{"left of rect", point{0, 4}, point{5, 4}},
		{"right of rect", point{99, 4}, point{14, 4}},
		{"above rect", point{8, 0}, point{8, 2}},
		{"below rect", point{8, 99}, point{8, 7}},
		{"top-left corner over", point{-3, -3}, point{5, 2}},
		{"bottom-right corner over", point{99, 99}, point{14, 7}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := clampToRect(tt.in, r); got != tt.want {
				t.Errorf("clampToRect(%v) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestNormalizeSelection(t *testing.T) {
	tests := []struct {
		name                   string
		a, b                   point
		wx0, wy0, wx1, wy1     int
	}{
		{"top-left to bottom-right", point{2, 3}, point{8, 6}, 2, 3, 8, 6},
		{"bottom-right to top-left", point{8, 6}, point{2, 3}, 2, 3, 8, 6},
		{"horizontal flip only", point{8, 3}, point{2, 6}, 2, 3, 8, 6},
		{"vertical flip only", point{2, 6}, point{8, 3}, 2, 3, 8, 6},
		{"same point", point{5, 5}, point{5, 5}, 5, 5, 5, 5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			x0, y0, x1, y1 := normalizeSelection(tt.a, tt.b)
			if x0 != tt.wx0 || y0 != tt.wy0 || x1 != tt.wx1 || y1 != tt.wy1 {
				t.Errorf("normalize(%v, %v) = (%d,%d,%d,%d), want (%d,%d,%d,%d)",
					tt.a, tt.b, x0, y0, x1, y1, tt.wx0, tt.wy0, tt.wx1, tt.wy1)
			}
		})
	}
}

func TestExtractRectFromView_PlainText(t *testing.T) {
	view := strings.Join([]string{
		"abcdefghij",
		"klmnopqrst",
		"uvwxyz0123",
		"4567890123",
	}, "\n")

	tests := []struct {
		name           string
		x0, y0, x1, y1 int
		want           string
	}{
		{"full first row", 0, 0, 9, 0, "abcdefghij"},
		{"middle block", 2, 1, 5, 2, "mnop\nwxyz"},
		{"single cell", 3, 0, 3, 0, "d"},
		{"trailing spaces trimmed", 0, 0, 11, 0, "abcdefghij"}, // x1 past line end
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractRectFromView(view, tt.x0, tt.y0, tt.x1, tt.y1)
			if got != tt.want {
				t.Errorf("extract = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestExtractRectFromView_StripsANSI(t *testing.T) {
	// ANSI-styled content: red "FOO" + reset + " BAR".
	line := "\x1b[31mFOO\x1b[0m BAR"
	got := extractRectFromView(line, 0, 0, 6, 0)
	if got != "FOO BAR" {
		t.Errorf("extract with ANSI = %q, want %q", got, "FOO BAR")
	}
}

func TestExtractRectFromView_TrailingEmptyLinesRemoved(t *testing.T) {
	view := strings.Join([]string{
		"hello",
		"",
		"",
	}, "\n")
	got := extractRectFromView(view, 0, 0, 4, 2)
	if got != "hello" {
		t.Errorf("expected trailing blanks removed, got %q", got)
	}
}

func TestApplySelectionOverlay(t *testing.T) {
	view := strings.Join([]string{
		"aaaaaaaaaa",
		"bbbbbbbbbb",
		"cccccccccc",
	}, "\n")

	out := applySelectionOverlay(view, 2, 1, 5, 1) // row 1, cols 2..5

	lines := strings.Split(out, "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %d", len(lines))
	}
	// Untouched lines should match exactly.
	if lines[0] != "aaaaaaaaaa" {
		t.Errorf("row 0 modified unexpectedly: %q", lines[0])
	}
	if lines[2] != "cccccccccc" {
		t.Errorf("row 2 modified unexpectedly: %q", lines[2])
	}
	// Plain text of the selected row must round-trip (overlay preserves
	// content; only styling/escape sequences may be added depending on the
	// active color profile).
	if plain := stripANSI(lines[1]); plain != "bbbbbbbbbb" {
		t.Errorf("plain text after overlay = %q, want bbbbbbbbbb", plain)
	}
}

func TestApplySelectionOverlay_PadsShortLines(t *testing.T) {
	// Selection extends past the actual content in the row; overlay should
	// still produce a row of at least the selection width when stripped.
	view := "abc" // only 3 chars wide
	out := applySelectionOverlay(view, 0, 0, 6, 0)
	plain := stripANSI(out)
	if len(plain) < 7 {
		t.Errorf("expected padded row to be at least 7 cells, got %q (len=%d)", plain, len(plain))
	}
	if !strings.HasPrefix(plain, "abc") {
		t.Errorf("expected row to start with original content, got %q", plain)
	}
}

func TestPanelRect_DashboardWatch(t *testing.T) {
	m := New(500)
	m.width = 120
	m.height = 40
	m.now = time.Now()
	m.recalculateLayout()

	r := m.panelRect(focusWatch)
	// Watch starts at headerH(1) + tabBarH(1) = y=2, height = computedWatchH().
	if r.x0 != 0 || r.x1 != m.width {
		t.Errorf("watch rect should span full width, got x:[%d,%d)", r.x0, r.x1)
	}
	if r.y0 != 2 {
		t.Errorf("watch rect y0 should be 2, got %d", r.y0)
	}
	if r.y1 != 2+m.computedWatchH() {
		t.Errorf("watch rect y1 should be %d, got %d", 2+m.computedWatchH(), r.y1)
	}
}

func TestPanelRect_Disjoint(t *testing.T) {
	// Different panels should not overlap on the dashboard.
	m := New(500)
	m.width = 200
	m.height = 50
	m.now = time.Now()
	m.recalculateLayout()

	rectsToCheck := []panelFocus{focusWatch, focusPositions, focusBias, focusLog, focusMacro, focusAgentActivity}
	rs := make(map[panelFocus]rect)
	for _, p := range rectsToCheck {
		r := m.panelRect(p)
		if !r.empty() {
			rs[p] = r
		}
	}
	for a, ra := range rs {
		for b, rb := range rs {
			if a >= b {
				continue
			}
			if rectsOverlap(ra, rb) {
				t.Errorf("panels %v and %v overlap: %+v vs %+v", a, b, ra, rb)
			}
		}
	}
}

func rectsOverlap(a, b rect) bool {
	if a.x1 <= b.x0 || b.x1 <= a.x0 {
		return false
	}
	if a.y1 <= b.y0 || b.y1 <= a.y0 {
		return false
	}
	return true
}

func TestSelection_StartsOnLeftClick(t *testing.T) {
	m := New(500)
	m.width = 120
	m.height = 40
	m.now = time.Now()
	m.recalculateLayout()

	// Click in the watch panel area.
	m.Update(tea.MouseMsg{Type: tea.MouseLeft, X: 10, Y: 3})
	if !m.selecting {
		t.Fatal("expected selecting=true after left click in panel with mouse on")
	}
	if m.selStart != m.selEnd {
		t.Errorf("expected start==end after press, got start=%v end=%v", m.selStart, m.selEnd)
	}
	if m.selRect.empty() {
		t.Error("expected non-empty selRect after press in panel")
	}
}

func TestSelection_DragClampsToPanelRect(t *testing.T) {
	m := New(500)
	m.width = 120
	m.height = 40
	m.now = time.Now()
	m.recalculateLayout()

	// Press inside watch panel.
	m.Update(tea.MouseMsg{Type: tea.MouseLeft, X: 10, Y: 3})
	r := m.selRect

	// Drag far past the panel boundary in every direction.
	m.Update(tea.MouseMsg{Type: tea.MouseLeft, Action: tea.MouseActionMotion, X: 9999, Y: 9999})
	if m.selEnd.x != r.x1-1 {
		t.Errorf("drag right should clamp to panel right edge %d, got %d", r.x1-1, m.selEnd.x)
	}
	if m.selEnd.y != r.y1-1 {
		t.Errorf("drag down should clamp to panel bottom edge %d, got %d", r.y1-1, m.selEnd.y)
	}

	m.Update(tea.MouseMsg{Type: tea.MouseLeft, Action: tea.MouseActionMotion, X: -100, Y: -100})
	if m.selEnd.x != r.x0 {
		t.Errorf("drag left should clamp to panel left edge %d, got %d", r.x0, m.selEnd.x)
	}
	if m.selEnd.y != r.y0 {
		t.Errorf("drag up should clamp to panel top edge %d, got %d", r.y0, m.selEnd.y)
	}
}

func TestSelection_ReleaseEndsSelection(t *testing.T) {
	m := New(500)
	m.width = 120
	m.height = 40
	m.now = time.Now()
	m.recalculateLayout()

	m.Update(tea.MouseMsg{Type: tea.MouseLeft, X: 10, Y: 3})
	m.Update(tea.MouseMsg{Type: tea.MouseLeft, Action: tea.MouseActionMotion, X: 30, Y: 5})
	if !m.selecting {
		t.Fatal("expected selecting=true mid-drag")
	}

	m.Update(tea.MouseMsg{Type: tea.MouseRelease, Action: tea.MouseActionRelease})
	if m.selecting {
		t.Error("expected selecting=false after release")
	}
}

func TestSelection_DisabledWhenMouseOff(t *testing.T) {
	m := New(500)
	m.width = 120
	m.height = 40
	m.now = time.Now()
	m.recalculateLayout()
	m.mouseEnabled = false

	m.Update(tea.MouseMsg{Type: tea.MouseLeft, X: 10, Y: 3})
	if m.selecting {
		t.Error("selection should not start when mouse mode is off")
	}
}

func TestSelection_CtrlOTogglesAndCancels(t *testing.T) {
	m := New(500)
	m.width = 120
	m.height = 40
	m.now = time.Now()
	m.recalculateLayout()

	// Start a drag.
	m.Update(tea.MouseMsg{Type: tea.MouseLeft, X: 10, Y: 3})
	m.Update(tea.MouseMsg{Type: tea.MouseLeft, Action: tea.MouseActionMotion, X: 30, Y: 4})
	if !m.selecting {
		t.Fatal("expected selecting=true mid-drag")
	}

	// Ctrl+O should disable mouse mode and cancel the in-flight selection.
	m.Update(tea.KeyMsg{Type: tea.KeyCtrlO})
	if m.mouseEnabled {
		t.Error("Ctrl+O should toggle mouse off")
	}
	if m.selecting {
		t.Error("Ctrl+O should cancel in-flight selection")
	}
}

func TestOSC52SetEncoding(t *testing.T) {
	got := osc52Set("hello")
	want := "\x1b]52;c;aGVsbG8=\x07"
	if got != want {
		t.Errorf("osc52Set(\"hello\") = %q, want %q", got, want)
	}
}

func TestFormatCopyNotice(t *testing.T) {
	tests := []struct {
		n    int
		want string
	}{
		{0, "copied 0 chars"},
		{1, "copied 1 char"},
		{42, "copied 42 chars"},
	}
	for _, tt := range tests {
		if got := formatCopyNotice(tt.n); got != tt.want {
			t.Errorf("formatCopyNotice(%d) = %q, want %q", tt.n, got, tt.want)
		}
	}
}

package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// point is a screen cell coordinate.
type point struct {
	x, y int
}

// rect is a half-open screen rectangle: x in [x0, x1), y in [y0, y1).
type rect struct {
	x0, y0, x1, y1 int
}

func (r rect) empty() bool { return r.x1 <= r.x0 || r.y1 <= r.y0 }

func (r rect) contains(p point) bool {
	return p.x >= r.x0 && p.x < r.x1 && p.y >= r.y0 && p.y < r.y1
}

// clamp clips n to [lo, hi].
func clampInt(n, lo, hi int) int {
	if n < lo {
		return lo
	}
	if n > hi {
		return hi
	}
	return n
}

// clampToRect clips p to the cell range of r (treating r as inclusive on the
// last cell). Assumes r is non-empty; callers must check.
func clampToRect(p point, r rect) point {
	return point{
		x: clampInt(p.x, r.x0, r.x1-1),
		y: clampInt(p.y, r.y0, r.y1-1),
	}
}

// normalizeSelection returns the inclusive rectangle spanned by a..b as
// (x0, y0, x1, y1) with x0<=x1 and y0<=y1.
func normalizeSelection(a, b point) (int, int, int, int) {
	x0, x1 := a.x, b.x
	if x1 < x0 {
		x0, x1 = x1, x0
	}
	y0, y1 := a.y, b.y
	if y1 < y0 {
		y0, y1 = y1, y0
	}
	return x0, y0, x1, y1
}

// panelRect returns the screen rectangle for the given panel under the
// current layout. Returns the zero rect for panels not visible in the
// active layout.
func (m *Model) panelRect(panel panelFocus) rect {
	if m.height == 0 || m.width == 0 || panel == focusNone {
		return rect{}
	}

	// Non-dashboard tabs and XS layout fill the body with a single panel.
	if m.activeTab != tabDashboard || m.breakpoint() == bpXS {
		bodyH := m.bodyHeight()
		headerH := 1
		tabBarH := 1
		if m.breakpoint() == bpXS {
			tabBarH = 0
		}
		y0 := headerH + tabBarH
		y1 := y0 + bodyH
		return rect{0, y0, m.width, y1}
	}

	headerH := 1
	tabBarH := 1
	watchH := m.computedWatchH()
	agentH := m.computedAgentPanelH()
	bodyH := m.bodyHeight()
	middleH := bodyH - watchH - agentH
	if middleH < 0 {
		middleH = 0
	}

	watchStart := headerH + tabBarH
	watchEnd := watchStart + watchH
	middleStart := watchEnd
	middleEnd := middleStart + middleH
	agentStart := middleEnd
	agentEnd := agentStart + agentH

	switch panel {
	case focusWatch:
		return rect{0, watchStart, m.width, watchEnd}
	case focusAgentActivity:
		return rect{0, agentStart, m.width, agentEnd}
	case focusLog, focusPositions, focusBias, focusMacro:
		x0, x1 := m.middleColumnXBounds(panel)
		return rect{x0, middleStart, x1, middleEnd}
	}
	return rect{}
}

// middleColumnXBounds returns the [x0, x1) horizontal extent of the given
// middle-row panel under the current breakpoint. Mirrors middleColumnAtX.
func (m *Model) middleColumnXBounds(panel panelFocus) (int, int) {
	bp := m.breakpoint()
	switch bp {
	case bpSM:
		total := m.width - 2*borderH
		c1 := total/3 + borderH
		switch panel {
		case focusPositions:
			return 0, c1
		case focusLog:
			return c1, m.width
		}
	case bpMD:
		total := m.width - 3*borderH
		c1 := total*22/100 + borderH
		c2 := total*28/100 + borderH
		switch panel {
		case focusPositions:
			return 0, c1
		case focusBias:
			return c1, c1 + c2
		case focusLog:
			return c1 + c2, m.width
		}
	case bpLG:
		total := m.width - 4*borderH
		c1 := total*20/100 + borderH
		c2 := total*22/100 + borderH
		c4 := total*22/100 + borderH
		c3 := m.width - c1 - c2 - c4
		switch panel {
		case focusPositions:
			return 0, c1
		case focusBias:
			return c1, c1 + c2
		case focusLog:
			return c1 + c2, c1 + c2 + c3
		case focusMacro:
			return c1 + c2 + c3, m.width
		}
	case bpXL:
		total := m.width - 5*borderH
		c1 := total*16/100 + borderH
		c2 := total*20/100 + borderH
		c4 := total*20/100 + borderH
		c5 := total*16/100 + borderH
		c3 := m.width - c1 - c2 - c4 - c5
		switch panel {
		case focusPositions:
			return 0, c1
		case focusBias:
			return c1, c1 + c2
		case focusLog:
			return c1 + c2, c1 + c2 + c3
		case focusMacro:
			return c1 + c2 + c3, c1 + c2 + c3 + c4
		}
	}
	return 0, m.width
}

// selectionStyle highlights selected cells. Reverse video preserves any
// monochrome content while cleanly inverting colored text once we strip
// inner styles (see applySelectionOverlay).
var selectionStyle = lipgloss.NewStyle().
	Background(lipgloss.Color("12")). // bright blue
	Foreground(lipgloss.Color("15"))  // bright white

// applySelectionOverlay returns view with the cells inside the selection
// rectangle re-styled. The selection is identified by inclusive cell
// coordinates (x0,y0)..(x1,y1).
func applySelectionOverlay(view string, x0, y0, x1, y1 int) string {
	if y1 < y0 || x1 < x0 {
		return view
	}
	lines := strings.Split(view, "\n")
	for y := y0; y <= y1 && y < len(lines); y++ {
		lines[y] = overlayLine(lines[y], x0, x1)
	}
	return strings.Join(lines, "\n")
}

// overlayLine highlights cells [x0, x1] in the line.
func overlayLine(line string, x0, x1 int) string {
	w := ansi.StringWidth(line)
	innerW := x1 - x0 + 1

	left := ansi.Cut(line, 0, x0)

	// Capture the selection slice; pad with spaces if the line is shorter.
	var inner string
	if x0 < w {
		inner = ansi.Cut(line, x0, x1+1)
	}
	plain := ansi.Strip(inner)
	if pw := ansi.StringWidth(plain); pw < innerW {
		plain += strings.Repeat(" ", innerW-pw)
	}
	highlighted := selectionStyle.Render(plain)

	right := ""
	if x1+1 < w {
		right = ansi.Cut(line, x1+1, w)
	}

	return left + highlighted + right
}

// extractRectFromView returns the plain-text content of the cell rectangle
// (x0,y0)..(x1,y1) (inclusive) from a rendered view. Lines shorter than the
// selection are right-trimmed (no padding) to keep clipboard contents tidy.
func extractRectFromView(view string, x0, y0, x1, y1 int) string {
	if y1 < y0 || x1 < x0 {
		return ""
	}
	lines := strings.Split(view, "\n")
	var out []string
	for y := y0; y <= y1 && y < len(lines); y++ {
		line := lines[y]
		w := ansi.StringWidth(line)
		if x0 >= w {
			out = append(out, "")
			continue
		}
		end := x1 + 1
		if end > w {
			end = w
		}
		seg := ansi.Strip(ansi.Cut(line, x0, end))
		out = append(out, strings.TrimRight(seg, " "))
	}
	// Trim trailing fully-empty lines so a rectangle that overshoots the
	// last content row doesn't append blank lines to the clipboard.
	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	return strings.Join(out, "\n")
}

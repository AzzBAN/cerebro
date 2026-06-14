package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"charm.land/glamour/v2"
	"github.com/azhar/cerebro/internal/domain"
	"github.com/charmbracelet/lipgloss"
	"github.com/shopspring/decimal"
)

// padToLines pads s with blank lines so it has exactly n visible lines.
func padToLines(s string, n int) string {
	trimmed := strings.TrimRight(s, "\n")
	if trimmed == "" {
		return s
	}
	current := strings.Count(trimmed, "\n") + 1
	for current < n {
		trimmed += "\n "
		current++
	}
	return trimmed
}

// truncateLines clips s to at most maxLines newline-separated lines.
func truncateLines(s string, maxLines int) string {
	if maxLines <= 0 {
		return ""
	}
	count := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			count++
			if count >= maxLines {
				return s[:i]
			}
		}
	}
	return s
}

// ─── View ────────────────────────────────────────────────────────────────────

// View renders the full TUI layout. Every section is explicitly sized to
// guarantee the total output never exceeds m.height lines.
func (m Model) View() string {
	if m.width == 0 {
		return "Loading..."
	}

	if m.breakpoint() == bpXS {
		return m.viewXS()
	}

	header := m.renderHeader()
	tabBar := m.renderTabBar()
	statusBar := m.renderStatusBar()
	inputBar := m.renderInput()

	var body string
	switch m.activeTab {
	case tabMarket:
		body = m.renderTabMarket()
	case tabLogs:
		body = m.renderTabLogs()
	case tabAgents:
		body = m.renderTabAgents()
	default: // tabDashboard
		body = m.renderTabDashboard()
	}

	result := lipgloss.JoinVertical(lipgloss.Left,
		header, tabBar, body, statusBar, inputBar)

	// Pad to exactly m.height lines so the TUI fills the terminal.
	result = padToLines(result, m.height)

	// Drag-to-copy: overlay the active selection rectangle. Skip zero-size
	// selections (single click) to avoid a spurious 1-cell highlight.
	if m.selecting && m.selStart != m.selEnd {
		x0, y0, x1, y1 := normalizeSelection(m.selStart, m.selEnd)
		result = applySelectionOverlay(result, x0, y0, x1, y1)
	}
	return result
}

// viewXS renders the compact single-panel layout used on tiny terminals.
// It drops the Watch and Agent Activity panels because they'd consume more
// rows than the terminal has available.
func (m *Model) viewXS() string {
	header := m.renderHeader()
	statusBar := m.renderStatusBar()
	inputBar := m.renderInput()

	middleH := m.height - 3 // header(1) + status(1) + input(1)
	if middleH < 3 {
		middleH = 3
	}

	middle := m.renderMiddleXSAll(middleH)
	result := lipgloss.JoinVertical(lipgloss.Left, header, middle, statusBar, inputBar)
	result = padToLines(result, m.height)
	if m.selecting && m.selStart != m.selEnd {
		x0, y0, x1, y1 := normalizeSelection(m.selStart, m.selEnd)
		result = applySelectionOverlay(result, x0, y0, x1, y1)
	}
	return result
}

// renderMiddleXSAll fills the entire middle area (no border) with the active
// XS tab's content plus a tab strip. Used by viewXS.
func (m *Model) renderMiddleXSAll(middleH int) string {
	tabs := renderXSTabs(m.xsTab, m.width, m.width >= 80)

	contentH := middleH - 2 // border(2)
	contentH--              // tab strip (1)
	if contentH < 1 {
		contentH = 1
	}

	body := m.renderXSBody(contentH)
	body = truncateLines(body, contentH)

	// `borderH` (=2) is the lipgloss border-only overhead. With Padding(0,1)
	// this yields outer width = m.width and content area = m.width - frameH.
	return borderStyle.Width(m.width - borderH).MaxHeight(middleH).Render(tabs + "\n" + body)
}

// renderXSBody returns the content for the currently-active XS tab.
func (m *Model) renderXSBody(contentH int) string {
	switch m.xsTab {
	case 0: // Market
		return m.renderXSMarket(contentH)
	case 1: // Positions
		return m.renderPositions(contentH)
	case 2: // Log
		return m.renderLogPanel(m.width, contentH)
	case 3: // Bias
		return m.renderBiasPanel(m.width, contentH)
	case 4: // Macro
		return m.renderMacroPanel(m.width, contentH)
	case 5: // Agents
		return m.renderAgentRunsPanel(m.width, contentH)
	default:
		return dimStyle.Render("  (unknown tab)")
	}
}

// renderXSMarket is a compact market list for the XS Market tab.
func (m *Model) renderXSMarket(contentH int) string {
	header := panelHeaderMarket.Render("Market")
	maxLines := contentH - 1
	if maxLines < 1 {
		maxLines = 1
	}
	if len(m.quotes) == 0 {
		return header + "\n" + dimStyle.Render("  Waiting for market data…")
	}
	syms := make([]string, 0, len(m.quotes))
	for s := range m.quotes {
		syms = append(syms, s)
	}
	sort.Strings(syms)
	if len(syms) > maxLines {
		syms = syms[:maxLines]
	}
	rows := make([]string, 0, len(syms))
	for _, s := range syms {
		q := m.quotes[s]
		rows = append(rows, fmt.Sprintf(" %-12s %-10s %s",
			symStyle.Render(s),
			formatPrice(q.last),
			formatChange(q.priceChange, q.priceChangePercent),
		))
	}
	return header + "\n" + strings.Join(rows, "\n")
}

// ─── Panel renderers ─────────────────────────────────────────────────────────

func (m Model) renderHeader() string {
	logo := lipgloss.NewStyle().Bold(true).Foreground(colorAccent).Render(" ◈ Cerebro ")
	env := lipgloss.NewStyle().Foreground(colorFgDim).Render("paper")
	left := logo + dimStyle.Render("│") + " " + env
	clockStr := m.now.Format("2006-01-02 15:04:05 MST")
	clock := clockStyle.Render(clockStr + " ")
	leftW := lipgloss.Width(left)
	clockW := lipgloss.Width(clock)
	spacer := strings.Repeat(" ", max(0, m.width-leftW-clockW))
	return lipgloss.NewStyle().
		Background(lipgloss.Color("234")).
		Width(m.width).
		Render(left + spacer + clock)
}

// renderTabBar renders the main horizontal tab strip.
func (m Model) renderTabBar() string {
	parts := make([]string, 0, len(mainTabLabels))
	for i, label := range mainTabLabels {
		icon := mainTabIcons[i]
		text := icon + " " + label
		if mainTab(i) == m.activeTab {
			parts = append(parts, tabActiveStyle.Render(text))
		} else {
			parts = append(parts, tabInactiveStyle.Render(text))
		}
	}
	row := strings.Join(parts, "")
	hint := dimStyle.Render("  Tab/1-4")
	row += hint
	// Pad to full width
	rowW := lipgloss.Width(row)
	if rowW < m.width {
		row += strings.Repeat(" ", m.width-rowW)
	}
	return lipgloss.NewStyle().
		Background(lipgloss.Color("234")).
		Width(m.width).
		Render(row)
}

func (m Model) renderWatchPanel() string {
	symCount := len(m.quotes)

	// Build header with scroll indicators
	title := "Market Watch"
	if symCount > maxWatchLines {
		title = fmt.Sprintf("Market Watch (%d/%d)", m.watchScrollY+1, symCount)
	}
	if m.watchScrollX > 0 {
		title += " ←"
	}
	totalW := watchTotalContentWidth()
	availW := m.watchContentWidth()
	if m.watchScrollX+availW < totalW {
		title += " →"
	}
	header := panelHeaderMarket.Render(title)

	var content string
	if symCount == 0 {
		content = dimStyle.Render("  Waiting for market data...")
	} else {
		syms := make([]string, 0, symCount)
		for s := range m.quotes {
			syms = append(syms, s)
		}
		sort.Strings(syms)

		// Vertical slice
		offset := m.watchScrollY
		if offset >= len(syms) {
			offset = 0
		}
		end := offset + maxWatchLines
		if end > len(syms) {
			end = len(syms)
		}
		page := syms[offset:end]

		pad := lipgloss.NewStyle().Width
		// Header row
		hdrSym := pad(colSymbol).Render(dimStyle.Render("Symbol"))
		hdrLast := pad(colLast).Render(dimStyle.Render("Last"))
		hdrChg := pad(colChg).Render(dimStyle.Render("Chg / Chg%"))
		hdrBA := pad(colBidAsk).Render(dimStyle.Render("Bid / Ask"))
		hdrSpread := pad(colSpread).Render(dimStyle.Render("Spread"))
		hdrVol := pad(colVol).Render(dimStyle.Render("Vol(24h)"))
		hdrBias := pad(colBias).Render(dimStyle.Render("Bias"))
		headerRow := hdrSym + hdrLast + hdrChg + hdrBA + hdrSpread + hdrVol + hdrBias

		rows := []string{headerRow}
		for _, sym := range page {
			q := m.quotes[sym]
			bias := m.biasResults[domain.Symbol(sym)]
			rows = append(rows, formatWatchRow(q, bias,
				colSymbol, colLast, colChg, colBidAsk, colSpread, colVol, colBias))
		}
		fullContent := strings.Join(rows, "\n")

		// Horizontal crop
		if m.watchScrollX > 0 || availW < totalW {
			lines := strings.Split(fullContent, "\n")
			cropped := make([]string, len(lines))
			for i, line := range lines {
				runes := []rune(line)
				sx := m.watchScrollX
				if sx > len(runes) {
					sx = len(runes)
				}
				ex := sx + availW
				if ex > len(runes) {
					ex = len(runes)
				}
				cropped[i] = string(runes[sx:ex])
			}
			content = strings.Join(cropped, "\n")
		} else {
			content = fullContent
		}
	}

	outerH := m.computedWatchH()
	style := borderStyle
	if m.focusedPanel == focusWatch {
		style = focusedBorderStyle
	}
	// `availW` is the content area; the lipgloss `Width()` argument for the
	// bordered panel is the block width (content + padding) = `m.width - borderH`.
	return style.Width(m.width - borderH).MaxHeight(outerH).Render(header + "\n" + content)
}

func formatWatchRow(q quoteState, bias domain.BiasResult, wSym, wLast, wChg, wBA, wSpread, wVol, wBias int) string {
	// Symbol
	symStr := lipgloss.NewStyle().Width(wSym).Render(symStyle.Render(q.symbol))

	// Last price
	lastStr := formatPrice(q.last)
	lastStr = lipgloss.NewStyle().Width(wLast).Render(lastStr)

	// Change / Change%
	chgStr := formatChange(q.priceChange, q.priceChangePercent)
	chgStr = lipgloss.NewStyle().Width(wChg).Render(chgStr)

	// Bid / Ask
	bidAskStr := formatBidAsk(q.bid, q.ask)
	bidAskStr = lipgloss.NewStyle().Width(wBA).Render(bidAskStr)

	// Spread
	spread := q.ask.Sub(q.bid)
	spreadStr := lipgloss.NewStyle().Width(wSpread).Render(formatPrice(spread))

	// Volume
	volStr := lipgloss.NewStyle().Width(wVol).Render(formatVolume(q.volume24h))

	// Bias (screening agent's directional read; "—" when no bias cached yet)
	biasStr := lipgloss.NewStyle().Width(wBias).Render(formatBias(bias))

	return symStr + lastStr + chgStr + bidAskStr + spreadStr + volStr + biasStr
}

// formatBias renders a BiasResult as a coloured short label that fits in
// colBias. An empty result (no cached bias yet) renders as a dim "—".
func formatBias(b domain.BiasResult) string {
	if b.CachedAt.IsZero() {
		return dimStyle.Render("—")
	}
	switch b.Score {
	case domain.BiasBullish:
		return priceStyle.Render("Bullish")
	case domain.BiasBearish:
		return errStyle.Render("Bearish")
	default:
		return warnStyle.Render("Neutral")
	}
}

// formatPrice renders a decimal price with magnitude-aware precision:
// >=1000 → 2dp, >=1 → 4dp, otherwise 6dp. A zero value renders as "-".
func formatPrice(v decimal.Decimal) string {
	if v.IsZero() {
		return "-"
	}
	abs := v.Abs()
	switch {
	case abs.GreaterThanOrEqual(decimal.NewFromInt(1000)):
		return v.StringFixed(2)
	case abs.GreaterThanOrEqual(decimal.NewFromInt(1)):
		return v.StringFixed(4)
	default:
		return v.StringFixed(6)
	}
}

func formatChange(chg, chgPct decimal.Decimal) string {
	if chg.IsZero() && chgPct.IsZero() {
		return dimStyle.Render("-")
	}
	sign := "+"
	if chg.IsNegative() {
		sign = ""
	}
	text := fmt.Sprintf("%s%s (%s%s%%)", sign, formatPrice(chg.Abs()), sign, chgPct.StringFixed(2))
	if !chg.IsNegative() {
		return priceStyle.Render(text)
	}
	return errStyle.Render(text)
}

func formatBidAsk(bid, ask decimal.Decimal) string {
	if bid.IsZero() && ask.IsZero() {
		return dimStyle.Render("-")
	}
	return fmt.Sprintf("%s / %s", formatPrice(bid), formatPrice(ask))
}

func formatVolume(v decimal.Decimal) string {
	if v.IsZero() {
		return "-"
	}
	f := v.InexactFloat64()
	switch {
	case f >= 1e9:
		return fmt.Sprintf("%.1fB", f/1e9)
	case f >= 1e6:
		return fmt.Sprintf("%.0fM", f/1e6)
	case f >= 1e3:
		return fmt.Sprintf("%.0fK", f/1e3)
	default:
		return fmt.Sprintf("%.0f", f)
	}
}

// middleHeight returns the exact outer height for the middle section.
func (m *Model) middleHeight() int {
	if m.height == 0 || m.width == 0 {
		return 10
	}
	headerH := 1
	watchH := m.computedWatchH()
	agentH := m.computedAgentPanelH()
	statusH := 1
	inputH := 3 // bordered input box: top border + content + bottom border
	tabBarH := 1 // main tab bar

	askH := 0
	if m.askResponse != "" {
		askH = askResponseH
		// Cap ask panel to at most 40% of terminal height so the middle section stays usable.
		maxAskH := m.height * 2 / 5
		if askH > maxAskH && maxAskH > 5 {
			askH = maxAskH
		}
	}

	return m.height - headerH - tabBarH - watchH - agentH - statusH - inputH - askH
}

// bodyHeight returns available height for tab body content (everything except
// header, tab bar, status bar, and input).
func (m *Model) bodyHeight() int {
	if m.height == 0 || m.width == 0 {
		return 10
	}
	headerH := 1
	tabBarH := 1
	statusH := 1
	inputH := 3 // bordered input box: top border + content + bottom border

	askH := 0
	if m.askResponse != "" {
		askH = askResponseH
		maxAskH := m.height * 2 / 5
		if askH > maxAskH && maxAskH > 5 {
			askH = maxAskH
		}
	}

	h := m.height - headerH - tabBarH - statusH - inputH - askH
	if h < 3 {
		h = 3
	}
	return h
}

// ─── Tab views ────────────────────────────────────────────────────────────────

// renderTabDashboard renders the classic dashboard layout (watch + panels + agent).
func (m *Model) renderTabDashboard() string {
	bodyH := m.bodyHeight()
	watchH := m.computedWatchH()
	agentH := m.computedAgentPanelH()
	middleH := bodyH - watchH - agentH

	watch := m.renderWatchPanel()

	// When the terminal is too small for all three sections, drop the middle
	// or the agent panel to avoid overflow.
	if middleH <= 2 {
		// Not enough room for bordered middle panels — skip them.
		if bodyH-watchH >= minAgentPanelH {
			agentPanel := m.renderAgentPanel()
			return lipgloss.JoinVertical(lipgloss.Left, watch, agentPanel)
		}
		return watch
	}

	middle := m.renderMiddle()
	agentPanel := m.renderAgentPanel()
	return lipgloss.JoinVertical(lipgloss.Left, watch, middle, agentPanel)
}

// renderTabMarket renders a full-height market watch view.
func (m *Model) renderTabMarket() string {
	bodyH := m.bodyHeight()
	contentH := bodyH - 2 // border
	if contentH < 1 {
		contentH = 1
	}
	contentW := m.width - borderH

	header := panelHeaderMarket.Render("Market Watch")

	if len(m.quotes) == 0 {
		content := header + "\n" + dimStyle.Render("  Waiting for market data...")
		return borderStyle.Width(contentW).MaxHeight(bodyH).Render(content)
	}

	syms := make([]string, 0, len(m.quotes))
	for s := range m.quotes {
		syms = append(syms, s)
	}
	sort.Strings(syms)

	maxRows := contentH - 2 // header + table header
	if maxRows < 1 {
		maxRows = 1
	}
	if maxRows > len(syms) {
		maxRows = len(syms)
	}

	pad := lipgloss.NewStyle().Width
	hdrRow := pad(colSymbol).Render(dimStyle.Render("Symbol")) +
		pad(colLast).Render(dimStyle.Render("Last")) +
		pad(colChg).Render(dimStyle.Render("Chg / Chg%")) +
		pad(colBidAsk).Render(dimStyle.Render("Bid / Ask")) +
		pad(colSpread).Render(dimStyle.Render("Spread")) +
		pad(colVol).Render(dimStyle.Render("Vol(24h)")) +
		pad(colBias).Render(dimStyle.Render("Bias"))

	rows := []string{hdrRow}
	for _, sym := range syms[:maxRows] {
		q := m.quotes[sym]
		bias := m.biasResults[domain.Symbol(sym)]
		rows = append(rows, formatWatchRow(q, bias,
			colSymbol, colLast, colChg, colBidAsk, colSpread, colVol, colBias))
	}

	content := header + "\n" + strings.Join(rows, "\n")

	// Add positions summary below if space allows
	remaining := contentH - 2 - maxRows - 1
	if remaining > 3 && len(m.positionRows) > 0 {
		content += "\n\n" + panelHeaderPositions.Render("Open Positions")
		posLines := m.renderPositions(remaining - 1)
		// Strip the header since we already added one
		if idx := strings.Index(posLines, "\n"); idx >= 0 {
			posLines = posLines[idx+1:]
		}
		content += "\n" + posLines
	}

	style := borderStyle
	if m.focusedPanel == focusWatch {
		style = focusedBorderStyle
	}
	return style.Width(contentW).MaxHeight(bodyH).Render(truncateLines(content, contentH))
}

// renderTabLogs renders a full-height log viewer.
func (m *Model) renderTabLogs() string {
	bodyH := m.bodyHeight()
	contentH := bodyH - 2 // border
	if contentH < 1 {
		contentH = 1
	}
	contentW := m.width - borderH

	logContent := m.renderLogPanel(m.width, contentH)

	style := focusedBorderStyle
	return style.Width(contentW).MaxHeight(bodyH).Render(truncateLines(logContent, contentH))
}

// renderTabAgents renders a full-height agent activity view.
func (m *Model) renderTabAgents() string {
	bodyH := m.bodyHeight()
	contentH := bodyH - 2 // border
	if contentH < 1 {
		contentH = 1
	}
	contentW := m.width - borderH

	header := panelHeaderAgent.Render("Agent Activity")

	var lines []string
	// Show active agents first
	for _, id := range m.agentRunOrder {
		run := m.agentRuns[id]
		if run.step == StepComplete || run.step == StepError {
			continue
		}
		desc := run.description
		if desc == "" {
			desc = string(run.step)
		}
		frame := spinnerFrames[m.spinnerFrame]
		lines = append(lines,
			fmt.Sprintf("  %s %s", agentStyle.Render(frame), agentStyle.Render(desc)),
			dimStyle.Render(fmt.Sprintf("    %s · %s/%s · %s",
				run.agent, run.provider, run.model,
				time.Since(run.started).Truncate(10*time.Millisecond))),
		)
	}

	if len(lines) > 0 {
		lines = append(lines, "")
	}

	// Show completed/errored
	lines = append(lines, dimStyle.Render("  ─── History ───"))
	for i := len(m.agentRunOrder) - 1; i >= 0; i-- {
		id := m.agentRunOrder[i]
		run := m.agentRuns[id]
		if run.step != StepComplete && run.step != StepError {
			continue
		}
		elapsed := run.finished.Sub(run.started).Truncate(10 * time.Millisecond)
		if run.step == StepError {
			lines = append(lines,
				fmt.Sprintf("  %s %s %s %s",
					errStyle.Render("✗"), run.agent, dimStyle.Render(string(run.symbol)), dimStyle.Render(elapsed.String())),
				dimStyle.Render("    "+truncateStr(run.err, contentW-6)),
			)
		} else {
			lines = append(lines,
				fmt.Sprintf("  %s %s %s %s",
					priceStyle.Render("✓"), run.agent, dimStyle.Render(string(run.symbol)), dimStyle.Render(elapsed.String())),
			)
		}
	}

	if len(m.agentRunOrder) == 0 {
		lines = append(lines, dimStyle.Render("  No agent runs yet"))
	}

	content := header + "\n" + strings.Join(lines, "\n")
	return borderStyle.Width(contentW).MaxHeight(bodyH).Render(truncateLines(content, contentH))
}

// renderMiddle dispatches to the appropriate breakpoint-specific layout for the
// non-XS tiers. XS bypasses this via viewXS() in View().
func (m *Model) renderMiddle() string {
	switch m.breakpoint() {
	case bpMD:
		return m.renderMiddleMD()
	case bpLG:
		return m.renderMiddleLG()
	case bpXL:
		return m.renderMiddleXL()
	default:
		return m.renderMiddleSM()
	}
}

// renderMiddleSM is the historical two-column layout: Positions | Log.
func (m *Model) renderMiddleSM() string {
	middleH := m.middleHeight()

	totalContentW := m.width - 2*borderH
	if totalContentW < 10 {
		totalContentW = 10
	}

	posContentW := totalContentW / 3
	logContentW := totalContentW - posContentW

	contentH := middleH - 2
	if contentH < 1 {
		contentH = 1
	}

	posStyle := borderStyle
	if m.focusedPanel == focusPositions {
		posStyle = focusedBorderStyle
	}
	posContent := truncateLines(m.renderPositions(contentH), contentH)
	logContent := truncateLines(m.renderLogPanel(logContentW+borderH, contentH), contentH)

	positions := posStyle.Width(posContentW).MaxHeight(contentH + 2).Render(posContent)
	logSt := borderStyle
	if m.focusedPanel == focusLog {
		logSt = focusedBorderStyle
	}
	logPanel := logSt.Width(logContentW).MaxHeight(contentH + 2).Render(logContent)

	joined := lipgloss.JoinHorizontal(lipgloss.Top, positions, logPanel)
	return lipgloss.PlaceHorizontal(m.width, lipgloss.Left, joined)
}

// renderMiddleMD is the three-column dashboard:
// [Positions / Macro] [Bias / Agent Runs] [Log]
func (m *Model) renderMiddleMD() string {
	middleH := m.middleHeight()
	// Single-panel columns: one bordered box of total height = middleH, so the
	// inner content area is middleH - 2.
	singleContentH := middleH - 2
	if singleContentH < 1 {
		singleContentH = 1
	}
	// Stacked columns: two bordered boxes whose outer heights sum to middleH,
	// so their content heights must sum to middleH - 4 (two border pairs).
	stackContentH := middleH - 4
	if stackContentH < 2 {
		stackContentH = 2
	}

	total := m.width - 3*borderH
	if total < 30 {
		total = 30
	}
	colLeft := total * 22 / 100
	colCenter := total * 28 / 100
	colRight := total - colLeft - colCenter

	// Vertical split inside Left and Center columns.
	topH := stackContentH / 2
	if topH < 3 {
		topH = 3
	}
	botH := stackContentH - topH
	if botH < 1 {
		botH = 1
	}

	left := stackPanels(colLeft, topH, botH,
		m.renderPositions(topH),
		m.renderMacroPanel(colLeft+borderH, botH),
	)
	center := stackPanels(colCenter, topH, botH,
		m.renderBiasPanel(colCenter+borderH, topH),
		m.renderAgentRunsPanel(colCenter+borderH, botH),
	)

	logSt := borderStyle
	if m.focusedPanel == focusLog {
		logSt = focusedBorderStyle
	}
	logContent := truncateLines(m.renderLogPanel(colRight+borderH, singleContentH), singleContentH)
	right := logSt.Width(colRight).MaxHeight(singleContentH + 2).Render(logContent)

	joined := lipgloss.JoinHorizontal(lipgloss.Top, left, center, right)
	return lipgloss.PlaceHorizontal(m.width, lipgloss.Left, joined)
}

// renderMiddleLG is the four-column layout:
// [Positions / Account] [Bias / Agent Runs] [Log] [Macro / Calendar]
func (m *Model) renderMiddleLG() string {
	middleH := m.middleHeight()
	singleContentH := middleH - 2
	if singleContentH < 1 {
		singleContentH = 1
	}
	stackContentH := middleH - 4
	if stackContentH < 2 {
		stackContentH = 2
	}

	total := m.width - 4*borderH
	if total < 40 {
		total = 40
	}
	col1 := total * 20 / 100
	col2 := total * 22 / 100
	col4 := total * 22 / 100
	col3 := total - col1 - col2 - col4

	topH := stackContentH / 2
	if topH < 3 {
		topH = 3
	}
	botH := stackContentH - topH
	if botH < 1 {
		botH = 1
	}

	c1 := stackPanels(col1, topH, botH,
		m.renderPositions(topH),
		m.renderAccountPanel(col1+borderH, botH),
	)
	c2 := stackPanels(col2, topH, botH,
		m.renderBiasPanel(col2+borderH, topH),
		m.renderAgentRunsPanel(col2+borderH, botH),
	)

	logSt := borderStyle
	if m.focusedPanel == focusLog {
		logSt = focusedBorderStyle
	}
	logContent := truncateLines(m.renderLogPanel(col3+borderH, singleContentH), singleContentH)
	c3 := logSt.Width(col3).MaxHeight(singleContentH + 2).Render(logContent)

	c4 := stackPanels(col4, topH, botH,
		m.renderMacroPanel(col4+borderH, topH),
		m.renderCalendarPanel(col4+borderH, botH),
	)

	joined := lipgloss.JoinHorizontal(lipgloss.Top, c1, c2, c3, c4)
	return lipgloss.PlaceHorizontal(m.width, lipgloss.Left, joined)
}

// renderMiddleXL is the five-column command center:
// [Positions / Account] [Bias / Agent Runs] [Log] [Macro / Calendar] [Health]
func (m *Model) renderMiddleXL() string {
	middleH := m.middleHeight()
	singleContentH := middleH - 2
	if singleContentH < 1 {
		singleContentH = 1
	}
	stackContentH := middleH - 4
	if stackContentH < 2 {
		stackContentH = 2
	}

	total := m.width - 5*borderH
	if total < 50 {
		total = 50
	}
	col1 := total * 16 / 100
	col2 := total * 20 / 100
	col4 := total * 20 / 100
	col5 := total * 16 / 100
	col3 := total - col1 - col2 - col4 - col5

	topH := stackContentH / 2
	if topH < 3 {
		topH = 3
	}
	botH := stackContentH - topH
	if botH < 1 {
		botH = 1
	}

	c1 := stackPanels(col1, topH, botH,
		m.renderPositions(topH),
		m.renderAccountPanel(col1+borderH, botH),
	)
	c2 := stackPanels(col2, topH, botH,
		m.renderBiasPanel(col2+borderH, topH),
		m.renderAgentRunsPanel(col2+borderH, botH),
	)

	logSt := borderStyle
	if m.focusedPanel == focusLog {
		logSt = focusedBorderStyle
	}
	logContent := truncateLines(m.renderLogPanel(col3+borderH, singleContentH), singleContentH)
	c3 := logSt.Width(col3).MaxHeight(singleContentH + 2).Render(logContent)

	c4 := stackPanels(col4, topH, botH,
		m.renderMacroPanel(col4+borderH, topH),
		m.renderCalendarPanel(col4+borderH, botH),
	)
	c5 := borderStyle.Width(col5).MaxHeight(singleContentH + 2).Render(
		truncateLines(m.renderHealthPanel(col5+borderH, singleContentH), singleContentH),
	)

	joined := lipgloss.JoinHorizontal(lipgloss.Top, c1, c2, c3, c4, c5)
	return lipgloss.PlaceHorizontal(m.width, lipgloss.Left, joined)
}

// renderXSTabs renders a horizontal tab strip with the active tab highlighted.
// useFullLabels picks between the full xsTabLabels and the abbreviated
// xsTabShort variants based on available width.
func renderXSTabs(active, width int, useFullLabels bool) string {
	labels := xsTabLabels
	if !useFullLabels {
		labels = xsTabShort
	}
	parts := make([]string, 0, len(labels))
	for i, label := range labels {
		if i == active {
			parts = append(parts, lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("0")).
				Background(lipgloss.Color("14")).
				Render(" "+label+" "))
		} else {
			parts = append(parts, dimStyle.Render(" "+label+" "))
		}
	}
	row := strings.Join(parts, "·")
	if useFullLabels {
		row += dimStyle.Render("  [Tab]")
	}
	return truncateStr(row, width)
}

// stackPanels joins two panels vertically inside a column of fixed width,
// each rendered within its own border. Heights are passed in as content heights.
func stackPanels(width, topH, botH int, top, bot string) string {
	topRendered := borderStyle.Width(width).MaxHeight(topH + 2).Render(truncateLines(top, topH))
	botRendered := borderStyle.Width(width).MaxHeight(botH + 2).Render(truncateLines(bot, botH))
	return lipgloss.JoinVertical(lipgloss.Left, topRendered, botRendered)
}

func (m *Model) renderPositions(contentH int) string {
	header := panelHeaderPositions.Render("Active Positions")
	if len(m.positionRows) == 0 {
		return header + "\n" + dimStyle.Render("  No open positions")
	}

	rows := append([]domain.Position(nil), m.positionRows...)
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Venue == rows[j].Venue {
			return rows[i].Symbol < rows[j].Symbol
		}
		return rows[i].Venue < rows[j].Venue
	})

	maxLines := contentH - 1
	if maxLines < 1 {
		maxLines = 1
	}

	const labelW = 7
	lbl := lipgloss.NewStyle().Width(labelW)

	lines := []string{header}
	lineCount := 0
	for _, p := range rows {
		if lineCount >= maxLines {
			break
		}

		pnlUSD := p.UnrealizedPnL()
		pnlROI := p.UnrealizedPnLROI()
		quote := p.Symbol.QuoteAsset()
		if quote == "" {
			quote = "USDT"
		}
		usdStr := pnlUSD.StringFixed(2)
		roiStr := pnlROI.StringFixed(2) + "%"
		if pnlUSD.IsPositive() {
			usdStr = "+" + usdStr
		}
		if pnlROI.IsPositive() {
			roiStr = "+" + roiStr
		}
		combined := usdStr + " " + quote + " (" + roiStr + ")"
		var pnlStr string
		switch {
		case pnlUSD.IsPositive():
			pnlStr = priceStyle.Render(combined)
		case pnlUSD.IsNegative():
			pnlStr = errStyle.Render(combined)
		default:
			pnlStr = combined
		}

		sideStr := strings.ToUpper(string(p.Side))
		if p.Side == domain.SideBuy {
			sideStr = priceStyle.Bold(true).Render(sideStr)
		} else {
			sideStr = errStyle.Bold(true).Render(sideStr)
		}

		headerLine := "  " + symStyle.Render(string(p.Symbol)) + "    " + sideStr
		if p.Leverage > 1 {
			headerLine += " " + dimStyle.Render(fmt.Sprintf("%dx", p.Leverage))
		}

		posLines := []string{
			"  " + dimStyle.Render(string(p.Venue)),
			headerLine,
			"  " + lbl.Render(dimStyle.Render("QTY")) + "  " + p.Quantity.String(),
		}
		// Show MARGIN only for leveraged positions; for spot the notional
		// is just price × quantity which is already implied.
		if p.Leverage > 1 {
			marginLabel := "MARGIN"
			if p.Margin.IsZero() {
				// Adapter didn't report exchange-allocated margin; we're
				// rendering the derived minimum. Tag it so it's not
				// mistaken for the actual wallet allocation in isolated
				// mode.
				marginLabel = "MARGIN*"
			}
			posLines = append(posLines,
				"  "+lbl.Render(dimStyle.Render(marginLabel))+"  "+formatPositionPrice(p.EffectiveMargin().StringFixed(2))+" "+quote,
			)
		}
		posLines = append(posLines,
			"  "+lbl.Render(dimStyle.Render("ENTRY"))+"  "+formatPositionPrice(adaptivePricePrecision(p.EntryPrice)),
			"  "+lbl.Render(dimStyle.Render("CURRENT"))+"  "+formatPositionPrice(adaptivePricePrecision(p.CurrentPrice)),
			"  "+lbl.Render(dimStyle.Render("PNL"))+"  "+pnlStr,
		)
		if !p.StopLoss.IsZero() || !p.TakeProfit1.IsZero() {
			posLines = append(posLines,
				"  "+lbl.Render(dimStyle.Render("SL"))+"  "+formatPositionPrice(adaptivePricePrecision(p.StopLoss)),
				"  "+lbl.Render(dimStyle.Render("TP1"))+"  "+formatPositionPrice(adaptivePricePrecision(p.TakeProfit1)),
			)
		}

		remaining := maxLines - lineCount
		if len(posLines) > remaining {
			posLines = posLines[:remaining]
		}
		lines = append(lines, posLines...)
		lineCount += len(posLines)
	}
	return strings.Join(lines, "\n")
}

// formatPositionPrice adds dot thousands separator to a decimal string.
func formatPositionPrice(s string) string {
	parts := strings.Split(s, ".")
	intStr := parts[0]

	var buf strings.Builder
	n := len(intStr)
	for i := 0; i < n; i++ {
		if i > 0 && (n-i)%3 == 0 {
			buf.WriteByte('.')
		}
		buf.WriteByte(intStr[i])
	}

	if len(parts) > 1 {
		buf.WriteByte('.')
		buf.WriteString(parts[1])
	}

	return buf.String()
}

// adaptivePricePrecision renders a price with enough decimal places to stay
// descriptive across magnitudes. Large prices (>= 1) keep 2 decimals; sub-dollar
// assets like DOGE need more so 0.0882 doesn't collapse to 0.09. Trailing zeros
// are trimmed but at least 2 decimal places are always kept.
func adaptivePricePrecision(p decimal.Decimal) string {
	abs := p.Abs()
	var places int32
	switch {
	case abs.GreaterThanOrEqual(decimal.NewFromInt(1)):
		places = 2
	case abs.GreaterThanOrEqual(decimal.NewFromFloat(0.01)):
		places = 6
	default:
		places = 8
	}

	s := p.StringFixed(places)
	if !strings.Contains(s, ".") {
		return s
	}
	s = strings.TrimRight(s, "0")
	dot := strings.IndexByte(s, '.')
	if decs := len(s) - dot - 1; decs < 2 {
		s += strings.Repeat("0", 2-decs)
	}
	return s
}

// renderLogPanel renders the Activity & Log content. panelW is the OUTER panel
// width (including the border overhead). Pass 0 to fall back to the SM legacy
// width derivation.
func (m *Model) renderLogPanel(panelW, contentH int) string {
	header := panelHeaderLog.Render("Activity & Log")

	logLines := contentH - 1
	if logLines < 1 {
		logLines = 1
	}

	if len(m.logs) == 0 {
		return header + "\n" + dimStyle.Render("  Waiting for activity…")
	}

	total := len(m.logs)

	// `panelW` is the OUTER VISIBLE width of the bordered panel that contains
	// these log lines. The actual content area available for text inside the
	// border + padding is `panelW - frameH`, which is what we must wrap log
	// lines to so that lipgloss does not soft-wrap and grow the panel past its
	// allocated MaxHeight (which would clip the bottom border).
	var logContentW int
	if panelW > 0 {
		logContentW = panelW - frameH
	} else {
		gap := 1
		// Sum of two columns' content widths must fit inside the terminal
		// minus 2 column-frames and a separator gap.
		availContentW := m.width - 2*frameH - gap
		posContentW := availContentW / 3
		logContentW = availContentW - posContentW
	}
	if logContentW < 10 {
		logContentW = 10
	}
	truncateStyle := lipgloss.NewStyle().Width(logContentW)

	// `m.logScrollY` is in entry units (0 = pinned to bottom). Apply the
	// scroll first, then walk backward from the most-recent visible entry
	// rendering each one. Individual entries may wrap to multiple physical
	// lines once `truncateStyle.Render` runs, so we accumulate by physical
	// line count rather than entry count to guarantee the latest content
	// stays visible. Without this, a wrapped tail entry would push older
	// entries down and the caller's `truncateLines(..., contentH)` would
	// crop the newest lines (which appear at the bottom) silently.
	endIdx := total - m.logScrollY
	if endIdx > total {
		endIdx = total
	}
	if endIdx < 0 {
		endIdx = 0
	}

	rendered := make([]string, 0, logLines)
	linesUsed := 0
	for i := endIdx - 1; i >= 0 && linesUsed < logLines; i-- {
		line := truncateStyle.Render(m.logs[i].render())
		physLines := strings.Count(line, "\n") + 1
		rendered = append([]string{line}, rendered...)
		linesUsed += physLines
	}

	joined := strings.Join(rendered, "\n")
	// We may have overshot by one entry's worth of wrapped lines. Drop
	// physical lines from the TOP so the newest content remains pinned to
	// the bottom of the visible window.
	if linesUsed > logLines {
		phys := strings.Split(joined, "\n")
		joined = strings.Join(phys[len(phys)-logLines:], "\n")
	}

	return header + "\n" + joined
}

func (m *Model) renderAgentPanel() string {
	header := panelHeaderAgent.Render("Agent Activity")
	outerH := m.computedAgentPanelH()
	innerH := outerH - 2
	if innerH < 1 {
		innerH = 1
	}
	contentH := innerH - 1
	if contentH < 1 {
		contentH = 1
	}

	var activeLines []string
	var lastCompleted *agentRunState

	for _, id := range m.agentRunOrder {
		run := m.agentRuns[id]
		if run.step == StepComplete || run.step == StepError {
			if lastCompleted == nil || run.finished.After(lastCompleted.finished) {
				lastCompleted = run
			}
			continue
		}

		// First line: spinner + description (what the agent is doing)
		desc := run.description
		if desc == "" {
			desc = string(run.step)
			if run.step == StepTool && run.toolName != "" {
				desc = run.toolName
			}
		}

		frame := spinnerFrames[m.spinnerFrame]
		descLine := fmt.Sprintf(" %s %s", agentStyle.Render(frame), agentStyle.Render(desc))

		// Second line: metadata (agent, provider/model, step progress, elapsed)
		elapsed := time.Since(run.started).Truncate(10 * time.Millisecond)
		var metaParts []string
		metaParts = append(metaParts, run.agent)
		if run.provider != "" || run.model != "" {
			metaParts = append(metaParts, fmt.Sprintf("%s/%s", run.provider, run.model))
		}
		if run.maxSteps > 0 {
			metaParts = append(metaParts, fmt.Sprintf("step %d/%d", run.stepNum, run.maxSteps))
		}
		if run.symbol != "" {
			metaParts = append(metaParts, string(run.symbol))
		}
		metaParts = append(metaParts, elapsed.String())
		metaLine := dimStyle.Render("   " + strings.Join(metaParts, " · "))

		activeLines = append(activeLines, descLine, metaLine)
	}

	var content strings.Builder
	for _, l := range activeLines {
		content.WriteString(l + "\n")
	}

	usedLines := len(activeLines)

	if lastCompleted != nil {
		elapsed := lastCompleted.finished.Sub(lastCompleted.started).Truncate(10 * time.Millisecond)
		if lastCompleted.err != "" {
			summary := fmt.Sprintf(" ✗ %s %s → error (%s)",
				lastCompleted.agent, lastCompleted.symbol, elapsed)
			content.WriteString(errStyle.Render(summary) + "\n")
			content.WriteString(dimStyle.Render("  "+truncateStr(lastCompleted.err, m.width-frameH-4)) + "\n")
		} else {
			summary := fmt.Sprintf(" ✓ %s %s (%s)",
				lastCompleted.agent, lastCompleted.symbol, elapsed)
			content.WriteString(priceStyle.Render(summary) + "\n")
			if lastCompleted.content != "" {
				remaining := contentH - usedLines - 1
				if remaining < 1 {
					remaining = 1
				}
				// Markdown is rendered inside the bordered+padded agent panel,
				// so word-wrap to the actual content area (`m.width - frameH`),
				// not the lipgloss block width.
				rendered := renderAgentMarkdown(lastCompleted.content, m.width-frameH, remaining)
				content.WriteString(rendered)
			}
		}
	}

	if content.Len() == 0 {
		content.WriteString(dimStyle.Render("  No agent activity"))
	}

	fullContent := header + "\n" + truncateLines(content.String(), contentH)
	return borderStyle.Width(m.width - borderH).MaxHeight(outerH).Render(fullContent)
}

// renderAgentMarkdown renders markdown content with glamour, constrained to width and maxLines.
func renderAgentMarkdown(md string, width, maxLines int) string {
	if width < 20 {
		width = 20
	}
	r, err := glamour.NewTermRenderer(
		glamour.WithStandardStyle("dark"),
		glamour.WithWordWrap(width),
	)
	if err != nil {
		return truncateLines(md, maxLines)
	}
	rendered, err := r.Render(md)
	if err != nil {
		return truncateLines(md, maxLines)
	}
	rendered = strings.TrimRight(rendered, "\n")
	return truncateLines(rendered, maxLines)
}

// truncateStr clips s to at most max runes.
func truncateStr(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	if max > 3 {
		return string(runes[:max-3]) + "..."
	}
	return string(runes[:max])
}

func (m Model) renderStatusBar() string {
	sep := dimStyle.Render(" │ ")

	mouseLabel := "mouse:on"
	if !m.mouseEnabled {
		mouseLabel = "mouse:off"
	}

	var parts []string
	if m.heartbeat == "" {
		parts = append(parts, dimStyle.Render(" ♥ waiting…"))
	} else {
		parts = append(parts,
			lipgloss.NewStyle().Foreground(colorFgDim).Background(colorBg).Render(" "+m.heartbeatAt.Format("15:04:05")),
			lipgloss.NewStyle().Foreground(colorGreen).Background(colorBg).Render("♥"),
			lipgloss.NewStyle().Foreground(colorFg).Background(colorBg).Render(m.heartbeat),
		)
	}

	// Show a transient "copied N chars" notice for ~2s after a successful copy.
	if m.copyNotice != "" && time.Since(m.copyNoticeAt) < 2*time.Second {
		parts = append(parts,
			lipgloss.NewStyle().Foreground(colorGreen).Background(colorBg).Render("✓ "+m.copyNotice),
		)
	}

	if chip := m.renderBudgetChip(); chip != "" {
		parts = append(parts, chip)
	}

	right := dimStyle.Render(mouseLabel + "  Ctrl+O")
	leftText := strings.Join(parts, " ")
	leftW := lipgloss.Width(leftText)
	rightW := lipgloss.Width(right) + lipgloss.Width(sep)
	gap := m.width - leftW - rightW
	if gap < 0 {
		gap = 0
	}

	text := leftText + strings.Repeat(" ", gap) + sep + right
	maxContent := m.width
	runes := []rune(text)
	if len(runes) > maxContent {
		runes = runes[:maxContent]
	}
	text = string(runes)
	return lipgloss.NewStyle().
		Background(colorBg).
		Foreground(colorFg).
		Width(m.width).
		Render(text)
}

// renderBudgetChip renders a compact "⟐ 12.3K/500K · $1.24/5.00" chip for
// the status bar. Returns empty string when no snapshot has arrived yet or
// both budgets are disabled (0). Colour shifts as usage climbs:
// green < 75%, yellow ≥ 75%, red ≥ 100%.
func (m Model) renderBudgetChip() string {
	if !m.budgetSet {
		return ""
	}
	b := m.budget
	if b.TokenBudget <= 0 && b.CostBudgetUSD <= 0 {
		return ""
	}

	var pct float64
	var chunks []string
	if b.TokenBudget > 0 {
		chunks = append(chunks, fmt.Sprintf("%s/%s",
			formatTokensCompact(b.TokensUsed), formatTokensCompact(int64(b.TokenBudget))))
		if p := float64(b.TokensUsed) / float64(b.TokenBudget); p > pct {
			pct = p
		}
	} else if b.TokensUsed > 0 {
		chunks = append(chunks, formatTokensCompact(b.TokensUsed))
	}
	if b.CostBudgetUSD > 0 {
		chunks = append(chunks, fmt.Sprintf("$%.2f/%.2f", b.CostUSD, b.CostBudgetUSD))
		if p := b.CostUSD / b.CostBudgetUSD; p > pct {
			pct = p
		}
	} else if b.CostUSD > 0 {
		chunks = append(chunks, fmt.Sprintf("$%.2f", b.CostUSD))
	}
	if len(chunks) == 0 {
		return ""
	}

	colour := colorGreen
	switch {
	case pct >= 1.0:
		colour = colorRed
	case pct >= 0.75:
		colour = colorYellow
	}

	body := "⟐ " + strings.Join(chunks, " · ")
	return lipgloss.NewStyle().Foreground(colour).Background(colorBg).Render(body)
}

// formatTokensCompact renders a token count using compact K/M suffixes, e.g.
// 500 → "500", 12345 → "12.3K", 1_500_000 → "1.5M".
func formatTokensCompact(n int64) string {
	abs := n
	if abs < 0 {
		abs = -abs
	}
	switch {
	case abs >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case abs >= 1_000:
		return fmt.Sprintf("%.1fK", float64(n)/1_000)
	default:
		return fmt.Sprintf("%d", n)
	}
}

func (m Model) renderInput() string {
	var parts []string

	// Show response panel above input when available
	if m.askResponse != "" {
		question := dimStyle.Render(" Q: ") + truncateStr(m.askQuery, m.width-frameH-12)
		closeBtn := closeBtnStyle.Render(" [×] ")

		scrollIndicator := ""
		if m.askLines > maxAskResponseLines {
			pct := float64(m.askScrollY) / float64(m.askLines-maxAskResponseLines) * 100
			scrollIndicator = dimStyle.Render(fmt.Sprintf("  ↑↓ %.0f%%", pct))
		}

		label := question + closeBtn + scrollIndicator

		// Render full markdown content — scrolling handles truncation.
		// Word-wrap to the actual content area (panel inside border+padding).
		rendered := renderAgentMarkdown(m.askResponse, m.width-frameH, 500)

		// Apply scroll: extract lines and pick the visible window.
		allLines := strings.Split(rendered, "\n")
		total := len(allLines)
		start := total - maxAskResponseLines - m.askScrollY
		if start < 0 {
			start = 0
		}
		end := start + maxAskResponseLines
		if end > total {
			end = total
		}
		visible := strings.Join(allLines[start:end], "\n")
		// Cap visible lines when terminal is short.
		visibleLines := maxAskResponseLines
		responseH := 2 + 1 + visibleLines // border(2) + header(1) + content
		maxAskH := m.height * 2 / 5
		if responseH > maxAskH && maxAskH > 5 {
			visibleLines = maxAskH - 3 // subtract border(2) + header(1)
			responseH = maxAskH
		}
		visible = padToLines(visible, visibleLines)

		responseContent := label + "\n" + visible
		parts = append(parts, borderStyle.Width(m.width-borderH).MaxHeight(responseH).Render(responseContent))
	}

	// Input box styled like Claude Code: rounded border with the textarea
	// inside. The textarea renders its own "❯" prompt, placeholder, cursor,
	// and handles paste / word-delete / multi-line editing natively.
	var content string
	if m.askLoading {
		frame := spinnerFrames[m.spinnerFrame]
		content = inputStyle.Render("❯ ") + dimStyle.Render(fmt.Sprintf("%s thinking...", frame))
	} else {
		content = m.ta.View()
	}

	boxStyle := borderStyle
	if m.ta.Focused() {
		boxStyle = focusedBorderStyle
	}
	parts = append(parts, boxStyle.Width(m.width-borderH).Render(content))
	return lipgloss.JoinVertical(lipgloss.Left, parts...)
}

func (e logEntry) render() string {
	ts := dimStyle.Render(e.ts.Format("15:04:05"))
	var badge string
	switch e.level {
	case "ERROR":
		badge = errStyle.Render("ERR")
	case "WARN":
		badge = warnStyle.Render("WRN")
	case "INFO":
		badge = infoStyle.Render("INF")
	case "DEBUG":
		badge = debugStyle.Render("DBG")
	case "AGENT":
		badge = agentStyle.Render("AGT")
	case "ORDER":
		badge = priceStyle.Render("ORD")
	default:
		badge = dimStyle.Render("   ")
	}
	return fmt.Sprintf("%s %s %s", ts, badge, e.text)
}

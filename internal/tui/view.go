package tui

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/azhar/cerebro/internal/domain"
	"github.com/charmbracelet/lipgloss"
	"charm.land/glamour/v2"
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

	header := m.renderHeader()
	watch := m.renderWatchPanel()
	middle := m.renderMiddle()
	agentPanel := m.renderAgentPanel()
	statusBar := m.renderStatusBar()
	inputBar := m.renderInput()

	result := lipgloss.JoinVertical(lipgloss.Left,
		header, watch, middle, agentPanel, statusBar, inputBar)

	// Pad to exactly m.height lines so the TUI fills the terminal.
	result = padToLines(result, m.height)
	return result
}

// ─── Panel renderers ─────────────────────────────────────────────────────────

func (m Model) renderHeader() string {
	appName := appHeaderStyle.Render(" Cerebro ")
	clockStr := m.now.Format("2006-01-02 15:04:05 MST")
	clock := clockStyle.Render(clockStr)
	appW := lipgloss.Width(appName)
	clockW := lipgloss.Width(clock)
	spacer := strings.Repeat(" ", max(0, m.width-appW-clockW))
	return appName + spacer + clock
}

func (m Model) renderWatchPanel() string {
	symCount := len(m.quotes)

	// Build header with scroll indicators
	title := "Market Watch"
	if symCount > maxWatchLines {
		title = fmt.Sprintf("Market Watch (%d/%d ↕)", m.watchScrollY+1, symCount)
	}
	if m.watchScrollX > 0 {
		title += " ←"
	}
	totalW := watchTotalContentWidth()
	availW := m.watchContentWidth()
	if m.watchScrollX+availW < totalW {
		title += " →"
	}
	header := headerStyle.Render(title)

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
		headerRow := hdrSym + hdrLast + hdrChg + hdrBA + hdrSpread + hdrVol

		rows := []string{headerRow}
		for _, sym := range page {
			q := m.quotes[sym]
			rows = append(rows, formatWatchRow(q, colSymbol, colLast, colChg, colBidAsk, colSpread, colVol))
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
	return style.Width(availW).MaxHeight(outerH).Render(header + "\n" + content)
}

func formatWatchRow(q quoteState, wSym, wLast, wChg, wBA, wSpread, wVol int) string {
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
	spread := q.ask - q.bid
	spreadStr := lipgloss.NewStyle().Width(wSpread).Render(formatPrice(spread))

	// Volume
	volStr := lipgloss.NewStyle().Width(wVol).Render(formatVolume(q.volume24h))

	return symStr + lastStr + chgStr + bidAskStr + spreadStr + volStr
}

func formatPrice(v float64) string {
	if v == 0 {
		return "-"
	}
	if math.Abs(v) >= 1000 {
		return fmt.Sprintf("%.2f", v)
	}
	if math.Abs(v) >= 1 {
		return fmt.Sprintf("%.4f", v)
	}
	return fmt.Sprintf("%.6f", v)
}

func formatChange(chg, chgPct float64) string {
	if chg == 0 && chgPct == 0 {
		return dimStyle.Render("-")
	}
	sign := "+"
	if chg < 0 {
		sign = ""
	}
	text := fmt.Sprintf("%s%s (%s%.2f%%)", sign, formatPrice(math.Abs(chg)), sign, chgPct)
	if chg >= 0 {
		return priceStyle.Render(text)
	}
	return errStyle.Render(text)
}

func formatBidAsk(bid, ask float64) string {
	if bid == 0 && ask == 0 {
		return dimStyle.Render("-")
	}
	return fmt.Sprintf("%s / %s", formatPrice(bid), formatPrice(ask))
}

func formatVolume(v float64) string {
	if v == 0 {
		return "-"
	}
	switch {
	case v >= 1e9:
		return fmt.Sprintf("%.1fB", v/1e9)
	case v >= 1e6:
		return fmt.Sprintf("%.0fM", v/1e6)
	case v >= 1e3:
		return fmt.Sprintf("%.0fK", v/1e3)
	default:
		return fmt.Sprintf("%.0f", v)
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
	inputH := 1

	askH := 0
	if m.askResponse != "" {
		askH = askResponseH
		// Cap ask panel to at most 40% of terminal height so the middle section stays usable.
		maxAskH := m.height * 2 / 5
		if askH > maxAskH && maxAskH > 5 {
			askH = maxAskH
		}
	}

	return m.height - headerH - watchH - agentH - statusH - inputH - askH
}

func (m *Model) renderMiddle() string {
	middleH := m.middleHeight()

	gap := 1
	totalContentW := m.width - 2*borderH - gap
	if totalContentW < 10 {
		totalContentW = 10
	}

	posContentW := totalContentW / 3
	logContentW := totalContentW - posContentW

	contentH := middleH - 2
	if contentH < 1 {
		contentH = 1
	}

	posContent := truncateLines(m.renderPositions(contentH), contentH)
	logContent := truncateLines(m.renderLogPanel(contentH), contentH)

	positions := borderStyle.Width(posContentW).MaxHeight(contentH + 2).Render(posContent)
	logStyle := borderStyle
	if m.focusedPanel == focusLog {
		logStyle = focusedBorderStyle
	}
	logPanel := logStyle.Width(logContentW).MaxHeight(contentH + 2).Render(logContent)

	joined := lipgloss.JoinHorizontal(lipgloss.Top, positions, " ", logPanel)
	return lipgloss.PlaceHorizontal(m.width, lipgloss.Left, joined)
}

func (m *Model) renderPositions(contentH int) string {
	header := headerStyle.Render("Active Positions")
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

		pnl := p.UnrealizedPnLPct().StringFixed(2) + "%"
		var pnlStr string
		if p.UnrealizedPnLPct().IsPositive() {
			pnlStr = priceStyle.Render("+" + pnl)
		} else if p.UnrealizedPnLPct().IsNegative() {
			pnlStr = errStyle.Render(pnl)
		} else {
			pnlStr = pnl
		}

		sideStr := strings.ToUpper(string(p.Side))
		if p.Side == domain.SideBuy {
			sideStr = priceStyle.Bold(true).Render(sideStr)
		} else {
			sideStr = errStyle.Bold(true).Render(sideStr)
		}

		posLines := []string{
			"  " + dimStyle.Render(string(p.Venue)),
			"  " + symStyle.Render(string(p.Symbol)) + "    " + sideStr,
			"  " + lbl.Render(dimStyle.Render("QTY")) + "  " + p.Quantity.String(),
			"  " + lbl.Render(dimStyle.Render("ENTRY")) + "  " + formatPositionPrice(p.EntryPrice.StringFixed(2)),
			"  " + lbl.Render(dimStyle.Render("CURRENT")) + "  " + formatPositionPrice(p.CurrentPrice.StringFixed(2)),
			"  " + lbl.Render(dimStyle.Render("pnl")) + "  " + pnlStr,
		}
		if !p.StopLoss.IsZero() || !p.TakeProfit1.IsZero() {
			posLines = append(posLines,
				"  "+lbl.Render(dimStyle.Render("SL"))+"  "+formatPositionPrice(p.StopLoss.StringFixed(2)),
				"  "+lbl.Render(dimStyle.Render("TP1"))+"  "+formatPositionPrice(p.TakeProfit1.StringFixed(2)),
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

func (m *Model) renderLogPanel(contentH int) string {
	header := headerStyle.Render("Activity & Log")

	logLines := contentH - 1
	if logLines < 1 {
		logLines = 1
	}

	if len(m.logs) == 0 {
		return header + "\n" + dimStyle.Render("  Waiting for activity…")
	}

	total := len(m.logs)

	start := total - logLines - m.logScrollY
	if start < 0 {
		start = 0
	}
	end := start + logLines
	if end > total {
		end = total
		start = end - logLines
		if start < 0 {
			start = 0
		}
	}

	window := m.logs[start:end]

	gap := 1
	availContentW := m.width - 2*borderH - gap
	posContentW := availContentW / 3
	logContentW := availContentW - posContentW
	if logContentW < 10 {
		logContentW = 10
	}
	truncateStyle := lipgloss.NewStyle().Width(logContentW)

	rendered := make([]string, 0, len(window))
	for _, e := range window {
		line := truncateStyle.Render(e.render())
		rendered = append(rendered, line)
	}

	return header + "\n" + strings.Join(rendered, "\n")
}

func (m *Model) renderAgentPanel() string {
	header := headerStyle.Render("Agent Activity")
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
			content.WriteString(dimStyle.Render("  " + truncateStr(lastCompleted.err, m.width-borderH-4)) + "\n")
		} else {
			summary := fmt.Sprintf(" ✓ %s %s (%s)",
				lastCompleted.agent, lastCompleted.symbol, elapsed)
			content.WriteString(priceStyle.Render(summary) + "\n")
			if lastCompleted.content != "" {
				remaining := contentH - usedLines - 1
				if remaining < 1 {
					remaining = 1
				}
				rendered := renderAgentMarkdown(lastCompleted.content, m.width-borderH, remaining)
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
	mouseLabel := "mouse:on"
	if !m.mouseEnabled {
		mouseLabel = "mouse:off"
	}
	var text string
	if m.heartbeat == "" {
		text = fmt.Sprintf("  ♥ waiting for first heartbeat…  %s  Ctrl+O toggle", mouseLabel)
	} else {
		text = fmt.Sprintf(" %s  ♥  %s  %s  Ctrl+O toggle", m.heartbeatAt.Format("15:04:05"), m.heartbeat, mouseLabel)
	}
	maxContent := m.width
	runes := []rune(text)
	if len(runes) > maxContent {
		runes = runes[:maxContent]
	}
	text = string(runes)
	return lipgloss.NewStyle().
		Background(lipgloss.Color("235")).
		Foreground(lipgloss.Color("7")).
		Width(m.width).
		Render(text)
}

func (m Model) renderInput() string {
	var parts []string

	// Show response panel above input when available
	if m.askResponse != "" {
		question := dimStyle.Render(" Q: ") + truncateStr(m.askQuery, m.width-borderH-12)
		closeBtn := closeBtnStyle.Render(" [×] ")

		scrollIndicator := ""
		if m.askLines > maxAskResponseLines {
			pct := float64(m.askScrollY) / float64(m.askLines-maxAskResponseLines) * 100
			scrollIndicator = dimStyle.Render(fmt.Sprintf("  ↑↓ %.0f%%", pct))
		}

		label := question + closeBtn + scrollIndicator

		// Render full markdown content — scrolling handles truncation.
		rendered := renderAgentMarkdown(m.askResponse, m.width-borderH, 500)

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

	// Loading indicator or normal prompt
	if m.askLoading {
		frame := spinnerFrames[m.spinnerFrame]
		prompt := inputStyle.Render("/ask > ")
		loading := dimStyle.Render(fmt.Sprintf(" %s thinking...", frame))
		parts = append(parts, prompt+loading)
		return lipgloss.JoinVertical(lipgloss.Left, parts...)
	}

	prompt := inputStyle.Render("/ask > ")
	cursor := "▌"
	if !m.inputActive {
		cursor = dimStyle.Render("▌")
	}
	parts = append(parts, prompt+m.input+cursor)
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

package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/azhar/cerebro/internal/domain"
	"github.com/azhar/cerebro/internal/port"
	"github.com/charmbracelet/lipgloss"
	"github.com/shopspring/decimal"
)

// ─── Bias / Signals panel ────────────────────────────────────────────────────

func (m *Model) renderBiasPanel(width, contentH int) string {
	header := panelHeaderBias.Render("Bias / Signals")

	maxLines := contentH - 1
	if maxLines < 1 {
		maxLines = 1
	}

	if len(m.biasOrder) == 0 {
		return header + "\n" + dimStyle.Render("  Awaiting screening agent…")
	}

	// Show most-recently-updated first.
	syms := append([]domain.Symbol(nil), m.biasOrder...)
	sort.SliceStable(syms, func(i, j int) bool {
		return m.biasResults[syms[i]].CachedAt.After(m.biasResults[syms[j]].CachedAt)
	})

	limit := maxBiasRows
	if limit > maxLines {
		limit = maxLines
	}
	if limit > len(syms) {
		limit = len(syms)
	}
	syms = syms[:limit]

	// Reserve column widths within the panel. `width` is the OUTER VISIBLE
	// panel width; the actual content area inside the bordered + padded box
	// is `width - frameH`.
	innerW := width - frameH
	if innerW < 14 {
		innerW = 14
	}
	const symColW = 15
	const scoreColW = 9
	ageColW := innerW - symColW - scoreColW - 1
	if ageColW < 4 {
		ageColW = 4
	}

	pad := lipgloss.NewStyle().Width
	hdrRow := pad(symColW).Render(dimStyle.Render("Symbol")) +
		pad(scoreColW).Render(dimStyle.Render("Bias")) +
		pad(ageColW).Render(dimStyle.Render("Age"))

	rows := []string{hdrRow}
	now := time.Now()
	for _, s := range syms {
		b := m.biasResults[s]
		var scoreStr string
		switch b.Score {
		case domain.BiasBullish:
			scoreStr = priceStyle.Render("Bullish")
		case domain.BiasBearish:
			scoreStr = errStyle.Render("Bearish")
		default:
			scoreStr = warnStyle.Render("Neutral")
		}
		age := formatAge(now.Sub(b.CachedAt))
		if b.IsExpired() {
			age = errStyle.Render(age + "·exp")
		}
		row := pad(symColW).Render(symStyle.Render(string(s))) +
			pad(scoreColW).Render(scoreStr) +
			pad(ageColW).Render(dimStyle.Render(age))
		rows = append(rows, row)
	}

	if len(rows) > maxLines {
		rows = rows[:maxLines]
	}
	return header + "\n" + strings.Join(rows, "\n")
}

// ─── Macro panel ─────────────────────────────────────────────────────────────

func (m *Model) renderMacroPanel(width, contentH int) string {
	header := panelHeaderMacro.Render("Macro")
	maxLines := contentH - 1
	if maxLines < 1 {
		maxLines = 1
	}

	innerW := width - frameH
	if innerW < 14 {
		innerW = 14
	}

	// Always show all four indicators so the panel feels stable.
	lines := []string{
		formatMacroFearGreed(m.macro.FearGreed, innerW),
		formatMacroFunding(m.macro.BTCFundingRate, innerW),
		formatMacroOI(m.macro.BTCOpenInterest, innerW),
		formatMacroLongShort(m.macro.BTCLongShort, innerW),
	}
	if !m.macro.UpdatedAt.IsZero() && len(lines) < maxLines {
		lines = append(lines, dimStyle.Render("  updated "+formatAge(time.Since(m.macro.UpdatedAt))+" ago"))
	}

	if len(lines) > maxLines {
		lines = lines[:maxLines]
	}
	return header + "\n" + strings.Join(lines, "\n")
}

func formatMacroFearGreed(fg domain.FearGreedIndex, innerW int) string {
	const label = "  F&G    "
	if fg.Value == 0 && fg.Category == "" {
		return label + dimStyle.Render("—")
	}
	gaugeW := innerW - len(label) - 8
	if gaugeW < 6 {
		gaugeW = 6
	}
	if gaugeW > 20 {
		gaugeW = 20
	}
	gauge := renderBarGauge(fg.Value, 100, gaugeW, fearGreedColor(fg.Value))
	val := fmt.Sprintf(" %3d", fg.Value)
	cat := ""
	if fg.Category != "" {
		cat = " " + dimStyle.Render(fg.Category)
	}
	return label + gauge + val + cat
}

func formatMacroFunding(fr domain.FundingRate, innerW int) string {
	_ = innerW
	const label = "  Fund.  "
	if fr.Rate == 0 && fr.FetchedAt.IsZero() {
		return label + dimStyle.Render("—")
	}
	pct := fr.Rate * 100
	var styled string
	if pct >= 0 {
		styled = priceStyle.Render(fmt.Sprintf("+%.4f%%", pct))
	} else {
		styled = errStyle.Render(fmt.Sprintf("%.4f%%", pct))
	}
	suffix := ""
	if !fr.NextFundingTime.IsZero() {
		dt := time.Until(fr.NextFundingTime)
		if dt > 0 {
			suffix = "  " + dimStyle.Render("next "+formatAge(dt))
		}
	}
	return label + styled + suffix
}

func formatMacroOI(oi domain.OpenInterest, innerW int) string {
	_ = innerW
	const label = "  OI     "
	if oi.TotalUSD.IsZero() && oi.FetchedAt.IsZero() {
		return label + dimStyle.Render("—")
	}
	val := formatVolume(oi.TotalUSD)
	chg := oi.Change24h
	var chgStr string
	switch {
	case chg > 0:
		chgStr = priceStyle.Render(fmt.Sprintf("+%.2f%%", chg))
	case chg < 0:
		chgStr = errStyle.Render(fmt.Sprintf("%.2f%%", chg))
	default:
		chgStr = dimStyle.Render("0.00%")
	}
	return label + val + " " + dimStyle.Render("24h") + " " + chgStr
}

func formatMacroLongShort(ls domain.LongShortRatio, innerW int) string {
	_ = innerW
	const label = "  L/S    "
	if ls.GlobalRatio == 0 && ls.FetchedAt.IsZero() {
		return label + dimStyle.Render("—")
	}
	r := ls.GlobalRatio
	var styled string
	switch {
	case r > 1.05:
		styled = priceStyle.Render(fmt.Sprintf("%.2f", r))
	case r < 0.95:
		styled = errStyle.Render(fmt.Sprintf("%.2f", r))
	default:
		styled = warnStyle.Render(fmt.Sprintf("%.2f", r))
	}
	return label + styled
}

// fearGreedColor returns the lipgloss color used for the F&G gauge fill.
func fearGreedColor(v int) lipgloss.Color {
	switch {
	case v < 25:
		return lipgloss.Color("9") // red
	case v < 45:
		return lipgloss.Color("11") // yellow
	case v < 55:
		return lipgloss.Color("8") // grey
	case v < 75:
		return lipgloss.Color("10") // green
	default:
		return lipgloss.Color("13") // magenta (extreme greed)
	}
}

// renderBarGauge renders a horizontal bar of width cells filled proportionally
// to value/max, in the requested color.
func renderBarGauge(value, max, width int, color lipgloss.Color) string {
	if max <= 0 {
		max = 1
	}
	if value < 0 {
		value = 0
	}
	if value > max {
		value = max
	}
	filled := value * width / max
	if filled < 0 {
		filled = 0
	}
	if filled > width {
		filled = width
	}
	full := lipgloss.NewStyle().Foreground(color).Render(strings.Repeat("█", filled))
	empty := dimStyle.Render(strings.Repeat("░", width-filled))
	return full + empty
}

// ─── Agent Runs panel ────────────────────────────────────────────────────────

func (m *Model) renderAgentRunsPanel(width, contentH int) string {
	header := panelHeaderAgent.Render("Agent Runs")
	maxLines := contentH - 1
	if maxLines < 1 {
		maxLines = 1
	}

	innerW := width - frameH
	if innerW < 14 {
		innerW = 14
	}

	if len(m.agentRunOrder) == 0 {
		return header + "\n" + dimStyle.Render("  No runs yet")
	}

	// Show most recent runs first.
	ids := append([]string(nil), m.agentRunOrder...)
	for i, j := 0, len(ids)-1; i < j; i, j = i+1, j-1 {
		ids[i], ids[j] = ids[j], ids[i]
	}
	limit := maxAgentRunRows
	if limit > maxLines {
		limit = maxLines
	}
	if limit > len(ids) {
		limit = len(ids)
	}
	ids = ids[:limit]

	rows := []string{}
	for _, id := range ids {
		run := m.agentRuns[id]
		rows = append(rows, formatAgentRunRow(run, innerW))
	}
	return header + "\n" + strings.Join(rows, "\n")
}

func formatAgentRunRow(run *agentRunState, innerW int) string {
	var icon string
	switch run.step {
	case StepComplete:
		icon = priceStyle.Render("✓")
	case StepError:
		icon = errStyle.Render("✗")
	default:
		icon = warnStyle.Render("•")
	}

	var elapsed time.Duration
	if !run.finished.IsZero() {
		elapsed = run.finished.Sub(run.started)
	} else {
		elapsed = time.Since(run.started)
	}
	elapsed = elapsed.Truncate(10 * time.Millisecond)

	right := dimStyle.Render(elapsed.String())
	rightW := lipgloss.Width(right)

	left := fmt.Sprintf(" %s %s", icon, run.agent)
	if run.symbol != "" {
		left += " " + dimStyle.Render(string(run.symbol))
	}
	leftW := lipgloss.Width(left)

	gap := innerW - leftW - rightW
	if gap < 1 {
		gap = 1
	}
	return left + strings.Repeat(" ", gap) + right
}

// ─── Account / PnL panel (LG and up) ──────────────────────────────────────────

func (m *Model) renderAccountPanel(width, contentH int) string {
	_ = width
	header := panelHeaderAccount.Render("Account")
	maxLines := contentH - 1
	if maxLines < 1 {
		maxLines = 1
	}

	lines := []string{}

	if len(m.positionRows) == 0 {
		lines = append(lines, dimStyle.Render("  No exposure"))
	} else {
		totalUSD := decimal.Zero
		totalROI := decimal.Zero
		quote := ""
		for _, p := range m.positionRows {
			totalUSD = totalUSD.Add(p.UnrealizedPnL())
			totalROI = totalROI.Add(p.UnrealizedPnLROI())
			if quote == "" {
				quote = p.Symbol.QuoteAsset()
			}
		}
		if quote == "" {
			quote = "USDT"
		}
		avgROI := decimal.Zero
		if len(m.positionRows) > 0 {
			avgROI = totalROI.Div(decimal.NewFromInt(int64(len(m.positionRows))))
		}
		lines = append(lines,
			"  "+dimStyle.Render("positions")+"  "+fmt.Sprintf("%d", len(m.positionRows)),
			"  "+dimStyle.Render("total pnl")+"  "+formatUSD(totalUSD, quote),
			"  "+dimStyle.Render("avg roi  ")+"  "+formatPercent(avgROI),
		)
	}

	if m.heartbeat != "" {
		lines = append(lines, "  "+dimStyle.Render("hb       ")+"  "+truncateStr(m.heartbeat, width-frameH-12))
	}

	if len(lines) > maxLines {
		lines = lines[:maxLines]
	}
	return header + "\n" + strings.Join(lines, "\n")
}

func formatPercent(v decimal.Decimal) string {
	switch {
	case v.IsPositive():
		return priceStyle.Render(fmt.Sprintf("+%s%%", v.StringFixed(2)))
	case v.IsNegative():
		return errStyle.Render(fmt.Sprintf("%s%%", v.StringFixed(2)))
	default:
		return dimStyle.Render("0.00%")
	}
}

func formatUSD(v decimal.Decimal, quote string) string {
	s := v.StringFixed(2)
	if quote != "" {
		s += " " + quote
	}
	switch {
	case v.IsPositive():
		return priceStyle.Render("+" + s)
	case v.IsNegative():
		return errStyle.Render(s)
	default:
		return dimStyle.Render(s)
	}
}

// ─── News panel (LG and up) ──────────────────────────────────────────────────
// Repurposed from the previous Calendar placeholder; the combined news
// runner (CryptoPanic + FinancialJuice) feeds this panel with sentiment-
// tagged headlines, deduped by ID and sorted newest-first. The Finnhub
// economic calendar still runs behind the scenes and drives the risk
// blackout — it is not surfaced here to keep the panel focused.

func (m *Model) renderCalendarPanel(width, contentH int) string {
	header := panelHeaderCalendar.Render("News")
	maxLines := contentH - 1
	if maxLines < 1 {
		maxLines = 1
	}

	if !m.newsSet || len(m.news.Items) == 0 {
		lines := []string{
			dimStyle.Render("  No headlines yet"),
			dimStyle.Render("  (waiting for news scrape)"),
		}
		if len(lines) > maxLines {
			lines = lines[:maxLines]
		}
		return header + "\n" + strings.Join(lines, "\n")
	}

	// Responsive two-line layout per item, e.g.:
	//
	//     ▲ 19m · financialjuice.com
	//       WATCH LIVE: Trump Participates in Senate
	//       Hearing on Economy and Iran Tensions
	//
	// The marker (▲/▼/·) carries the sentiment so the title itself stays
	// in default colour and remains readable on small terminals. Title
	// text is wrapped within the panel's inner content area so nothing
	// spills past the border.
	//
	// Layout adapts to the column width:
	//   innerW ≥ 32  → 2-space gutter, 4-space title indent, blank row
	//                  between items, up to 2 title lines per item.
	//   22 ≤ innerW  → tighter 1/2 indents, no blank separator, still
	//                  allows 2 title lines so long headlines remain
	//                  readable.
	//   < 22         → single-line collapsed rows: "marker age title…"
	//                  (falls back to the legacy dense format so very
	//                  small columns stay informative).
	innerW := width - frameH
	if innerW < 10 {
		innerW = 10
	}

	switch {
	case innerW >= 32:
		return header + "\n" + m.renderNewsSpacious(innerW, maxLines, 2, 4, true, 2)
	case innerW >= 22:
		return header + "\n" + m.renderNewsSpacious(innerW, maxLines, 1, 2, false, 2)
	default:
		return header + "\n" + m.renderNewsCompact(innerW, maxLines)
	}
}

// renderNewsSpacious emits the two-line layout (meta + wrapped title).
// gutter is the leading indent on the meta line; titleIndent is the indent
// on title lines; separator toggles a blank row between items; titleMaxLines
// caps the number of wrapped title lines per item.
func (m *Model) renderNewsSpacious(innerW, maxLines, gutter, titleIndent int, separator bool, titleMaxLines int) string {
	titleWidth := innerW - titleIndent
	if titleWidth < 10 {
		titleWidth = 10
	}
	metaBudget := innerW - gutter
	if metaBudget < 8 {
		metaBudget = 8
	}

	lines := make([]string, 0, maxLines)
	for i, item := range m.news.Items {
		if len(lines) >= maxLines {
			break
		}

		metaLine := strings.Repeat(" ", gutter) + newsMetaLine(item, metaBudget)
		lines = append(lines, metaLine)

		if len(lines) >= maxLines {
			break
		}
		titleLines := wrapTitle(item.Title, titleWidth, titleMaxLines)
		for _, tl := range titleLines {
			if len(lines) >= maxLines {
				break
			}
			lines = append(lines, strings.Repeat(" ", titleIndent)+tl)
		}

		if separator && i < len(m.news.Items)-1 && len(lines) < maxLines-1 {
			lines = append(lines, "")
		}
	}
	return strings.Join(lines, "\n")
}

// renderNewsCompact emits a single line per item: age + truncated title.
// Used for very narrow columns where a two-line layout would leave too
// few items visible.
func (m *Model) renderNewsCompact(innerW, maxLines int) string {
	lines := make([]string, 0, maxLines)
	for _, item := range m.news.Items {
		if len(lines) >= maxLines {
			break
		}
		age := ""
		if !item.PublishedAt.IsZero() {
			age = formatAge(time.Since(item.PublishedAt))
		}
		// Budget the prefix "  <age> " (gutter=2, age ~3, trailing
		// space) so the title gets the rest.
		prefix := "  "
		prefixVisible := 2 // gutter
		if age != "" {
			prefix += dimStyle.Render(age) + " "
			prefixVisible += len(age) + 1
		}
		titleBudget := innerW - prefixVisible
		if titleBudget < 6 {
			titleBudget = 6
		}
		title := wrapTitle(item.Title, titleBudget, 1)
		titleStr := ""
		if len(title) > 0 {
			titleStr = title[0]
		}
		lines = append(lines, prefix+titleStr)
	}
	return strings.Join(lines, "\n")
}

// newsMetaLine builds the meta row "<age> <source>" and truncates the
// source so the whole rendered line fits within budget runes of visible
// width. Age and source are dimmed.
func newsMetaLine(item port.NewsItem, budget int) string {
	age := ""
	if !item.PublishedAt.IsZero() {
		age = formatAge(time.Since(item.PublishedAt))
	}
	source := newsSourceLabel(item.Domain, item.Source)

	// Visible characters used by the fixed parts: len(age) + 1 space
	// between age and source.
	used := 0
	if age != "" {
		used += len([]rune(age))
	}
	if source != "" && used > 0 {
		used += 1
	}
	if n := used + len([]rune(source)); n > budget {
		avail := budget - used
		if avail < 1 {
			source = ""
		} else {
			runes := []rune(source)
			if avail > 1 && len(runes) > avail {
				source = string(runes[:avail-1]) + "…"
			} else if len(runes) > avail {
				source = string(runes[:avail])
			}
		}
	}

	parts := make([]string, 0, 2)
	if age != "" {
		parts = append(parts, dimStyle.Render(age))
	}
	if source != "" {
		parts = append(parts, dimStyle.Render(source))
	}
	return strings.Join(parts, " ")
}

// newsSourceLabel returns a compact source string, preferring the upstream
// publisher domain when present and falling back to the ingest source name
// (e.g. "cryptopanic", "financialjuice"). The "www." prefix is stripped
// because it adds noise without information.
func newsSourceLabel(domain, source string) string {
	d := strings.TrimSpace(domain)
	d = strings.TrimPrefix(d, "www.")
	if d != "" {
		return d
	}
	return strings.TrimSpace(source)
}

// wrapTitle breaks s into up to maxLines lines no wider than width runes.
// Lines prefer word boundaries; the very last line is ellipsised with "…"
// when the title overflows. Tokens longer than width are hard-cut. The
// output never contains a line wider than width runes.
func wrapTitle(s string, width, maxLines int) []string {
	s = strings.TrimSpace(s)
	if s == "" || width <= 0 || maxLines <= 0 {
		return nil
	}
	out := make([]string, 0, maxLines)
	remaining := s
	for len(out) < maxLines && remaining != "" {
		isLast := len(out) == maxLines-1
		line, rest := takeNewsLine(remaining, width, isLast)
		out = append(out, line)
		remaining = strings.TrimLeft(rest, " ")
	}
	// Overflow → tag the last visible line with an ellipsis. We trim to
	// width-1 runes first so the appended "…" keeps the line ≤ width.
	if remaining != "" && len(out) > 0 {
		last := []rune(out[len(out)-1])
		if len(last) > width-1 {
			last = last[:width-1]
		}
		// Drop any trailing whitespace before the ellipsis for a tidier look.
		for len(last) > 0 && last[len(last)-1] == ' ' {
			last = last[:len(last)-1]
		}
		out[len(out)-1] = string(last) + "…"
	}
	return out
}

// takeNewsLine returns the longest prefix of s that fits in width runes.
// When last is false it prefers a break on the last whitespace inside the
// window and falls back to a hard cut for unbroken tokens. When last is
// true it always hard-cuts at width so the caller can pack as many
// characters as possible onto the final line before appending the
// ellipsis. The returned rest may contain leading whitespace from the
// break point — callers strip it.
func takeNewsLine(s string, width int, last bool) (line, rest string) {
	runes := []rune(s)
	if len(runes) <= width {
		return s, ""
	}
	cut := width
	if !last {
		for cut > 0 && runes[cut] != ' ' {
			cut--
		}
		if cut == 0 {
			// Long token with no whitespace inside the window — hard cut.
			cut = width
		}
	}
	return string(runes[:cut]), string(runes[cut:])
}

// ─── Health panel (XL) ────────────────────────────────────────────────────────

func (m *Model) renderHealthPanel(width, contentH int) string {
	_ = width
	header := panelHeaderHealth.Render("Health")
	maxLines := contentH - 1
	if maxLines < 1 {
		maxLines = 1
	}

	hbLine := "  " + dimStyle.Render("hb     ") + "  " + dimStyle.Render("waiting")
	if !m.heartbeatAt.IsZero() {
		age := time.Since(m.heartbeatAt)
		marker := priceStyle.Render("●")
		if age > 30*time.Second {
			marker = errStyle.Render("●")
		} else if age > 10*time.Second {
			marker = warnStyle.Render("●")
		}
		hbLine = "  " + marker + " " + dimStyle.Render("hb   ") + "  " + dimStyle.Render(formatAge(age)+" ago")
	}

	// Count recent ERROR-level entries in the log (last 10 minutes).
	cutoff := time.Now().Add(-10 * time.Minute)
	var errCount int
	for i := len(m.logs) - 1; i >= 0; i-- {
		e := m.logs[i]
		if e.ts.Before(cutoff) {
			break
		}
		if e.level == "ERROR" {
			errCount++
		}
	}
	errLine := "  " + dimStyle.Render("errors ") + "  "
	if errCount > 0 {
		errLine += errStyle.Render(fmt.Sprintf("%d in 10m", errCount))
	} else {
		errLine += priceStyle.Render("0")
	}

	lines := []string{
		hbLine,
		errLine,
		"  " + dimStyle.Render("logs   ") + "  " + fmt.Sprintf("%d/%d", len(m.logs), m.maxLogLines),
	}
	if len(lines) > maxLines {
		lines = lines[:maxLines]
	}
	return header + "\n" + strings.Join(lines, "\n")
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

// formatAge renders a duration in a compact human form (e.g. "12s", "3m", "1h").
func formatAge(d time.Duration) string {
	if d < 0 {
		d = -d
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

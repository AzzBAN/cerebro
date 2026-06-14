package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/azhar/cerebro/internal/port"
)

// TestWrapTitle verifies the news-panel title wrapper preserves word
// boundaries, respects the width and line caps, and ellipsises overflow.
func TestWrapTitle(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		width    int
		maxLines int
		want     []string
	}{
		{
			name:     "fits on single line",
			input:    "Brent Crude futures settle at $108.10",
			width:    40,
			maxLines: 2,
			want:     []string{"Brent Crude futures settle at $108.10"},
		},
		{
			name:     "wraps to two lines on word boundary",
			input:    "WATCH LIVE: Trump Participates in Senate Hearing",
			width:    20,
			maxLines: 2,
			// Line 1 breaks at the last space ≤ width: "WATCH LIVE: Trump"
			// (17 runes). The remainder ("Participates in Senate Hearing")
			// won't fit in a single 20-rune line so the last line is
			// hard-cut and ellipsised to fill the column.
			want: []string{"WATCH LIVE: Trump", "Participates in Sen…"},
		},
		{
			name:     "ellipsises only the last line on overflow",
			input:    "one two three four five six seven eight nine ten eleven twelve",
			width:    12,
			maxLines: 2,
			// Line 1 word-wraps to "one two" (whitespace at index 7).
			// Last line hard-cuts to "three four f" (12), then trims
			// trailing whitespace and appends "…" → "three four…" (11).
			want: []string{"one two", "three four…"},
		},
		{
			name:     "hard cuts a token longer than width",
			input:    "supercalifragilisticexpialidocious is long",
			width:    10,
			maxLines: 2,
			// No whitespace in window → hard cut. Last line ellipsised
			// because there's still text remaining.
			want: []string{"supercalif", "ragilisti…"},
		},
		{
			name:     "empty input yields nil",
			input:    "",
			width:    20,
			maxLines: 2,
			want:     nil,
		},
		{
			name:     "zero width yields nil",
			input:    "anything",
			width:    0,
			maxLines: 2,
			want:     nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := wrapTitle(tc.input, tc.width, tc.maxLines)
			if len(got) != len(tc.want) {
				t.Fatalf("len mismatch: got %d %q, want %d %q",
					len(got), got, len(tc.want), tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("line %d: got %q, want %q", i, got[i], tc.want[i])
				}
			}
		})
	}
}

// TestWrapTitleNeverExceedsWidth fuzz-checks that no produced line exceeds
// the requested width — this is a hard invariant for keeping the news
// panel from spilling outside its column.
func TestWrapTitleNeverExceedsWidth(t *testing.T) {
	corpus := []string{
		"Trump tells Congress Iran hostilities authorisation may extend overnight",
		"NYMEX WTI Crude June futures settle at $80.42",
		"IRGC: Controlling nearly 2,000 km of strategic Persian Gulf shipping lane",
		"BTC ETF inflows reach record $1.2B in single trading day amid tightening",
	}
	for _, w := range []int{10, 18, 24, 32, 60} {
		for _, m := range []int{1, 2, 3} {
			for _, s := range corpus {
				lines := wrapTitle(s, w, m)
				if len(lines) > m {
					t.Fatalf("width=%d max=%d: produced %d lines, exceeds cap",
						w, m, len(lines))
				}
				for _, ln := range lines {
					if n := len([]rune(ln)); n > w {
						t.Errorf("width=%d max=%d: line %q has %d runes",
							w, m, ln, n)
					}
				}
			}
		}
	}
}

// TestNewsSourceLabel covers the domain → source fallback logic used in
// the meta line.
func TestNewsSourceLabel(t *testing.T) {
	tests := []struct {
		domain, source, want string
	}{
		{"coindesk.com", "cryptopanic", "coindesk.com"},
		{"www.financialjuice.com", "financialjuice", "financialjuice.com"},
		{"", "financialjuice", "financialjuice"},
		{"  ", "  cryptopanic  ", "cryptopanic"},
		{"", "", ""},
	}
	for _, tc := range tests {
		got := newsSourceLabel(tc.domain, tc.source)
		if got != tc.want {
			t.Errorf("newsSourceLabel(%q,%q)=%q want %q",
				tc.domain, tc.source, got, tc.want)
		}
	}
}

// TestRenderNewsPanelLayout asserts the two-line layout: a meta line with
// source + age, followed by an indented title line. The Calendar render
// function is the News panel renderer (legacy name kept for compatibility).
func TestRenderNewsPanelLayout(t *testing.T) {
	m := New(500)
	m.news = NewsSnapshot{
		Items: []port.NewsItem{
			{
				Title:       "Bitcoin ETF approved by SEC",
				Domain:      "coindesk.com",
				Source:      "cryptopanic",
				Sentiment:   "bullish",
				PublishedAt: time.Now().Add(-5 * time.Minute),
			},
			{
				Title:       "WATCH LIVE: Trump Participates in Senate Hearing on Economy",
				Domain:      "financialjuice.com",
				Source:      "financialjuice",
				Sentiment:   "neutral",
				PublishedAt: time.Now().Add(-19 * time.Minute),
			},
		},
		UpdatedAt: time.Now(),
	}
	m.newsSet = true

	out := m.renderCalendarPanel(60, 20)
	plain := stripANSI(out)
	rows := strings.Split(plain, "\n")

	// Header is row 0.
	if !strings.Contains(rows[0], "News") {
		t.Fatalf("missing News header in row 0: %q", rows[0])
	}

	// First item meta line: 2-space gutter, contains the marker + source.
	if !strings.HasPrefix(rows[1], "  ") {
		t.Errorf("meta line missing 2-space gutter: %q", rows[1])
	}
	if !strings.Contains(rows[1], "coindesk.com") {
		t.Errorf("meta line missing source: %q", rows[1])
	}

	// Title line: 4-space indent, contains the title text.
	if !strings.HasPrefix(rows[2], "    ") {
		t.Errorf("title line missing 4-space indent: %q", rows[2])
	}
	if !strings.Contains(rows[2], "Bitcoin") {
		t.Errorf("title line missing title text: %q", rows[2])
	}

	// Second item appears after a blank separator at some later row.
	joined := strings.Join(rows, "\n")
	if !strings.Contains(joined, "financialjuice.com") {
		t.Errorf("second item meta missing: %q", joined)
	}
	if !strings.Contains(joined, "Trump") {
		t.Errorf("second item title missing: %q", joined)
	}
}

// TestRenderNewsPanelNeverOverflows asserts that no rendered row (header
// or any body line) exceeds the panel's inner content width across a range
// of column widths and a corpus of realistic headlines, some of which are
// deliberately long enough to wrap multiple times. This is the hard
// invariant that prevents the "text bleeds past the border" regression.
func TestRenderNewsPanelNeverOverflows(t *testing.T) {
	corpus := []port.NewsItem{
		{
			Title:       "Monday FX Options Expiries",
			Domain:      "financialjuice.com",
			Source:      "financialjuice",
			Sentiment:   "neutral",
			PublishedAt: time.Now().Add(-14 * time.Minute),
		},
		{
			Title:       "WATCH LIVE: Trump Participates in Seniors Event 3 PM ET",
			Domain:      "financialjuice.com",
			Source:      "financialjuice",
			Sentiment:   "neutral",
			PublishedAt: time.Now().Add(-36 * time.Minute),
		},
		{
			Title:       "Brent Crude futures settle at $108.17/bbl, down $2.23, 2.02%",
			Domain:      "financialjuice.com",
			Source:      "financialjuice",
			Sentiment:   "bearish",
			PublishedAt: time.Now().Add(-40 * time.Minute),
		},
		{
			Title:       "Trump tells Congress Iran hostilities have terminated – NBC",
			Domain:      "financialjuice.com",
			Source:      "financialjuice",
			Sentiment:   "neutral",
			PublishedAt: time.Now().Add(-57 * time.Minute),
		},
		{
			Title:       "IRGC: Controlling nearly 2,000 km of strategic Persian Gulf shipping lane",
			Domain:      "financialjuice.com",
			Source:      "financialjuice",
			Sentiment:   "bullish",
			PublishedAt: time.Now().Add(-51 * time.Minute),
		},
	}

	m := New(500)
	m.news = NewsSnapshot{Items: corpus, UpdatedAt: time.Now()}
	m.newsSet = true

	// Cover spacious, mid, compact and pathological widths.
	for _, outer := range []int{20, 28, 36, 48, 60, 80, 120} {
		innerW := outer - frameH
		if innerW < 1 {
			continue
		}
		for _, h := range []int{6, 12, 20} {
			out := m.renderCalendarPanel(outer, h)
			for i, row := range strings.Split(stripANSI(out), "\n") {
				if w := runeWidth(row); w > innerW {
					t.Errorf("outer=%d inner=%d h=%d row %d exceeds: %d runes: %q",
						outer, innerW, h, i, w, row)
				}
			}
		}
	}
}

// TestRenderNewsPanelCompactBranch verifies that very narrow columns fall
// back to the single-line compact layout (no two-line pairs).
func TestRenderNewsPanelCompactBranch(t *testing.T) {
	m := New(500)
	m.news = NewsSnapshot{
		Items: []port.NewsItem{
			{Title: "Short headline", Domain: "financialjuice.com", Sentiment: "neutral", PublishedAt: time.Now().Add(-5 * time.Minute)},
			{Title: "Another short one", Domain: "coindesk.com", Sentiment: "bullish", PublishedAt: time.Now().Add(-10 * time.Minute)},
		},
		UpdatedAt: time.Now(),
	}
	m.newsSet = true

	// outer=24 → innerW=20, falls below the spacious threshold (22) and
	// below the tight threshold — use compact single-line branch.
	out := m.renderCalendarPanel(24, 10)
	rows := strings.Split(stripANSI(out), "\n")

	// Header + 1 row per item; no blank separators.
	var bodyRows []string
	for _, r := range rows[1:] {
		if r == "" {
			continue
		}
		bodyRows = append(bodyRows, r)
	}
	if len(bodyRows) != 2 {
		t.Fatalf("expected 2 body rows in compact mode, got %d: %q",
			len(bodyRows), bodyRows)
	}
	// Each row must contain the age (marker + age + title in one line).
	for _, r := range bodyRows {
		if !strings.Contains(r, "m") { // "5m"/"10m" suffix
			t.Errorf("compact row missing age marker: %q", r)
		}
	}
}

// runeWidth counts visible runes in a plain (non-ANSI) string. The TUI
// layout uses rune width for all sizing calculations.
func runeWidth(s string) int { return len([]rune(s)) }

package telegram

import (
	"strings"
	"testing"
)

func TestMarkdownToHTML(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "h3 header becomes bold",
			in:   "### Market Overview",
			want: "<b>Market Overview</b>",
		},
		{
			name: "bullet with bold symbol",
			in:   "- **BTC/USDT-PERP**: Bullish",
			want: "• <b>BTC/USDT-PERP</b>: Bullish",
		},
		{
			name: "italic underscores left intact, single asterisk italicised",
			in:   "_No actionable opportunities identified this cycle._",
			want: "_No actionable opportunities identified this cycle._",
		},
		{
			name: "html-significant chars are escaped",
			in:   "P&L is < 0 and risk > limit",
			want: "P&amp;L is &lt; 0 and risk &gt; limit",
		},
		{
			name: "inline code",
			in:   "use `make build` to compile",
			want: "use <code>make build</code> to compile",
		},
		{
			name: "trading data with plus/percent/parens is untouched",
			in:   "- BTC +1.36% (conf=0.82) $4.97B",
			want: "• BTC +1.36% (conf=0.82) $4.97B",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := markdownToHTML(tt.in); got != tt.want {
				t.Errorf("markdownToHTML(%q)\n got: %q\nwant: %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestMarkdownToHTML_NoRawMarkers(t *testing.T) {
	// A realistic multi-section summary must not leak raw "###" or "**".
	in := strings.Join([]string{
		"### Market Overview",
		"Broadly bullish across monitored assets (6 bullish / 3 bearish / 1 neutral).",
		"",
		"### Per-Symbol Bias",
		"- **BTC/USDT-PERP**: Bullish — Price +1.36% over 24h.",
	}, "\n")

	got := markdownToHTML(in)
	if strings.Contains(got, "###") {
		t.Errorf("output still contains raw '###':\n%s", got)
	}
	if strings.Contains(got, "**") {
		t.Errorf("output still contains raw '**':\n%s", got)
	}
	if !strings.Contains(got, "<b>Market Overview</b>") {
		t.Errorf("header not converted to bold:\n%s", got)
	}
}

func TestStripHTMLTags(t *testing.T) {
	in := "<b>BTC</b> &amp; ETH &lt;up&gt;"
	want := "BTC & ETH <up>"
	if got := stripHTMLTags(in); got != want {
		t.Errorf("stripHTMLTags(%q) = %q, want %q", in, got, want)
	}
}

func TestSplitForTelegram(t *testing.T) {
	t.Run("short message stays single", func(t *testing.T) {
		got := splitForTelegram("hello")
		if len(got) != 1 || got[0] != "hello" {
			t.Errorf("expected single unchanged chunk, got %v", got)
		}
	})

	t.Run("splits on line boundaries under limit", func(t *testing.T) {
		line := strings.Repeat("x", 1000)
		msg := strings.Join([]string{line, line, line, line, line}, "\n") // ~5004 chars
		chunks := splitForTelegram(msg)
		if len(chunks) < 2 {
			t.Fatalf("expected message to split into multiple chunks, got %d", len(chunks))
		}
		for i, c := range chunks {
			if len(c) > telegramMaxMessageLen {
				t.Errorf("chunk %d exceeds limit: %d", i, len(c))
			}
		}
	})

	t.Run("hard-splits an oversized single line", func(t *testing.T) {
		line := strings.Repeat("y", telegramMaxMessageLen+500)
		chunks := splitForTelegram(line)
		if len(chunks) < 2 {
			t.Fatalf("expected oversized line to hard-split, got %d", len(chunks))
		}
		for i, c := range chunks {
			if len(c) > telegramMaxMessageLen {
				t.Errorf("chunk %d exceeds limit: %d", i, len(c))
			}
		}
	})
}

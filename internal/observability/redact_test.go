package observability

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestRedactURL(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    string
		exact   bool
		mustHave []string
		mustNot  []string
	}{
		{
			name:    "finnhub-style token leaks",
			in:      "https://finnhub.io/api/v1/calendar/economic?from=2026-04-19&to=2026-04-20&token=d7gk911r01qmqj45n520",
			mustHave: []string{"token=REDACTED", "from=2026-04-19", "to=2026-04-20"},
			mustNot:  []string{"d7gk911r01qmqj45n520"},
		},
		{
			name:    "no query string is unchanged",
			in:      "https://example.com/path",
			want:    "https://example.com/path",
			exact:   true,
		},
		{
			name:    "case-insensitive parameter name",
			in:      "https://example.com/?ApiKey=hunter2&q=ok",
			mustHave: []string{"ApiKey=REDACTED", "q=ok"},
			mustNot:  []string{"hunter2"},
		},
		{
			name:    "non-sensitive params survive",
			in:      "https://example.com/x?from=a&to=b",
			want:    "https://example.com/x?from=a&to=b",
			exact:   true,
		},
		{
			name:    "non-URL string passes through",
			in:      "this is not a url",
			want:    "this is not a url",
			exact:   true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := RedactURL(tt.in)
			if tt.exact && got != tt.want {
				t.Fatalf("got %q want %q", got, tt.want)
			}
			for _, s := range tt.mustHave {
				if !strings.Contains(got, s) {
					t.Errorf("output %q missing %q", got, s)
				}
			}
			for _, s := range tt.mustNot {
				if strings.Contains(got, s) {
					t.Errorf("output %q still contains secret %q", got, s)
				}
			}
		})
	}
}

func TestRedactErrorString_GoURLError(t *testing.T) {
	// Reproduces the exact pattern observed in cerebro.log:
	//   `Get "https://finnhub.io/...?token=...": context deadline exceeded`
	in := `finnhub calendar: http: Get "https://finnhub.io/api/v1/calendar/economic?from=2026-04-19&to=2026-04-20&token=d7gk911r01qmqj45n520d7gk911r01qmqj45n52g": context deadline exceeded`
	got := RedactErrorString(in)
	if strings.Contains(got, "d7gk911r01qmqj45n520d7gk911r01qmqj45n52g") {
		t.Fatalf("token leaked: %q", got)
	}
	if !strings.Contains(got, "context deadline exceeded") {
		t.Fatalf("non-URL portion of message lost: %q", got)
	}
	if !strings.Contains(got, "token=REDACTED") {
		t.Fatalf("expected token=REDACTED, got: %q", got)
	}
}

func TestRedactErr_NilSafe(t *testing.T) {
	if got := RedactErr(nil); got != "" {
		t.Fatalf("RedactErr(nil) = %q, want empty", got)
	}
	err := fmt.Errorf("wrap: %w", errors.New(`Get "https://x.io/?token=abc": canceled`))
	got := RedactErr(err)
	if strings.Contains(got, "abc") {
		t.Fatalf("token leaked through error wrap: %q", got)
	}
}

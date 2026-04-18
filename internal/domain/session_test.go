package domain

import (
	"testing"
	"time"
)

func TestSessionWindow_Contains(t *testing.T) {
	tests := []struct {
		name string
		w    SessionWindow
		time string // UTC time in "15:04" format
		want bool
	}{
		{
			name: "NY open at 12:30",
			w:    SessionWindowNYOpen,
			time: "12:30",
			want: true,
		},
		{
			name: "NY open at start boundary",
			w:    SessionWindowNYOpen,
			time: "12:00",
			want: true,
		},
		{
			name: "NY open at end boundary excluded",
			w:    SessionWindowNYOpen,
			time: "14:00",
			want: false,
		},
		{
			name: "NY open before window",
			w:    SessionWindowNYOpen,
			time: "11:59",
			want: false,
		},
		{
			name: "NY open after window",
			w:    SessionWindowNYOpen,
			time: "14:01",
			want: false,
		},
		{
			name: "Asian open at 01:00",
			w:    SessionWindowAsianOpen,
			time: "01:00",
			want: true,
		},
		{
			name: "overlap window at 13:59",
			w:    SessionWindowOverlap,
			time: "13:59",
			want: true,
		},
		{
			name: "overlap window at 16:00 excluded",
			w:    SessionWindowOverlap,
			time: "16:00",
			want: false,
		},
	}

	baseDate := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parsed, err := time.Parse("15:04", tt.time)
			if err != nil {
				t.Fatalf("invalid time %q: %v", tt.time, err)
			}
			testTime := time.Date(
				baseDate.Year(), baseDate.Month(), baseDate.Day(),
				parsed.Hour(), parsed.Minute(), 0, 0, time.UTC,
			)
			got := tt.w.Contains(testTime)
			if got != tt.want {
				t.Errorf("Contains(%v) = %v, want %v", testTime, got, tt.want)
			}
		})
	}
}

func TestSessionWindow_ContainsTimezoneConversion(t *testing.T) {
	w := SessionWindowNYOpen // 12:00–14:00 UTC

	// 12:30 UTC = 07:30 EST — should be inside
	est := time.FixedZone("EST", -5*60*60)
	local := time.Date(2026, 1, 1, 7, 30, 0, 0, est)

	if !w.Contains(local) {
		t.Error("Contains should convert to UTC before checking")
	}
}

func TestSessionWindowFor(t *testing.T) {
	tests := []struct {
		name   string
		filter SessionFilter
		want   *SessionWindow
	}{
		{"NY open", SessionNYOpen, &SessionWindowNYOpen},
		{"Asian open", SessionAsianOpen, &SessionWindowAsianOpen},
		{"Overlap", SessionOverlap, &SessionWindowOverlap},
		{"All returns nil", SessionAll, nil},
		{"unknown returns nil", SessionFilter("unknown"), nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SessionWindowFor(tt.filter)
			if tt.want == nil {
				if got != nil {
					t.Error("expected nil, got non-nil")
				}
				return
			}
			if got == nil {
				t.Fatal("expected non-nil, got nil")
			}
			if got.Name != tt.want.Name ||
				got.StartHour != tt.want.StartHour ||
				got.EndHour != tt.want.EndHour {
				t.Errorf("got %+v, want %+v", *got, *tt.want)
			}
		})
	}
}

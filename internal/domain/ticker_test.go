package domain

import (
	"testing"
	"time"
)

func TestTickerSummary_IsNewListing(t *testing.T) {
	now := time.Now().UTC()
	tests := []struct {
		name     string
		listedAt time.Time
		maxAge   time.Duration
		want     bool
	}{
		{name: "zero time", listedAt: time.Time{}, maxAge: 30 * 24 * time.Hour, want: false},
		{name: "zero maxAge", listedAt: now.Add(-24 * time.Hour), maxAge: 0, want: false},
		{name: "within window", listedAt: now.Add(-24 * time.Hour), maxAge: 48 * time.Hour, want: true},
		{name: "at edge", listedAt: now.Add(-48 * time.Hour), maxAge: 48 * time.Hour + time.Minute, want: true},
		{name: "outside window", listedAt: now.Add(-60 * 24 * time.Hour), maxAge: 30 * 24 * time.Hour, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts := TickerSummary{ListedAt: tt.listedAt}
			if got := ts.IsNewListing(tt.maxAge); got != tt.want {
				t.Errorf("IsNewListing() = %v, want %v", got, tt.want)
			}
		})
	}
}

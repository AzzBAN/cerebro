package calendar

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// fairEconomyPayload builds a JSON feed body with events at fixed offsets
// (in hours) from the given base time, using the feed's RFC3339 date format.
func fairEconomyPayload(t *testing.T, base time.Time, offsets map[string]int) string {
	t.Helper()
	var sb []byte
	sb = append(sb, '[')
	first := true
	for title, off := range offsets {
		if !first {
			sb = append(sb, ',')
		}
		first = false
		when := base.Add(time.Duration(off) * time.Hour).Format(time.RFC3339)
		entry := fmt.Sprintf(`{"title":%q,"country":"USD","date":%q,"impact":"High","forecast":"0.3%%","previous":"0.2%%"}`, title, when)
		sb = append(sb, entry...)
	}
	sb = append(sb, ']')
	return string(sb)
}

func TestFairEconomy_UpcomingEvents_WindowFiltering(t *testing.T) {
	base := time.Now().UTC()
	// past: -2h (excluded), soon: +3h (included), far: +48h (excluded for a 24h window)
	body := fairEconomyPayload(t, base, map[string]int{
		"Past Event": -2,
		"Soon Event": 3,
		"Far Event":  48,
	})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	feed := NewFairEconomy()
	prev := faireconomyURL
	faireconomyURL = srv.URL
	defer func() { faireconomyURL = prev }()

	events, err := feed.UpcomingEvents(context.Background(), 24)
	if err != nil {
		t.Fatalf("UpcomingEvents: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event in 24h window, got %d: %+v", len(events), events)
	}
	if events[0].Title != "Soon Event" {
		t.Fatalf("expected Soon Event, got %q", events[0].Title)
	}
	if events[0].Impact != "high" {
		t.Fatalf("expected impact high, got %q", events[0].Impact)
	}
	if events[0].Currency != "USD" {
		t.Fatalf("expected currency USD, got %q", events[0].Currency)
	}
}

func TestFairEconomy_NormalizeImpact(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"high", "High", "high"},
		{"medium", "Medium", "medium"},
		{"low", "Low", "low"},
		{"holiday falls back to low", "Holiday", "low"},
		{"empty falls back to low", "", "low"},
		{"whitespace trimmed", "  HIGH  ", "high"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizeImpact(tt.in); got != tt.want {
				t.Errorf("normalizeImpact(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestFairEconomy_Non200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	feed := NewFairEconomy()
	prev := faireconomyURL
	faireconomyURL = srv.URL
	defer func() { faireconomyURL = prev }()

	_, err := feed.UpcomingEvents(context.Background(), 24)
	if err == nil {
		t.Fatal("expected error on non-200 status")
	}
}

func TestFairEconomy_PreservesCtxDeadlineSentinel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer srv.Close()

	feed := NewFairEconomy()
	prev := faireconomyURL
	faireconomyURL = srv.URL
	defer func() { faireconomyURL = prev }()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := feed.UpcomingEvents(ctx, 24)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected DeadlineExceeded, got: %v", err)
	}
}

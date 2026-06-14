package calendar

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestUpcomingEvents_TokenNeverLeaksOnError verifies that any HTTP-level
// failure scrubs the API token before it can reach the log/error pipeline.
//
// Regression guard: production logs at one point captured a Finnhub API token
// inside `Get "https://finnhub.io/...?token=xxx": context deadline exceeded`
// because the stdlib *url.Error embeds the full request URL.
func TestUpcomingEvents_TokenNeverLeaksOnError(t *testing.T) {
	const secret = "supersecrettokenvalue123"

	// Server hangs forever so the client times out → forces a *url.Error
	// from the http roundtripper (which embeds the URL with token).
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer srv.Close()

	feed := New(secret)
	feed.client.Timeout = 50 * time.Millisecond
	prev := finnhubBaseURL
	finnhubBaseURL = srv.URL
	defer func() { finnhubBaseURL = prev }()

	_, err := feed.UpcomingEvents(context.Background(), 24)
	if err == nil {
		t.Fatal("expected error from hanging server, got nil")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("token leaked in error: %q", err)
	}
	if !strings.Contains(err.Error(), "REDACTED") {
		t.Fatalf("expected REDACTED marker in error, got: %q", err)
	}
}

// TestUpcomingEvents_PreservesCtxDeadlineSentinel asserts callers can still
// classify a timeout via errors.Is(err, context.DeadlineExceeded) even after
// redaction.
func TestUpcomingEvents_PreservesCtxDeadlineSentinel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer srv.Close()

	feed := New("anything")
	prev := finnhubBaseURL
	finnhubBaseURL = srv.URL
	defer func() { finnhubBaseURL = prev }()

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

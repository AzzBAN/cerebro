package cryptopanic

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestParseRetryAfter(t *testing.T) {
	tests := []struct {
		in   string
		want time.Duration
	}{
		{"", 0},
		{"5", 5 * time.Second},
		{" 60 ", 60 * time.Second},
		{"not-a-number", 0},
		{"-1", 0}, // negative seconds treated as "no retry"; HTTP date fallback will also fail
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			if got := parseRetryAfter(tt.in); got != tt.want {
				t.Errorf("parseRetryAfter(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

// errorString satisfies error without needing fmt in the failure path.
type clientErr string

func (e clientErr) Error() string { return string(e) }

// TestTransientError_IsRetryable verifies errors.As picks up transient
// errors — this is the hook FallbackFeed uses to distinguish structural
// breakage (ErrBadPayload) from upstream hiccups.
func TestTransientError_IsRetryable(t *testing.T) {
	te := &transientError{
		status:     http.StatusBadGateway,
		retryAfter: 60 * time.Second,
		inner:      clientErr("upstream 502"),
	}
	// errors.As should match.
	var target *transientError
	if !errors.As(error(te), &target) {
		t.Fatal("errors.As failed to match *transientError")
	}
	if target.status != http.StatusBadGateway {
		t.Errorf("status = %d, want 502", target.status)
	}
	if target.retryAfter != 60*time.Second {
		t.Errorf("retryAfter = %v, want 60s", target.retryAfter)
	}
	// ErrBadPayload must NOT be a transientError — the two sentinels
	// are the core of the retry/fallback decision.
	if errors.As(ErrBadPayload, &target) {
		t.Fatal("ErrBadPayload should not satisfy errors.As(*transientError)")
	}
}

// TestClient_Retries502 drives the retry loop against an httptest server
// that returns 502 the first 2 times and a valid payload on the 3rd.
// This proves the retry + backoff interplay with Retry-After works.
func TestClient_Retries502(t *testing.T) {
	var calls atomic.Int32
	mux := http.NewServeMux()

	// Minimal homepage that sets a csrftoken cookie.
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			http.SetCookie(w, &http.Cookie{
				Name:  "csrftoken",
				Value: "TESTCSRFTOKEN00000000000000000000000000000000000000000000000000000",
				Path:  "/",
			})
			_, _ = w.Write([]byte("<html/>"))
			return
		}
		http.NotFound(w, r)
	})

	// /web-api/posts/ returns 502 twice, then a stub envelope with an
	// empty-but-valid-shaped encrypted payload. We don't exercise the
	// decrypt path here (covered by the golden vector test); we just
	// assert the client retries and surfaces the final error.
	mux.HandleFunc("/web-api/posts/", func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if n < 3 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusBadGateway)
			_, _ = fmt.Fprintln(w, `{"title":"Bad gateway"}`)
			return
		}
		// On attempt 3: return a valid-shape envelope with an empty S so
		// decodeEnvelope returns the "empty encrypted payload" error.
		// That error is NOT a transientError — the retry loop stops and
		// surfaces it to the caller, which is exactly the semantics we
		// want: a bad payload exits the loop and bubbles to the fallback.
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":true,"s":""}`))
	})

	srv := newTestServer(t, mux)
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	_, err := c.FetchPosts(testCtx(t), Query{})
	if err == nil {
		t.Fatal("expected error from empty payload; got nil")
	}
	if calls.Load() != 3 {
		t.Errorf("got %d calls to /web-api/posts/, want 3 (2 retries + 1 success)", calls.Load())
	}
	// Error should be the server-code / empty-payload kind, NOT the
	// transient retry-exhausted kind.
	if strings.Contains(err.Error(), "exhausted") {
		t.Errorf("unexpected exhaustion error: %v", err)
	}
}

// TestClient_RateLimitedArmsCooldown verifies that a 429 response stops
// the retry loop immediately, arms the cooldown window, and causes the
// next FetchPosts call to short-circuit with ErrRateLimited without
// hitting the network. This is the mechanism that suppresses log-spam
// during Cloudflare throttling windows.
func TestClient_RateLimitedArmsCooldown(t *testing.T) {
	var posts atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{
			Name: "csrftoken", Value: "CSRFTOKEN429TEST00000000000000000000000000000000000000000000000000",
			Path: "/",
		})
		_, _ = w.Write([]byte("<html/>"))
	})
	mux.HandleFunc("/web-api/posts/", func(w http.ResponseWriter, r *http.Request) {
		posts.Add(1)
		w.Header().Set("Retry-After", "60")
		w.WriteHeader(http.StatusTooManyRequests)
	})

	srv := newTestServer(t, mux)
	defer srv.Close()

	c := newTestClient(t, srv.URL)

	// First call: one POST, then immediate fail with ErrRateLimited.
	// No retry storm, no "exhausted 3 attempts".
	_, err := c.FetchPosts(testCtx(t), Query{})
	if err == nil {
		t.Fatal("expected ErrRateLimited; got nil")
	}
	if !errors.Is(err, ErrRateLimited) {
		t.Errorf("error %v does not wrap ErrRateLimited", err)
	}
	if strings.Contains(err.Error(), "exhausted") {
		t.Errorf("429 should not trigger retry exhaustion; got %q", err)
	}
	if got := posts.Load(); got != 1 {
		t.Errorf("posts called %d times, want 1 (no retries on 429)", got)
	}

	// Second call within the cooldown window: no network at all.
	_, err = c.FetchPosts(testCtx(t), Query{})
	if !errors.Is(err, ErrRateLimited) {
		t.Fatalf("second call: expected ErrRateLimited; got %v", err)
	}
	if got := posts.Load(); got != 1 {
		t.Errorf("posts called %d times after cooldown arm, want 1 (short-circuit)", got)
	}

	// Sanity: cooldown window reflects the Retry-After header (60s),
	// capped by maxRateLimitCooldown.
	remain, active := c.cooldownRemaining(time.Now())
	if !active {
		t.Fatal("cooldown should be active after 429")
	}
	if remain > 60*time.Second || remain < 55*time.Second {
		t.Errorf("cooldown remaining = %v, want ~60s (from Retry-After)", remain)
	}
}

// TestClient_RateLimitedDefaultCooldown verifies we fall back to
// defaultRateLimitCooldown when the upstream omits Retry-After.
func TestClient_RateLimitedDefaultCooldown(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{
			Name: "csrftoken", Value: "CSRFTOKEN429NOHDR000000000000000000000000000000000000000000000000",
			Path: "/",
		})
		_, _ = w.Write([]byte("<html/>"))
	})
	mux.HandleFunc("/web-api/posts/", func(w http.ResponseWriter, r *http.Request) {
		// No Retry-After header.
		w.WriteHeader(http.StatusTooManyRequests)
	})

	srv := newTestServer(t, mux)
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	_, err := c.FetchPosts(testCtx(t), Query{})
	if !errors.Is(err, ErrRateLimited) {
		t.Fatalf("expected ErrRateLimited; got %v", err)
	}
	remain, active := c.cooldownRemaining(time.Now())
	if !active {
		t.Fatal("cooldown should be active")
	}
	if remain < defaultRateLimitCooldown-5*time.Second || remain > defaultRateLimitCooldown {
		t.Errorf("cooldown remaining = %v, want ~%v (default)", remain, defaultRateLimitCooldown)
	}
}

// TestClient_Exhausts502 verifies the client gives up after maxAttempts
// of repeated 502s and surfaces a retry-exhaustion error.
func TestClient_Exhausts502(t *testing.T) {
	var calls atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{
			Name: "csrftoken", Value: "CSRFTOKENEXHAUSTTEST00000000000000000000000000000000000000000000",
			Path: "/",
		})
		_, _ = w.Write([]byte("<html/>"))
	})
	mux.HandleFunc("/web-api/posts/", func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Retry-After", "0")
		w.WriteHeader(http.StatusBadGateway)
	})

	srv := newTestServer(t, mux)
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	_, err := c.FetchPosts(testCtx(t), Query{})
	if err == nil {
		t.Fatal("expected exhaustion error; got nil")
	}
	if !strings.Contains(err.Error(), "exhausted") {
		t.Errorf("error %q does not mention exhaustion", err)
	}
	if calls.Load() != 3 {
		t.Errorf("calls = %d, want 3 (maxAttempts)", calls.Load())
	}

	// The wrapped error must unwrap to a transientError so the fallback
	// can distinguish it from ErrBadPayload.
	var te *transientError
	if !errors.As(err, &te) {
		t.Errorf("errors.As(*transientError) failed; error chain: %v", err)
	}
	// And it must NOT be ErrBadPayload — this is the critical wire that
	// keeps the browser fallback dormant for upstream outages.
	if errors.Is(err, ErrBadPayload) {
		t.Error("retry-exhausted 502 should not be ErrBadPayload")
	}
}

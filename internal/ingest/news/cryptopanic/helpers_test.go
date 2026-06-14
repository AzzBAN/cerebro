package cryptopanic

import (
	"context"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"testing"
	"time"
)

// newTestServer wraps an httptest.Server and registers cleanup.
func newTestServer(t *testing.T, h http.Handler) *httptest.Server {
	t.Helper()
	return httptest.NewServer(h)
}

// newTestClient builds a Client pointed at the given base URL (usually
// httptest.Server.URL) with a short timeout so tests run fast.
func newTestClient(t *testing.T, base string) *Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookie jar: %v", err)
	}
	return &Client{
		http: &http.Client{
			Timeout: 2 * time.Second,
			Jar:     jar,
		},
		userAgent: "cryptopanic-test",
		baseURL:   base,
	}
}

// testCtx returns a context with a short deadline and cancels it on test
// cleanup. 5s is more than enough for any retry loop in these tests.
func testCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return ctx
}

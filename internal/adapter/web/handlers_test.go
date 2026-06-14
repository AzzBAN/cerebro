package web

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestStaticFrontendServed verifies the embedded Next.js export is served at
// "/" when built into the binary. When the frontend has not been built (the
// dist/.gitkeep placeholder only), the route returns the "not built"
// placeholder instead — either outcome is a valid pass, but a 404 or 500 is
// a wiring bug.
func TestStaticFrontendServed(t *testing.T) {
	s := newTestServer("", nil)
	mux := http.NewServeMux()
	s.routes(mux)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET / status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if _, built := frontendFS(); built {
		// Built frontend: index.html should contain the app shell.
		if len(body) == 0 {
			t.Error("served empty body from embedded frontend")
		}
	} else {
		// Not built: placeholder message.
		if rec.Header().Get("Content-Type") == "" {
			t.Error("placeholder response missing content-type")
		}
	}
}

// TestStateRouteGatedByAuth verifies the wired /api/state route (not just the
// bare middleware) rejects unauthenticated requests.
func TestStateRouteGatedByAuth(t *testing.T) {
	s := newTestServer("secret", nil)
	mux := http.NewServeMux()
	s.routes(mux)

	tests := []struct {
		name       string
		header     string
		wantStatus int
	}{
		{"no token", "", http.StatusUnauthorized},
		{"valid token", "Bearer secret", http.StatusOK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/state", nil)
			if tt.header != "" {
				req.Header.Set("Authorization", tt.header)
			}
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
		})
	}
}

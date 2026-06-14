package web

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/azhar/cerebro/internal/positionproposal"
)

// stubProposalController records confirm/reject calls for handler tests.
type stubProposalController struct {
	confirmed  []string
	rejected   []string
	confirmErr error
}

func (s *stubProposalController) Confirm(_ context.Context, id string) error {
	s.confirmed = append(s.confirmed, id)
	return s.confirmErr
}

func (s *stubProposalController) Reject(id string) error {
	s.rejected = append(s.rejected, id)
	return nil
}

func TestHandleProposalConfirm(t *testing.T) {
	ctrl := &stubProposalController{}
	srv := newTestServer("", nil)
	srv.SetProposalController(ctrl)

	req := httptest.NewRequest(http.MethodPost, "/api/proposals/abc-123/confirm", nil)
	req.SetPathValue("id", "abc-123")
	rec := httptest.NewRecorder()
	srv.handleProposalAction(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if len(ctrl.confirmed) != 1 || ctrl.confirmed[0] != "abc-123" {
		t.Fatalf("confirmed = %v, want [abc-123]", ctrl.confirmed)
	}
}

func TestHandleProposalReject(t *testing.T) {
	ctrl := &stubProposalController{}
	srv := newTestServer("", nil)
	srv.SetProposalController(ctrl)

	req := httptest.NewRequest(http.MethodPost, "/api/proposals/xyz/reject", nil)
	req.SetPathValue("id", "xyz")
	rec := httptest.NewRecorder()
	srv.handleProposalAction(rec, req)

	if rec.Code != http.StatusOK || len(ctrl.rejected) != 1 {
		t.Fatalf("reject failed: code=%d rejected=%v", rec.Code, ctrl.rejected)
	}
}

func TestHandleProposalUnknownID(t *testing.T) {
	ctrl := &stubProposalController{confirmErr: fmt.Errorf("%w: x", positionproposal.ErrUnknownProposal)}
	srv := newTestServer("", nil)
	srv.SetProposalController(ctrl)

	req := httptest.NewRequest(http.MethodPost, "/api/proposals/x/confirm", nil)
	req.SetPathValue("id", "x")
	rec := httptest.NewRecorder()
	srv.handleProposalAction(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

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

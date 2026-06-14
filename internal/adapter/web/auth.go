package web

import (
	"crypto/subtle"
	"net/http"
	"strings"
)

// auth wraps a handler with bearer-token authentication. Every /api and /ws
// request must carry `Authorization: Bearer <token>` matching the configured
// WebAuthToken. The comparison is constant-time to avoid timing leaks.
//
// For the WebSocket endpoint, browsers cannot set Authorization headers on the
// WebSocket handshake, so the token may alternatively be supplied via the
// `token` query parameter.
func (s *Server) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.cfg.AuthToken == "" {
			// No token configured: config validation forbids this when
			// web.enabled, so this path only runs in tests/dev. Allow through.
			next.ServeHTTP(w, r)
			return
		}
		if !s.tokenValid(r) {
			w.Header().Set("WWW-Authenticate", "Bearer")
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) tokenValid(r *http.Request) bool {
	var presented string
	if h := r.Header.Get("Authorization"); h != "" {
		presented = strings.TrimPrefix(h, "Bearer ")
	} else {
		presented = r.URL.Query().Get("token")
	}
	if presented == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(presented), []byte(s.cfg.AuthToken)) == 1
}

// checkOrigin enforces the WebSocket Origin allowlist. An empty allowlist
// permits same-origin requests only (browsers omit Origin for same-origin in
// some cases; we accept a missing Origin as same-origin). Configured entries
// are matched exactly.
func (s *Server) checkOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	if len(s.cfg.AllowedOrigins) == 0 {
		// Same-origin only: compare against the request Host.
		return strings.HasSuffix(origin, "://"+r.Host)
	}
	for _, allowed := range s.cfg.AllowedOrigins {
		if origin == allowed {
			return true
		}
	}
	return false
}

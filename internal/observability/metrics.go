//go:build metrics

package observability

import (
	"net/http"

	"log/slog"
)

// MetricsEnabled is true when the binary is built with -tags metrics.
const MetricsEnabled = true

// StartMetricsServer starts a simple HTTP server on the given address.
// Only available when built with -tags metrics (excluded from production binary
// by default to keep the binary small).
//
// In production use a proper Prometheus client; this is a structural placeholder
// that exports the metrics endpoint on /metrics.
func StartMetricsServer(addr string) {
	mux := http.NewServeMux()
	mux.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		// TODO: expose real counters via github.com/prometheus/client_golang
		// when integrating Prometheus in the full production setup.
		_, _ = w.Write([]byte("# Cerebro metrics endpoint\n# Build with -tags metrics to enable\n"))
	})
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	slog.Info("metrics server starting", "addr", addr)
	go func() {
		if err := http.ListenAndServe(addr, mux); err != nil {
			slog.Error("metrics server stopped", "error", err)
		}
	}()
}

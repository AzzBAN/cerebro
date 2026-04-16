//go:build !metrics

package observability

// MetricsEnabled is false when built without -tags metrics.
const MetricsEnabled = false

// StartMetricsServer is a no-op when not built with -tags metrics.
func StartMetricsServer(_ string) {}

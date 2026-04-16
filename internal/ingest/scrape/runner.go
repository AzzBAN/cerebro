package scrape

import (
	"context"
	"log/slog"
	"time"
)

// Runner schedules and retries a scrape job on a fixed interval.
type Runner struct {
	name     string
	interval time.Duration
	timeout  time.Duration
	runFn    func(ctx context.Context) error
}

// NewRunner creates a Runner for a scrape job.
func NewRunner(name string, interval, timeout time.Duration, runFn func(ctx context.Context) error) *Runner {
	return &Runner{
		name:     name,
		interval: interval,
		timeout:  timeout,
		runFn:    runFn,
	}
}

// Run executes the scrape job on schedule until ctx is cancelled.
func (r *Runner) Run(ctx context.Context) error {
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()

	slog.Info("scrape runner started", "name", r.name, "interval", r.interval)
	r.execute(ctx) // run immediately

	for {
		select {
		case <-ctx.Done():
			slog.Info("scrape runner stopping", "name", r.name)
			return nil
		case <-ticker.C:
			r.execute(ctx)
		}
	}
}

func (r *Runner) execute(ctx context.Context) {
	jobCtx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()

	if err := r.runFn(jobCtx); err != nil {
		slog.Error("scrape job failed", "name", r.name, "error", err)
	} else {
		slog.Debug("scrape job complete", "name", r.name)
	}
}

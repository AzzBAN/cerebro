package strategy

import (
	"log/slog"
	"sync"
	"time"

	"github.com/azhar/cerebro/internal/domain"
)

// DedupWindow rejects signals that fire for the same symbol within a configurable
// window. This prevents double exposure when multiple strategies agree simultaneously.
type DedupWindow struct {
	mu      sync.Mutex
	window  time.Duration
	lastSig map[domain.Symbol]time.Time
}

// NewDedupWindow creates a window with the given duration.
func NewDedupWindow(window time.Duration) *DedupWindow {
	return &DedupWindow{
		window:  window,
		lastSig: make(map[domain.Symbol]time.Time),
	}
}

// Allow returns true if the signal should be forwarded, false if it is a duplicate.
// Records the current time for accepted signals.
func (d *DedupWindow) Allow(sig domain.Signal) bool {
	d.mu.Lock()
	defer d.mu.Unlock()

	last, seen := d.lastSig[sig.Symbol]
	if seen && time.Since(last) < d.window {
		slog.Debug("signal deduplicated",
			"symbol", sig.Symbol,
			"strategy", sig.Strategy,
			"last_signal_ago", time.Since(last).Round(time.Second),
		)
		return false
	}
	d.lastSig[sig.Symbol] = time.Now()
	return true
}

// Reset clears all dedup state. Useful between backtest runs.
func (d *DedupWindow) Reset() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.lastSig = make(map[domain.Symbol]time.Time)
}

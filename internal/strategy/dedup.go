package strategy

import (
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/azhar/cerebro/internal/domain"
)

// DedupWindow rejects repeat signals for the same (symbol, strategy, side) tuple
// within a per-strategy window.
type DedupWindow struct {
	mu         sync.Mutex
	defaultWin time.Duration
	lastSig    map[string]time.Time
}

// NewDedupWindow creates a dedup tracker. defaultWindow is used for any signal
// whose strategy does not supply a positive window via Allow.
func NewDedupWindow(defaultWindow time.Duration) *DedupWindow {
	if defaultWindow <= 0 {
		defaultWindow = time.Minute
	}
	return &DedupWindow{
		defaultWin: defaultWindow,
		lastSig:    make(map[string]time.Time),
	}
}

// Allow reports whether sig should be forwarded, recording the time for
// accepted signals. window is the strategy's configured dedup window; values
// <= 0 fall back to the tracker's default.
func (d *DedupWindow) Allow(sig domain.Signal, window time.Duration) bool {
	if window <= 0 {
		window = d.defaultWin
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	genAt := sig.GeneratedAt
	if genAt.IsZero() {
		genAt = time.Now().UTC()
	}

	key := fmt.Sprintf("%s:%s:%s", sig.Symbol, sig.Strategy, sig.Side)
	last, seen := d.lastSig[key]
	if seen && genAt.Sub(last) < window && genAt.Sub(last) >= 0 {
		slog.Debug("signal deduplicated",
			"symbol", sig.Symbol,
			"strategy", sig.Strategy,
			"side", sig.Side,
			"window", window,
			"last_signal_ago", genAt.Sub(last).Round(time.Second),
		)
		return false
	}
	d.lastSig[key] = genAt
	return true
}

// Reset clears all dedup state. Useful between backtest runs.
func (d *DedupWindow) Reset() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.lastSig = make(map[string]time.Time)
}

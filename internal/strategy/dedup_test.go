package strategy

import (
	"testing"
	"time"

	"github.com/azhar/cerebro/internal/domain"
)

func TestDedupWindow_AllowFirst(t *testing.T) {
	d := NewDedupWindow(5 * time.Minute)
	sig := domain.Signal{Symbol: "BTC/USDT", Strategy: "test"}

	if !d.Allow(sig, 5*time.Minute) {
		t.Error("first signal should be allowed")
	}
}

func TestDedupWindow_BlockDuplicate(t *testing.T) {
	d := NewDedupWindow(5 * time.Minute)
	sig := domain.Signal{Symbol: "BTC/USDT", Strategy: "test"}

	d.Allow(sig, 5*time.Minute)
	if d.Allow(sig, 5*time.Minute) {
		t.Error("duplicate signal within window should be blocked")
	}
}

func TestDedupWindow_DifferentSymbols(t *testing.T) {
	d := NewDedupWindow(5 * time.Minute)

	sig1 := domain.Signal{Symbol: "BTC/USDT"}
	sig2 := domain.Signal{Symbol: "ETH/USDT"}

	if !d.Allow(sig1, 5*time.Minute) {
		t.Error("first BTC signal should be allowed")
	}
	if !d.Allow(sig2, 5*time.Minute) {
		t.Error("first ETH signal should be allowed")
	}
}

// Two different strategies firing on the same symbol must NOT block each
// other — the dedup key is (symbol, strategy), not symbol alone.
func TestDedupWindow_DifferentStrategiesSameSymbol(t *testing.T) {
	d := NewDedupWindow(5 * time.Minute)

	tf := domain.Signal{Symbol: "BTC/USDT", Strategy: "trend_following"}
	mr := domain.Signal{Symbol: "BTC/USDT", Strategy: "mean_reversion"}

	if !d.Allow(tf, 5*time.Minute) {
		t.Error("first trend_following signal should be allowed")
	}
	if !d.Allow(mr, 5*time.Minute) {
		t.Error("mean_reversion signal on same symbol should not be blocked by trend_following")
	}
}

func TestDedupWindow_DifferentSidesSameSymbol(t *testing.T) {
	d := NewDedupWindow(5 * time.Minute)
	baseTime := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)

	buySig := domain.Signal{
		Symbol:      "BTC/USDT",
		Strategy:    "trend_following",
		Side:        domain.SideBuy,
		GeneratedAt: baseTime,
	}
	sellSig := domain.Signal{
		Symbol:      "BTC/USDT",
		Strategy:    "trend_following",
		Side:        domain.SideSell,
		GeneratedAt: baseTime.Add(30 * time.Second),
	}

	if !d.Allow(buySig, 5*time.Minute) {
		t.Error("first buy signal should be allowed")
	}
	if !d.Allow(sellSig, 5*time.Minute) {
		t.Error("sell signal on same symbol/strategy should not be blocked by buy signal")
	}
}

func TestDedupWindow_HistoricalDeterministic(t *testing.T) {
	d := NewDedupWindow(5 * time.Minute)
	t0 := time.Date(2024, 6, 1, 10, 0, 0, 0, time.UTC)

	sig1 := domain.Signal{Symbol: "BTC/USDT", Strategy: "test", Side: domain.SideBuy, GeneratedAt: t0}
	sig2 := domain.Signal{Symbol: "BTC/USDT", Strategy: "test", Side: domain.SideBuy, GeneratedAt: t0.Add(2 * time.Minute)}
	sig3 := domain.Signal{Symbol: "BTC/USDT", Strategy: "test", Side: domain.SideBuy, GeneratedAt: t0.Add(6 * time.Minute)}

	if !d.Allow(sig1, 5*time.Minute) {
		t.Error("signal at t0 should be allowed")
	}
	if d.Allow(sig2, 5*time.Minute) {
		t.Error("signal at t0+2m should be blocked (within 5m window)")
	}
	if !d.Allow(sig3, 5*time.Minute) {
		t.Error("signal at t0+6m should be allowed (outside 5m window)")
	}
}

// A zero/negative window falls back to the tracker's default window.
func TestDedupWindow_FallsBackToDefault(t *testing.T) {
	d := NewDedupWindow(5 * time.Minute)
	sig := domain.Signal{Symbol: "BTC/USDT", Strategy: "test"}

	if !d.Allow(sig, 0) {
		t.Error("first signal should be allowed")
	}
	if d.Allow(sig, 0) {
		t.Error("duplicate within default window should be blocked when window <= 0")
	}
}

// Each strategy's own window is honoured independently.
func TestDedupWindow_PerStrategyWindow(t *testing.T) {
	d := NewDedupWindow(5 * time.Minute)
	sig := domain.Signal{Symbol: "BTC/USDT", Strategy: "scalp"}

	d.Allow(sig, 1*time.Nanosecond)
	time.Sleep(10 * time.Millisecond) // wait for the short window to expire

	if !d.Allow(sig, 1*time.Nanosecond) {
		t.Error("signal should be allowed after its short per-strategy window expires")
	}
}

func TestDedupWindow_Reset(t *testing.T) {
	d := NewDedupWindow(5 * time.Minute)
	sig := domain.Signal{Symbol: "BTC/USDT"}

	d.Allow(sig, 5*time.Minute)
	d.Reset()

	if !d.Allow(sig, 5*time.Minute) {
		t.Error("signal should be allowed after reset")
	}
}

func TestDedupWindow_WindowExpiry(t *testing.T) {
	d := NewDedupWindow(1 * time.Nanosecond)
	sig := domain.Signal{Symbol: "BTC/USDT"}

	d.Allow(sig, 1*time.Nanosecond)
	time.Sleep(10 * time.Millisecond) // wait for window to expire

	if !d.Allow(sig, 1*time.Nanosecond) {
		t.Error("signal should be allowed after window expires")
	}
}

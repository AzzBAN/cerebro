package strategy

import (
	"testing"
	"time"

	"github.com/azhar/cerebro/internal/domain"
)

func TestDedupWindow_AllowFirst(t *testing.T) {
	d := NewDedupWindow(5 * time.Minute)
	sig := domain.Signal{Symbol: "BTC/USDT", Strategy: "test"}

	if !d.Allow(sig) {
		t.Error("first signal should be allowed")
	}
}

func TestDedupWindow_BlockDuplicate(t *testing.T) {
	d := NewDedupWindow(5 * time.Minute)
	sig := domain.Signal{Symbol: "BTC/USDT", Strategy: "test"}

	d.Allow(sig)
	if d.Allow(sig) {
		t.Error("duplicate signal within window should be blocked")
	}
}

func TestDedupWindow_DifferentSymbols(t *testing.T) {
	d := NewDedupWindow(5 * time.Minute)

	sig1 := domain.Signal{Symbol: "BTC/USDT"}
	sig2 := domain.Signal{Symbol: "ETH/USDT"}

	if !d.Allow(sig1) {
		t.Error("first BTC signal should be allowed")
	}
	if !d.Allow(sig2) {
		t.Error("first ETH signal should be allowed")
	}
}

func TestDedupWindow_Reset(t *testing.T) {
	d := NewDedupWindow(5 * time.Minute)
	sig := domain.Signal{Symbol: "BTC/USDT"}

	d.Allow(sig)
	d.Reset()

	if !d.Allow(sig) {
		t.Error("signal should be allowed after reset")
	}
}

func TestDedupWindow_WindowExpiry(t *testing.T) {
	d := NewDedupWindow(1 * time.Nanosecond)
	sig := domain.Signal{Symbol: "BTC/USDT"}

	d.Allow(sig)
	time.Sleep(10 * time.Millisecond) // wait for window to expire

	if !d.Allow(sig) {
		t.Error("signal should be allowed after window expires")
	}
}

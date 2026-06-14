package app

import (
	"context"
	"testing"
	"time"
)

func TestMatchGlob(t *testing.T) {
	tests := []struct {
		pattern string
		name    string
		want    bool
	}{
		// Basic wildcard.
		{"bias:*", "bias:BTC/USDT", true},
		{"bias:*", "bias:ETH/USDT", true},
		{"bias:*", "bias:XAU/USDT-PERP", true},
		{"bias:*", "signal:BTC/USDT", false},
		{"bias:*", "bias:", true},

		// Nested slashes.
		{"open_position:*:*", "open_position:binance_spot:BTC/USDT", true},
		{"open_position:binance_futures:*", "open_position:binance_futures:ETH/USDT-PERP", true},

		// Question mark.
		{"bias:BTC/?SDT", "bias:BTC/USDT", true},
		{"bias:BTC/?SDT", "bias:BTC/ESDT", true},
		{"bias:BTC/?SDT", "bias:BTC/SDT", false},

		// Exact match.
		{"exact", "exact", true},
		{"exact", "exactx", false},
		{"exact", "exac", false},

		// Empty strings.
		{"*", "", true},
		{"*", "anything", true},
		{"", "", true},
		{"", "x", false},

		// Multiple wildcards.
		{"*:*:*", "a:b:c", true},
		{"*:*:*", "a:b/c:d/e", true},
	}

	for _, tt := range tests {
		t.Run(tt.pattern+"→"+tt.name, func(t *testing.T) {
			got := matchGlob(tt.pattern, tt.name)
			if got != tt.want {
				t.Errorf("matchGlob(%q, %q) = %v, want %v", tt.pattern, tt.name, got, tt.want)
			}
		})
	}
}

func TestMemoryCacheKeysWithSlash(t *testing.T) {
	ctx := context.Background()
	mc := newMemoryCache()

	// Simulate the screening agent writing bias keys with '/' in symbol names.
	symbols := []string{"BTC/USDT", "ETH/USDT", "XAU/USDT-PERP", "BTC/USDT-PERP"}
	for _, sym := range symbols {
		if err := mc.Set(ctx, "bias:"+sym, []byte(`{"score":50}`), time.Hour); err != nil {
			t.Fatal(err)
		}
	}
	// Also set a non-bias key to ensure it's excluded.
	if err := mc.Set(ctx, "signal:BTC/USDT", []byte(`{}`), time.Hour); err != nil {
		t.Fatal(err)
	}

	keys, err := mc.Keys(ctx, "bias:*")
	if err != nil {
		t.Fatal(err)
	}

	if len(keys) != len(symbols) {
		t.Errorf("Keys(bias:*) returned %d keys, want %d; keys=%v", len(keys), len(symbols), keys)
	}

	// Verify each symbol has a corresponding key.
	keySet := make(map[string]bool, len(keys))
	for _, k := range keys {
		keySet[k] = true
	}
	for _, sym := range symbols {
		if !keySet["bias:"+sym] {
			t.Errorf("missing expected key bias:%s", sym)
		}
	}
}

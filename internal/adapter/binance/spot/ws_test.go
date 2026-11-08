package spot

import (
	"reflect"
	"sync"
	"testing"

	"github.com/azhar/cerebro/internal/domain"
)

func TestSymbolSetEqual(t *testing.T) {
	tests := []struct {
		name string
		a, b []domain.Symbol
		want bool
	}{
		{"both empty", nil, nil, true},
		{"empty vs empty slice", []domain.Symbol{}, []domain.Symbol{}, true},
		{"same order", []domain.Symbol{"A", "B"}, []domain.Symbol{"A", "B"}, true},
		{"different order", []domain.Symbol{"A", "B"}, []domain.Symbol{"B", "A"}, true},
		{"with duplicates", []domain.Symbol{"A", "A", "B"}, []domain.Symbol{"A", "B"}, true},
		{"subset", []domain.Symbol{"A"}, []domain.Symbol{"A", "B"}, false},
		{"superset", []domain.Symbol{"A", "B"}, []domain.Symbol{"A"}, false},
		{"disjoint", []domain.Symbol{"A"}, []domain.Symbol{"B"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := symbolSetEqual(tt.a, tt.b); got != tt.want {
				t.Fatalf("symbolSetEqual(%v, %v) = %v, want %v", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

// TestUpdateSymbols_NoopOnEqualSet verifies that calling UpdateSymbols with
// the same symbol set does NOT signal a reconnect. Without this guard the
// 5-minute discovery watcher tears down the WS on every poll even when the
// merged symbol set is unchanged, surfacing as a stream of
// "spot klines WS disconnected … error=context canceled" warnings.
func TestUpdateSymbols_NoopOnEqualSet(t *testing.T) {
	ws := NewKlinesWS(nil, []domain.Symbol{"BTCUSDT", "ETHUSDT"}, domain.TF1m, nil)

	ws.UpdateSymbols([]domain.Symbol{"ETHUSDT", "BTCUSDT"}) // same set, different order

	select {
	case <-ws.reconnectCh:
		t.Fatal("reconnectCh fired on no-op UpdateSymbols — would cause a spurious disconnect warning")
	default:
	}
}

// TestUpdateSymbols_TriggersReconnectOnChange verifies that a real symbol-set
// change still triggers a reconnect (i.e. the guard above is not too eager).
func TestUpdateSymbols_TriggersReconnectOnChange(t *testing.T) {
	ws := NewKlinesWS(nil, []domain.Symbol{"BTCUSDT"}, domain.TF1m, nil)

	ws.UpdateSymbols([]domain.Symbol{"BTCUSDT", "ETHUSDT"})

	select {
	case <-ws.reconnectCh:
		// expected
	default:
		t.Fatal("reconnectCh did not fire on real symbol change")
	}

	got := ws.getSymbols()
	want := []domain.Symbol{"BTCUSDT", "ETHUSDT"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("symbols = %v, want %v", got, want)
	}
}

// TestUpdateSymbols_NoopPreservesSymbolList guards against a regression where
// the early-return skips assigning the new slice.
func TestUpdateSymbols_NoopPreservesSymbolList(t *testing.T) {
	ws := NewKlinesWS(nil, []domain.Symbol{"BTCUSDT", "ETHUSDT"}, domain.TF1m, nil)
	ws.UpdateSymbols([]domain.Symbol{"BTCUSDT", "ETHUSDT"}) // equal — no-op

	ws.mu.RLock()
	defer ws.mu.RUnlock()
	if len(ws.symbols) != 2 {
		t.Fatalf("symbols mutated on equal-set update: %v", ws.symbols)
	}
}

// TestUpdateSymbols_Concurrent exercises the set-equal guard from multiple
// goroutines to make sure no panic / lost-wakeup occurs when many discovery
// polls overlap.
func TestUpdateSymbols_Concurrent(t *testing.T) {
	ws := NewKlinesWS(nil, []domain.Symbol{"BTCUSDT"}, domain.TF1m, nil)

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ws.UpdateSymbols([]domain.Symbol{"BTCUSDT"})
		}()
	}
	wg.Wait()
}

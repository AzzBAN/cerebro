package agent

import (
	"context"
	"sync"
	"testing"
)

func TestUsage_Add(t *testing.T) {
	t.Run("accumulates across calls", func(t *testing.T) {
		u := &Usage{}
		u.Add(100, 20, 0)
		u.Add(50, 10, 30)
		in, out, cached := u.Snapshot()
		if in != 150 || out != 30 || cached != 30 {
			t.Fatalf("got in=%d out=%d cached=%d, want 150/30/30", in, out, cached)
		}
	})

	t.Run("nil-safe", func(t *testing.T) {
		var u *Usage
		u.Add(100, 20, 0) // must not panic
		in, out, cached := u.Snapshot()
		if in != 0 || out != 0 || cached != 0 {
			t.Fatalf("nil usage should return zeros, got %d/%d/%d", in, out, cached)
		}
	})

	t.Run("clamps negative values to zero", func(t *testing.T) {
		u := &Usage{}
		u.Add(-10, -5, -1)
		in, out, cached := u.Snapshot()
		if in != 0 || out != 0 || cached != 0 {
			t.Fatalf("negative deltas should clamp, got %d/%d/%d", in, out, cached)
		}
	})

	t.Run("concurrent adds are serialized", func(t *testing.T) {
		u := &Usage{}
		var wg sync.WaitGroup
		const workers = 10
		const iters = 100
		for i := 0; i < workers; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for j := 0; j < iters; j++ {
					u.Add(1, 1, 1)
				}
			}()
		}
		wg.Wait()
		in, out, cached := u.Snapshot()
		want := workers * iters
		if in != want || out != want || cached != want {
			t.Fatalf("concurrent accumulation lost updates: got %d/%d/%d want %d/%d/%d",
				in, out, cached, want, want, want)
		}
	})
}

func TestUsageContext(t *testing.T) {
	t.Run("round-trips via context", func(t *testing.T) {
		u := &Usage{}
		ctx := WithUsage(context.Background(), u)
		got := UsageFromCtx(ctx)
		if got != u {
			t.Fatalf("UsageFromCtx returned different pointer: got %p want %p", got, u)
		}
	})

	t.Run("nil usage does not pollute context", func(t *testing.T) {
		ctx := WithUsage(context.Background(), nil)
		if UsageFromCtx(ctx) != nil {
			t.Fatal("nil usage must return nil from context")
		}
	})

	t.Run("missing key returns nil", func(t *testing.T) {
		if UsageFromCtx(context.Background()) != nil {
			t.Fatal("empty context must return nil")
		}
	})
}

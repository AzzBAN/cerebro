package execution

import (
	"context"
	"fmt"
	"time"

	"github.com/azhar/cerebro/internal/port"
)

const dedupTTL = 24 * time.Hour

// DeduplicateOrder uses Redis to prevent double-submission of the same order intent.
// Returns (true, nil) if this is a fresh submission, (false, nil) if already seen.
func DeduplicateOrder(ctx context.Context, cache port.Cache, intentID string) (bool, error) {
	key := fmt.Sprintf("order_dedup:%s", intentID)
	ok, err := cache.SetNX(ctx, key, []byte("1"), dedupTTL)
	if err != nil {
		return false, fmt.Errorf("dedup setnx: %w", err)
	}
	return ok, nil
}

package port

import (
	"context"
	"time"
)

// Cache abstracts Redis for ephemeral state and fast lookups.
type Cache interface {
	// Set stores a value with an optional TTL (0 = no expiry).
	Set(ctx context.Context, key string, value []byte, ttl time.Duration) error

	// Get retrieves a value. Returns (nil, nil) if the key does not exist.
	Get(ctx context.Context, key string) ([]byte, error)

	// Delete removes a key.
	Delete(ctx context.Context, key string) error

	// IncrBy atomically increments an integer counter and returns the new value.
	IncrBy(ctx context.Context, key string, delta int64, ttl time.Duration) (int64, error)

	// Keys returns all keys matching a glob pattern.
	Keys(ctx context.Context, pattern string) ([]string, error)

	// Exists returns true if the key exists.
	Exists(ctx context.Context, key string) (bool, error)
}

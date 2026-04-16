package redis

import (
	"context"
	"errors"
	"fmt"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

// Cache implements port.Cache backed by Redis via go-redis.
type Cache struct {
	client *goredis.Client
}

// New creates a Redis Cache from a connection URL (redis:// or rediss://).
func New(redisURL string) (*Cache, error) {
	opts, err := goredis.ParseURL(redisURL)
	if err != nil {
		return nil, fmt.Errorf("redis: parse URL: %w", err)
	}
	client := goredis.NewClient(opts)
	return &Cache{client: client}, nil
}

// Ping checks the connection. Call once at startup.
func (c *Cache) Ping(ctx context.Context) error {
	if err := c.client.Ping(ctx).Err(); err != nil {
		return fmt.Errorf("redis ping: %w", err)
	}
	return nil
}

// Close shuts down the Redis client.
func (c *Cache) Close() error {
	return c.client.Close()
}

// Set stores bytes with an optional TTL. ttl=0 means no expiry.
func (c *Cache) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	return c.client.Set(ctx, key, value, ttl).Err()
}

// Get retrieves bytes. Returns (nil, nil) when the key does not exist.
func (c *Cache) Get(ctx context.Context, key string) ([]byte, error) {
	b, err := c.client.Get(ctx, key).Bytes()
	if errors.Is(err, goredis.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("redis get %q: %w", key, err)
	}
	return b, nil
}

// Delete removes a key.
func (c *Cache) Delete(ctx context.Context, key string) error {
	return c.client.Del(ctx, key).Err()
}

// IncrBy atomically increments a counter and resets its TTL.
// Returns the new value.
func (c *Cache) IncrBy(ctx context.Context, key string, delta int64, ttl time.Duration) (int64, error) {
	pipe := c.client.Pipeline()
	incrCmd := pipe.IncrBy(ctx, key, delta)
	if ttl > 0 {
		pipe.Expire(ctx, key, ttl)
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return 0, fmt.Errorf("redis incrby %q: %w", key, err)
	}
	return incrCmd.Val(), nil
}

// Keys returns all keys matching a glob pattern.
func (c *Cache) Keys(ctx context.Context, pattern string) ([]string, error) {
	keys, err := c.client.Keys(ctx, pattern).Result()
	if err != nil {
		return nil, fmt.Errorf("redis keys %q: %w", pattern, err)
	}
	return keys, nil
}

// Exists returns true if the key exists.
func (c *Cache) Exists(ctx context.Context, key string) (bool, error) {
	n, err := c.client.Exists(ctx, key).Result()
	if err != nil {
		return false, fmt.Errorf("redis exists %q: %w", key, err)
	}
	return n > 0, nil
}

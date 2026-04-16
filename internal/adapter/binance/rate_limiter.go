package binance

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/azhar/cerebro/internal/domain"
	"github.com/azhar/cerebro/internal/port"
)

// Binance hard limits (PRD §6.1).
const (
	WeightLimitPerMin    = 6000
	OrderLimit10s        = 50
	OrderLimitDay        = 160_000
	AlertAtPct           = 0.80
	WeightWindowTTL      = 70 * time.Second
	Order10sWindowTTL    = 15 * time.Second
	OrderDayWindowTTL    = 24 * time.Hour
	AlertDedupTTL        = 60 * time.Second // one alert per minute maximum
)

// RateLimiter enforces Binance rate limits using Redis-backed counters.
// All REST calls must pass CheckAndRecord before dispatch.
type RateLimiter struct {
	cache    port.Cache
	notifier port.Notifier
	ipLabel  string // identifies the outbound IP (or instance) for key namespacing
}

// NewRateLimiter creates a RateLimiter.
// ipLabel is used to namespace Redis keys when multiple instances share one Redis.
func NewRateLimiter(cache port.Cache, notifier port.Notifier, ipLabel string) *RateLimiter {
	return &RateLimiter{cache: cache, notifier: notifier, ipLabel: ipLabel}
}

// CheckAndRecord validates and records the REQUEST_WEIGHT cost of a pending request.
// Returns ErrRateLimitWeight if the rolling 1-min budget is exhausted.
// Returns ErrIPBanned if a previous 418 was recorded.
func (r *RateLimiter) CheckAndRecord(ctx context.Context, weight int) error {
	// Check for active IP ban.
	banKey := fmt.Sprintf("binance_ban:%s", r.ipLabel)
	banned, _ := r.cache.Exists(ctx, banKey)
	if banned {
		return domain.ErrIPBanned
	}

	weightKey := fmt.Sprintf("weight:%s", r.ipLabel)
	newTotal, err := r.cache.IncrBy(ctx, weightKey, int64(weight), WeightWindowTTL)
	if err != nil {
		slog.Warn("rate limiter: failed to increment weight counter", "error", err)
		return nil // fail open to avoid blocking on Redis errors
	}

	// Alert at 80%.
	if float64(newTotal) >= float64(WeightLimitPerMin)*AlertAtPct {
		r.alertOnce(ctx, fmt.Sprintf(
			"[WARN] Binance request weight at %d/%d (%.0f%%); backing off",
			newTotal, WeightLimitPerMin, float64(newTotal)/float64(WeightLimitPerMin)*100,
		))
	}

	if newTotal > int64(WeightLimitPerMin) {
		return fmt.Errorf("%w: weight=%d/%d", domain.ErrRateLimitWeight, newTotal, WeightLimitPerMin)
	}
	return nil
}

// RecordOrder increments both the 10s and daily order counters.
func (r *RateLimiter) RecordOrder(ctx context.Context, accountID string) error {
	key10s := fmt.Sprintf("orders_10s:%s", accountID)
	keyDay := fmt.Sprintf("orders_day:%s", accountID)

	n10s, _ := r.cache.IncrBy(ctx, key10s, 1, Order10sWindowTTL)
	nDay, _ := r.cache.IncrBy(ctx, keyDay, 1, OrderDayWindowTTL)

	if n10s > OrderLimit10s {
		return fmt.Errorf("order rate limit (10s): %d/%d", n10s, OrderLimit10s)
	}
	if float64(nDay) >= float64(OrderLimitDay)*AlertAtPct {
		r.alertOnce(ctx, fmt.Sprintf("[WARN] Binance daily order count at %d/%d", nDay, OrderLimitDay))
	}
	if nDay > OrderLimitDay {
		return fmt.Errorf("order rate limit (daily): %d/%d", nDay, OrderLimitDay)
	}
	return nil
}

// HandleHTTPResponse inspects the response status for 429/418 and adjusts state.
func (r *RateLimiter) HandleHTTPResponse(ctx context.Context, resp *http.Response) error {
	if resp == nil {
		return nil
	}
	switch resp.StatusCode {
	case http.StatusTooManyRequests: // 429
		retryAfter := retryAfterSeconds(resp)
		slog.Warn("binance 429 received; backing off",
			"retry_after_seconds", retryAfter)
		r.alertOnce(ctx, fmt.Sprintf("[RATE LIMIT] Binance 429 — retry after %ds", retryAfter))
		return fmt.Errorf("HTTP 429: retry after %ds", retryAfter)

	case 418: // IP ban
		slog.Error("binance 418 received — IP banned",
			"retry_after", resp.Header.Get("Retry-After"))
		banDuration := time.Duration(retryAfterSeconds(resp)) * time.Second
		if banDuration == 0 {
			banDuration = 2 * time.Minute // minimum ban
		}
		banKey := fmt.Sprintf("binance_ban:%s", r.ipLabel)
		_ = r.cache.Set(ctx, banKey, []byte("banned"), banDuration)
		r.alertOnce(ctx, fmt.Sprintf(
			"[CRITICAL] Binance IP BAN (418) — all operations halted for %s", banDuration))
		return domain.ErrIPBanned
	}
	return nil
}

func (r *RateLimiter) alertOnce(ctx context.Context, msg string) {
	if r.notifier == nil {
		return
	}
	dedupKey := fmt.Sprintf("alert_dedup:%x", []byte(msg[:min(len(msg), 32)]))
	already, _ := r.cache.Exists(ctx, dedupKey)
	if already {
		return
	}
	_ = r.cache.Set(ctx, dedupKey, []byte("1"), AlertDedupTTL)
	go func() {
		_ = r.notifier.Send(ctx, port.ChannelSystemAlerts, msg)
	}()
}

func retryAfterSeconds(resp *http.Response) int {
	if resp == nil {
		return 0
	}
	s := resp.Header.Get("Retry-After")
	if s == "" {
		return 0
	}
	n, _ := strconv.Atoi(s)
	return n
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

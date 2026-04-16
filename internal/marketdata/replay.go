package marketdata

import (
	"context"
	"time"

	"github.com/azhar/cerebro/internal/domain"
)

// ReplayFeed provides a deterministic candle stream from historical data.
// Used by the backtest engine to drive the strategy pipeline without lookahead bias.
type ReplayFeed struct {
	candles  []domain.Candle
	hub      *Hub
	simClock *SimClock
}

// NewReplayFeed creates a feed from a pre-loaded candle slice.
func NewReplayFeed(candles []domain.Candle, hub *Hub, clock *SimClock) *ReplayFeed {
	return &ReplayFeed{candles: candles, hub: hub, simClock: clock}
}

// Run drives each candle into the hub in timestamp order, advancing the sim clock.
// Blocks until all candles are published or ctx is cancelled.
func (r *ReplayFeed) Run(ctx context.Context) error {
	for _, c := range r.candles {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		// Advance the simulated clock to the candle's close time.
		r.simClock.Set(c.CloseTime)
		r.hub.PublishCandle(c)
	}
	return nil
}

// SimClock is a simulated clock injected into all components during backtesting,
// replacing time.Now() to prevent lookahead bias.
type SimClock struct {
	current time.Time
}

// NewSimClock creates a SimClock starting at t.
func NewSimClock(t time.Time) *SimClock {
	return &SimClock{current: t}
}

// Now returns the current simulated time.
func (c *SimClock) Now() time.Time {
	return c.current
}

// Set advances the clock to t. Panics if t is before the current time (lookahead guard).
func (c *SimClock) Set(t time.Time) {
	if t.Before(c.current) {
		panic("SimClock: time went backwards — lookahead bias detected")
	}
	c.current = t
}

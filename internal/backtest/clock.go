package backtest

import "time"

// Clock is an injectable time source.
// Production code uses RealClock; backtest code uses SimClock.
type Clock interface {
	Now() time.Time
}

// RealClock returns the actual system time.
type RealClock struct{}

func (RealClock) Now() time.Time { return time.Now().UTC() }

// SimClock is a deterministic clock for backtesting.
// Advancing backwards panics to prevent lookahead bias.
type SimClock struct {
	current time.Time
}

// NewSimClock creates a SimClock at the given start time.
func NewSimClock(start time.Time) *SimClock {
	return &SimClock{current: start}
}

// Now returns the current simulated time.
func (c *SimClock) Now() time.Time { return c.current }

// Advance sets the clock forward by d. Panics if d is negative.
func (c *SimClock) Advance(d time.Duration) {
	if d < 0 {
		panic("SimClock.Advance: cannot go backwards")
	}
	c.current = c.current.Add(d)
}

// Set moves the clock to t. Panics if t is before current time.
func (c *SimClock) Set(t time.Time) {
	if t.Before(c.current) {
		panic("SimClock.Set: time went backwards — lookahead bias detected")
	}
	c.current = t
}

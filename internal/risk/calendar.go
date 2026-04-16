package risk

import (
	"sync"
	"time"

	"github.com/azhar/cerebro/internal/domain"
)

// CalendarBlackout tracks upcoming high-impact economic events and returns
// true during the blackout window around each event.
type CalendarBlackout struct {
	mu     sync.RWMutex
	events []domain.EconomicEvent
}

// NewCalendarBlackout creates an empty blackout tracker.
func NewCalendarBlackout() *CalendarBlackout {
	return &CalendarBlackout{}
}

// Update replaces the event list. Called by the calendar ingest scheduler.
func (c *CalendarBlackout) Update(events []domain.EconomicEvent) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = events
}

// IsBlackedOut returns true if now falls within beforeMin minutes before or
// afterMin minutes after any high-impact event.
func (c *CalendarBlackout) IsBlackedOut(now time.Time, beforeMin, afterMin int) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	before := time.Duration(beforeMin) * time.Minute
	after := time.Duration(afterMin) * time.Minute

	for _, evt := range c.events {
		if evt.Impact != "high" {
			continue
		}
		windowStart := evt.ScheduledAt.Add(-before)
		windowEnd := evt.ScheduledAt.Add(after)
		if now.After(windowStart) && now.Before(windowEnd) {
			return true
		}
	}
	return false
}

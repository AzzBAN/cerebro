package risk

import (
	"testing"
	"time"

	"github.com/azhar/cerebro/internal/domain"
)

func TestCalendarBlackout_IsBlackedOut(t *testing.T) {
	eventTime := time.Date(2026, 4, 18, 12, 0, 0, 0, time.UTC)

	cal := NewCalendarBlackout()
	cal.Update([]domain.EconomicEvent{
		{Title: "NFP", Impact: "high", Currency: "USD", ScheduledAt: eventTime},
		{Title: "PMI", Impact: "medium", Currency: "EUR", ScheduledAt: eventTime},
	})

	tests := []struct {
		name       string
		now        time.Time
		beforeMin  int
		afterMin   int
		wantBlack  bool
	}{
		{
			name:      "inside before window",
			now:       eventTime.Add(-15 * time.Minute),
			beforeMin: 30,
			afterMin:  15,
			wantBlack: true,
		},
		{
			name:      "inside after window",
			now:       eventTime.Add(10 * time.Minute),
			beforeMin: 30,
			afterMin:  15,
			wantBlack: true,
		},
		{
			name:      "exactly at event time",
			now:       eventTime,
			beforeMin: 30,
			afterMin:  15,
			wantBlack: true,
		},
		{
			name:      "outside before window",
			now:       eventTime.Add(-31 * time.Minute),
			beforeMin: 30,
			afterMin:  15,
			wantBlack: false,
		},
		{
			name:      "outside after window",
			now:       eventTime.Add(16 * time.Minute),
			beforeMin: 30,
			afterMin:  15,
			wantBlack: false,
		},
		{
			name:      "zero window means event itself is not blacked out (strictly before/after)",
			now:       eventTime,
			beforeMin: 0,
			afterMin:  0,
			wantBlack: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := cal.IsBlackedOut(tt.now, tt.beforeMin, tt.afterMin)
			if got != tt.wantBlack {
				t.Errorf("IsBlackedOut() = %v, want %v", got, tt.wantBlack)
			}
		})
	}
}

func TestCalendarBlackout_IgnoresLowImpact(t *testing.T) {
	eventTime := time.Date(2026, 4, 18, 12, 0, 0, 0, time.UTC)

	cal := NewCalendarBlackout()
	cal.Update([]domain.EconomicEvent{
		{Title: "Low impact", Impact: "low", ScheduledAt: eventTime},
		{Title: "Medium impact", Impact: "medium", ScheduledAt: eventTime},
	})

	got := cal.IsBlackedOut(eventTime, 30, 15)
	if got {
		t.Error("should not blackout for non-high-impact events")
	}
}

func TestCalendarBlackout_EmptyEvents(t *testing.T) {
	cal := NewCalendarBlackout()
	got := cal.IsBlackedOut(time.Now(), 30, 15)
	if got {
		t.Error("empty calendar should not be blacked out")
	}
}

func TestCalendarBlackout_UpdateReplaces(t *testing.T) {
	eventTime := time.Date(2026, 4, 18, 12, 0, 0, 0, time.UTC)

	cal := NewCalendarBlackout()
	cal.Update([]domain.EconomicEvent{
		{Title: "First", Impact: "high", ScheduledAt: eventTime},
	})

	if !cal.IsBlackedOut(eventTime, 30, 15) {
		t.Error("first event should cause blackout")
	}

	cal.Update(nil)
	if cal.IsBlackedOut(eventTime, 30, 15) {
		t.Error("update with nil should clear events")
	}
}

func TestCalendarBlackout_MultipleEvents(t *testing.T) {
	t1 := time.Date(2026, 4, 18, 8, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 4, 18, 14, 0, 0, 0, time.UTC)

	cal := NewCalendarBlackout()
	cal.Update([]domain.EconomicEvent{
		{Title: "Event A", Impact: "high", ScheduledAt: t1},
		{Title: "Event B", Impact: "high", ScheduledAt: t2},
	})

	if !cal.IsBlackedOut(t1, 30, 15) {
		t.Error("should blackout for event A")
	}
	if !cal.IsBlackedOut(t2, 30, 15) {
		t.Error("should blackout for event B")
	}
	// Between events, outside both windows.
	between := time.Date(2026, 4, 18, 11, 0, 0, 0, time.UTC)
	if cal.IsBlackedOut(between, 30, 15) {
		t.Error("should not blackout between events outside windows")
	}
}

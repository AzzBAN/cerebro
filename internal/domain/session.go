package domain

import "time"

// SessionWindow is a UTC time range used to gate strategy entries.
// No Forex-specific names — these are generic high-volatility UTC windows.
type SessionWindow struct {
	Name      SessionFilter
	StartHour int // UTC hour (0–23)
	StartMin  int
	EndHour   int
	EndMin    int
}

// predefined session windows
var (
	SessionWindowNYOpen    = SessionWindow{SessionNYOpen, 12, 0, 14, 0}
	SessionWindowAsianOpen = SessionWindow{SessionAsianOpen, 0, 0, 2, 0}
	SessionWindowOverlap   = SessionWindow{SessionOverlap, 12, 0, 16, 0}
)

// Contains returns true if the given UTC time falls within this window.
func (s SessionWindow) Contains(t time.Time) bool {
	utc := t.UTC()
	h, m := utc.Hour(), utc.Minute()
	startMins := s.StartHour*60 + s.StartMin
	endMins := s.EndHour*60 + s.EndMin
	nowMins := h*60 + m
	return nowMins >= startMins && nowMins < endMins
}

// SessionWindowFor returns the SessionWindow for a given filter.
// Returns nil for SessionAll.
func SessionWindowFor(f SessionFilter) *SessionWindow {
	switch f {
	case SessionNYOpen:
		w := SessionWindowNYOpen
		return &w
	case SessionAsianOpen:
		w := SessionWindowAsianOpen
		return &w
	case SessionOverlap:
		w := SessionWindowOverlap
		return &w
	default:
		return nil
	}
}

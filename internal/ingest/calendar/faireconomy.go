package calendar

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/azhar/cerebro/internal/domain"
)

// faireconomyURL is a var (not const) so tests can point the client at an
// httptest server. Production code never mutates it.
//
// The FairEconomy feed (the same data ForexFactory publishes) is a free,
// keyless weekly economic calendar in JSON form. It is the default calendar
// source because Finnhub's /calendar/economic endpoint is premium-only and
// returns HTTP 403 on the free tier.
var faireconomyURL = "https://nfs.faireconomy.media/ff_calendar_thisweek.json"

// FairEconomyCalendar implements port.CalendarFeed using the free FairEconomy
// weekly economic calendar feed. No API key is required.
type FairEconomyCalendar struct {
	client *http.Client
}

// NewFairEconomy creates a FairEconomyCalendar with a default timeout.
func NewFairEconomy() *FairEconomyCalendar {
	return &FairEconomyCalendar{
		client: &http.Client{Timeout: 15 * time.Second},
	}
}

// NewFairEconomyWithTimeout creates a FairEconomyCalendar with a custom timeout.
func NewFairEconomyWithTimeout(timeout time.Duration) *FairEconomyCalendar {
	return &FairEconomyCalendar{
		client: &http.Client{Timeout: timeout},
	}
}

// faireconomyEvent mirrors one entry of the weekly JSON feed. Example:
//
//	{"title":"Core CPI m/m","country":"USD",
//	 "date":"2026-06-12T08:30:00-04:00","impact":"High",
//	 "forecast":"0.3%","previous":"0.2%"}
type faireconomyEvent struct {
	Title   string `json:"title"`
	Country string `json:"country"`
	Date    string `json:"date"`
	Impact  string `json:"impact"`
}

// UpcomingEvents returns economic events scheduled within the next hours hours.
func (f *FairEconomyCalendar) UpcomingEvents(ctx context.Context, hours int) ([]domain.EconomicEvent, error) {
	now := time.Now().UTC()
	to := now.Add(time.Duration(hours) * time.Hour)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, faireconomyURL, nil)
	if err != nil {
		return nil, fmt.Errorf("faireconomy calendar: build request: %w", err)
	}
	// A browser-like User-Agent avoids the upstream CDN returning 403 to
	// default Go transport agents.
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/137.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "application/json, text/plain, */*")

	resp, err := f.client.Do(req)
	if err != nil {
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			return nil, fmt.Errorf("faireconomy calendar: http: %w", context.DeadlineExceeded)
		case errors.Is(err, context.Canceled):
			return nil, fmt.Errorf("faireconomy calendar: http: %w", context.Canceled)
		default:
			return nil, fmt.Errorf("faireconomy calendar: http: %w", err)
		}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("faireconomy calendar: status %d", resp.StatusCode)
	}

	var raw []faireconomyEvent
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("faireconomy calendar: decode: %w", err)
	}

	var events []domain.EconomicEvent
	for _, e := range raw {
		if strings.TrimSpace(e.Title) == "" {
			continue
		}
		// The feed dates carry a numeric timezone offset (e.g. -04:00),
		// so RFC3339 parses them directly. Normalise to UTC for window
		// comparison.
		t, err := time.Parse(time.RFC3339, e.Date)
		if err != nil {
			continue
		}
		t = t.UTC()
		if t.Before(now) || t.After(to) {
			continue
		}
		events = append(events, domain.EconomicEvent{
			Title:       e.Title,
			Impact:      normalizeImpact(e.Impact),
			Currency:    strings.TrimSpace(e.Country),
			ScheduledAt: t,
		})
	}

	slog.DebugContext(ctx, "faireconomy calendar: parsed events", "total", len(raw), "upcoming", len(events))
	return events, nil
}

// normalizeImpact maps the feed's impact labels (High/Medium/Low/Holiday) to
// the domain's low|medium|high vocabulary. Unknown or non-impactful labels
// (e.g. "Holiday", "") fall back to "low".
func normalizeImpact(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "high":
		return "high"
	case "medium":
		return "medium"
	case "low":
		return "low"
	default:
		return "low"
	}
}

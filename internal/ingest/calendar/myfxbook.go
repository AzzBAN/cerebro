package calendar

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/azhar/cerebro/internal/domain"
)

const finnhubBaseURL = "https://finnhub.io/api/v1/calendar/economic"

// FinnhubCalendar implements port.CalendarFeed using the Finnhub economic calendar API.
// Free tier: 60 calls/minute. Requires FINNHUB_API_KEY.
type FinnhubCalendar struct {
	apiKey string
	client *http.Client
}

// New creates a FinnhubCalendar.
func New(apiKey string) *FinnhubCalendar {
	return &FinnhubCalendar{
		apiKey: apiKey,
		client: &http.Client{Timeout: 15 * time.Second},
	}
}

type finnhubResponse struct {
	EconomicCalendar []finnhubEvent `json:"economicCalendar"`
}

type finnhubEvent struct {
	Country string `json:"country"`
	Event   string `json:"event"`
	Impact  string `json:"impact"`
	Time    string `json:"time"`
}

// UpcomingEvents returns economic events within the next hours hours.
func (f *FinnhubCalendar) UpcomingEvents(ctx context.Context, hours int) ([]domain.EconomicEvent, error) {
	now := time.Now().UTC()
	to := now.Add(time.Duration(hours) * time.Hour)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, finnhubBaseURL, nil)
	if err != nil {
		return nil, fmt.Errorf("finnhub calendar: build request: %w", err)
	}
	q := req.URL.Query()
	q.Set("token", f.apiKey)
	q.Set("from", now.Format("2006-01-02"))
	q.Set("to", to.Format("2006-01-02"))
	req.URL.RawQuery = q.Encode()

	resp, err := f.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("finnhub calendar: http: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("finnhub calendar: status %d", resp.StatusCode)
	}

	var apiResp finnhubResponse
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return nil, fmt.Errorf("finnhub calendar: decode: %w", err)
	}

	var events []domain.EconomicEvent
	for _, e := range apiResp.EconomicCalendar {
		t, err := time.Parse("2006-01-02T15:04:05", e.Time)
		if err != nil {
			// Try UTC suffix.
			t, err = time.Parse("2006-01-02T15:04:05Z", e.Time)
			if err != nil {
				continue
			}
		}
		if t.Before(now) || t.After(to) {
			continue
		}
		impact := e.Impact
		if impact == "" {
			impact = classifyImpact(e.Event)
		}
		events = append(events, domain.EconomicEvent{
			Title:       e.Event,
			Impact:      impact,
			ScheduledAt: t,
		})
	}

	slog.Debug("finnhub calendar: parsed events", "total", len(apiResp.EconomicCalendar), "upcoming", len(events))
	return events, nil
}

func classifyImpact(title string) string {
	highImpact := []string{"NFP", "CPI", "FOMC", "Fed", "GDP", "PMI", "ECB", "BOE", "BOJ", "Non Farm", "Interest Rate", "Unemployment"}
	lower := strings.ToLower(title)
	for _, keyword := range highImpact {
		if strings.Contains(strings.ToLower(title), strings.ToLower(keyword)) {
			return "high"
		}
	}
	_ = lower
	return "medium"
}

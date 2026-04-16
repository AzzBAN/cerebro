package calendar

import (
	"context"
	"encoding/xml"
	"fmt"
	"net/http"
	"time"

	"github.com/azhar/cerebro/internal/domain"
)

const myfxbookRSSURL = "https://www.myfxbook.com/rss/forex-economic-calendar-rss.xml"

// MyfxbookCalendar implements port.CalendarFeed using the Myfxbook RSS feed.
type MyfxbookCalendar struct {
	client *http.Client
}

// New creates a MyfxbookCalendar.
func New() *MyfxbookCalendar {
	return &MyfxbookCalendar{
		client: &http.Client{Timeout: 15 * time.Second},
	}
}

type rssItem struct {
	Title   string `xml:"title"`
	PubDate string `xml:"pubDate"`
}

type rssFeed struct {
	Items []rssItem `xml:"channel>item"`
}

// UpcomingEvents returns high-impact economic events within the next hours hours.
func (m *MyfxbookCalendar) UpcomingEvents(ctx context.Context, hours int) ([]domain.EconomicEvent, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, myfxbookRSSURL, nil)
	if err != nil {
		return nil, fmt.Errorf("myfxbook: build request: %w", err)
	}

	resp, err := m.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("myfxbook: http: %w", err)
	}
	defer resp.Body.Close()

	var feed rssFeed
	if err := xml.NewDecoder(resp.Body).Decode(&feed); err != nil {
		return nil, fmt.Errorf("myfxbook: parse RSS: %w", err)
	}

	now := time.Now().UTC()
	window := now.Add(time.Duration(hours) * time.Hour)
	var events []domain.EconomicEvent

	for _, item := range feed.Items {
		t, err := time.Parse(time.RFC1123, item.PubDate)
		if err != nil {
			t, err = time.Parse(time.RFC1123Z, item.PubDate)
			if err != nil {
				continue
			}
		}
		if t.After(now) && t.Before(window) {
			impact := classifyImpact(item.Title)
			events = append(events, domain.EconomicEvent{
				Title:       item.Title,
				Impact:      impact,
				ScheduledAt: t,
			})
		}
	}
	return events, nil
}

// classifyImpact assigns a rough impact level based on common high-impact event names.
func classifyImpact(title string) string {
	highImpact := []string{"NFP", "CPI", "FOMC", "Fed", "GDP", "PMI", "ECB", "BOE", "BOJ"}
	for _, keyword := range highImpact {
		if contains(title, keyword) {
			return "high"
		}
	}
	return "medium"
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr ||
		len(s) > 0 && containsLoop(s, substr))
}

func containsLoop(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

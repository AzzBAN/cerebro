package port

import (
	"context"

	"github.com/azhar/cerebro/internal/domain"
)

// NewsItem is a single headline or market squawk.
type NewsItem struct {
	Title     string
	Summary   string
	Source    string
	URL       string
	Sentiment string // bullish | bearish | neutral (provider-provided)
	PublishedAt interface{} // time.Time
}

// NewsFeed fetches recent headlines for a given asset.
type NewsFeed interface {
	// FetchLatest returns up to limit recent headlines for the given asset keyword.
	FetchLatest(ctx context.Context, asset string, limit int) ([]NewsItem, error)
}

// CalendarFeed fetches upcoming economic events.
type CalendarFeed interface {
	// UpcomingEvents returns high-impact events within the given time window.
	UpcomingEvents(ctx context.Context, hours int) ([]domain.EconomicEvent, error)
}

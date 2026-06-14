package port

import (
	"context"
	"time"

	"github.com/azhar/cerebro/internal/domain"
)

// NewsVotes holds the provider-reported reaction counts for a post. Fields
// map 1:1 to CryptoPanic's vote schema; other news sources populate only the
// subset they expose (unused fields remain zero).
type NewsVotes struct {
	Positive  int
	Negative  int
	Important int
	Liked     int
	Disliked  int
	LOL       int
	Toxic     int
	Saved     int
	Comments  int
}

// NewsItem is a single headline or market squawk.
type NewsItem struct {
	// ID is the provider-stable identifier used for deduplication. Required
	// for CryptoPanic; sources without a stable ID may synthesise one from
	// URL + title (e.g. FinancialJuice uses <guid> / hashed title).
	ID string
	// Title is the headline text.
	Title string
	// Summary is the short body/description where available.
	Summary string
	// Source is the ingest adapter name ("cryptopanic", "financialjuice", ...).
	Source string
	// Domain is the upstream publisher domain (e.g. "coindesk.com").
	Domain string
	// URL is the canonical link to the article.
	URL string
	// Kind distinguishes "news" from "media" (video/podcast) where the
	// provider exposes it. Empty for sources without the distinction.
	Kind string
	// Currencies lists the asset tickers the item references (e.g. ["BTC","ETH"]).
	Currencies []string
	// Sentiment is a coarse label: "bullish" | "bearish" | "neutral". For
	// providers that expose votes, derive from Votes; for scraped RSS we
	// fall back to a keyword classifier.
	Sentiment string
	// Votes holds raw reaction counts. Zero-valued when unavailable.
	Votes NewsVotes
	// PublishedAt is when the upstream publisher posted the item.
	PublishedAt time.Time
}

// NewsFeed fetches recent headlines for a given asset.
type NewsFeed interface {
	// FetchLatest returns up to limit recent headlines for the given asset keyword.
	// When asset is empty, the global feed is returned.
	FetchLatest(ctx context.Context, asset string, limit int) ([]NewsItem, error)
}

// CalendarFeed fetches upcoming economic events.
type CalendarFeed interface {
	// UpcomingEvents returns high-impact events within the given time window.
	UpcomingEvents(ctx context.Context, hours int) ([]domain.EconomicEvent, error)
}

package cryptopanic

import (
	"context"
	"strings"
	"time"

	"github.com/azhar/cerebro/internal/port"
)

// Feed adapts the reverse-engineered Client to port.NewsFeed.
type Feed struct {
	client *Client
	// defaultFilter is applied when the caller does not override via asset.
	defaultFilter string
}

// NewFeed returns a NewsFeed backed by the pure-Go RE client. filter is
// one of hot|rising|bullish|bearish|important|saved|lol; empty defaults to "hot".
func NewFeed(client *Client, filter string) *Feed {
	if filter == "" {
		filter = "hot"
	}
	return &Feed{client: client, defaultFilter: filter}
}

// FetchLatest returns up to limit recent headlines. When asset is empty
// or not a valid ticker the global feed is returned; otherwise the
// ticker is sent as a currencies filter. Non-ticker input (natural
// language queries from the agent) is silently treated as empty —
// passing garbage to the /web-api/posts/ currencies filter returns no
// matches and wastes the scrape.
func (f *Feed) FetchLatest(ctx context.Context, asset string, limit int) ([]port.NewsItem, error) {
	q := Query{Filter: f.defaultFilter, Kind: "news", Public: true}
	if ticker := normalizeTicker(asset); ticker != "" {
		q.Currencies = []string{ticker}
	}

	posts, err := f.client.FetchPosts(ctx, q)
	if err != nil {
		return nil, err
	}

	if limit <= 0 || limit > len(posts) {
		limit = len(posts)
	}

	out := make([]port.NewsItem, 0, limit)
	for i := 0; i < limit; i++ {
		p := posts[i]
		if p.Kind == "sponsored" {
			// Skip paid placements — they are not news.
			limit = min(limit+1, len(posts))
			continue
		}
		out = append(out, toNewsItem(p))
	}
	return out, nil
}

// toNewsItem projects a rawPost into the shared port.NewsItem shape.
func toNewsItem(p rawPost) port.NewsItem {
	pubAt, _ := time.Parse(time.RFC3339, p.PublishedAt)
	if pubAt.IsZero() {
		pubAt, _ = time.Parse(time.RFC3339, p.CreatedAt)
	}

	ni := port.NewsItem{
		ID:          formatID(p.PK),
		Title:       p.Title,
		Summary:     p.Body,
		Source:      "cryptopanic",
		Domain:      p.Source.Domain,
		URL:         p.URL,
		Kind:        p.Kind,
		Currencies:  p.CurrenciesCodes,
		Sentiment:   classify(p),
		PublishedAt: pubAt,
		Votes: port.NewsVotes{
			Positive:  p.Votes.Positive,
			Negative:  p.Votes.Negative,
			Important: p.Votes.Important,
			Liked:     p.Votes.Like,
			Disliked:  p.Votes.Dislike,
			LOL:       p.Votes.LOL,
			Toxic:     p.Votes.Toxic,
			Saved:     p.Votes.Saved,
			Comments:  p.Votes.Comments,
		},
	}
	if ni.Domain == "" {
		ni.Domain = p.Domain
	}
	return ni
}

// classify prefers the provider's AI sentiment score (if present) and
// falls back to the vote-ratio heuristic the v1 API already exposes.
// AISentimentLevel: -1/-2 bearish, 0 neutral, 1/2 bullish (observed range).
func classify(p rawPost) string {
	if p.AISentimentLevel != nil {
		switch {
		case *p.AISentimentLevel > 0:
			return "bullish"
		case *p.AISentimentLevel < 0:
			return "bearish"
		default:
			return "neutral"
		}
	}
	pos := p.Votes.Positive
	neg := p.Votes.Negative
	switch {
	case pos > 2*neg && pos > 0:
		return "bullish"
	case neg > 2*pos && neg > 0:
		return "bearish"
	default:
		return "neutral"
	}
}

func formatID(pk int64) string {
	if pk == 0 {
		return ""
	}
	// Prefix the source so downstream dedup keys are collision-free across
	// multiple news adapters.
	return "cp:" + intToString(pk)
}

func intToString(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// normalizeTicker validates and uppercases a ticker symbol. Returns empty
// when the input is not a plausible CryptoPanic currency code: we accept
// 1-10 alphanumeric chars (CP's longest listed codes are ~7 chars; we
// allow a little slack for future tokens). Anything else (natural
// language, empty string, whitespace) returns empty which signals
// "global feed" to the caller.
func normalizeTicker(s string) string {
	t := strings.ToUpper(strings.TrimSpace(s))
	if t == "" || len(t) > 10 {
		return ""
	}
	for _, r := range t {
		if !((r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')) {
			return ""
		}
	}
	return t
}

// Ensure Feed implements port.NewsFeed (compile-time check).
var _ port.NewsFeed = (*Feed)(nil)

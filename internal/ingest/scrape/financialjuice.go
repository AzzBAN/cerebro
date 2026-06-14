package scrape

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/azhar/cerebro/internal/port"
)

const financialJuiceRSS = "https://www.financialjuice.com/feed.ashx?xy=rss"

// Default cooldown applied to the FinancialJuice scraper on HTTP 429 /
// 403 responses when the server does not send a `Retry-After` header.
// The value is deliberately longer than the combined news runner's tick
// (~5m) so repeated ticks during a rate-limit window are suppressed at
// the scraper level instead of hammering the endpoint.
const defaultFinancialJuiceCooldown = 15 * time.Minute

// FinancialJuiceScraper fetches market squawks from FinancialJuice via RSS.
//
// The scraper is safe for concurrent use and maintains an internal
// cooldown window: when Cloudflare responds with 429 or 403, subsequent
// calls short-circuit and return (nil, nil) until the cooldown expires.
// This prevents noisy warn-level logs and avoids provoking the upstream
// block further when the combined news runner ticks faster than the RSS
// endpoint tolerates.
type FinancialJuiceScraper struct {
	client          *http.Client
	defaultCooldown time.Duration

	mu            sync.Mutex
	cooldownUntil time.Time
	lastBlockCode int
}

// NewFinancialJuice creates a FinancialJuiceScraper with a default timeout.
func NewFinancialJuice() *FinancialJuiceScraper {
	return &FinancialJuiceScraper{
		client:          &http.Client{Timeout: 30 * time.Second},
		defaultCooldown: defaultFinancialJuiceCooldown,
	}
}

// NewFinancialJuiceWithTimeout creates a FinancialJuiceScraper with a custom timeout.
func NewFinancialJuiceWithTimeout(timeout time.Duration) *FinancialJuiceScraper {
	return &FinancialJuiceScraper{
		client:          &http.Client{Timeout: timeout},
		defaultCooldown: defaultFinancialJuiceCooldown,
	}
}

// FetchLatest returns recent market squawks from FinancialJuice RSS feed.
// When the scraper is inside a cooldown window from a prior 429/403 it
// returns (nil, nil) immediately so the caller treats the tick as a
// no-op without logging an error.
func (s *FinancialJuiceScraper) FetchLatest(ctx context.Context, asset string, limit int) ([]port.NewsItem, error) {
	if remain, ok := s.cooldownRemaining(time.Now()); ok {
		slog.Debug("financialjuice: in cooldown, skipping fetch",
			"remaining", remain.Truncate(time.Second),
			"last_status", s.lastBlockStatus())
		return nil, nil
	}

	body, err := s.fetchRSS(ctx)
	if err != nil {
		return nil, err
	}
	if body == nil {
		// Rate-limited or blocked — cooldown was applied in fetchRSS.
		// Return an empty result without error so the combined refresher
		// just records "financialjuice=0" for this tick.
		return nil, nil
	}
	defer body.Close()

	rss, err := decodeRSS(body)
	if err != nil {
		return nil, fmt.Errorf("financialjuice: parse rss: %w", err)
	}

	items := rss.Channel.Items
	if len(items) == 0 {
		slog.Debug("financialjuice: scraped squawks", "count", 0)
		return nil, nil
	}

	if limit > 0 && len(items) > limit {
		items = items[:limit]
	}

	result := make([]port.NewsItem, 0, len(items))
	for _, item := range items {
		title := strings.TrimPrefix(item.Title, "FinancialJuice: ")
		if title == "" {
			continue
		}
		pubAt, _ := time.Parse(time.RFC1123, item.PubDate)
		id := item.GUID
		if id == "" {
			id = item.Link
		}
		ni := port.NewsItem{
			ID:          id,
			Title:       title,
			Summary:     item.Description,
			Source:      "financialjuice",
			Domain:      "financialjuice.com",
			URL:         item.Link,
			Sentiment:   classifySentiment(title),
			PublishedAt: pubAt,
		}
		result = append(result, ni)
	}

	slog.Debug("financialjuice: scraped squawks", "count", len(result))
	return result, nil
}

func (s *FinancialJuiceScraper) fetchRSS(ctx context.Context) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, financialJuiceRSS, nil)
	if err != nil {
		return nil, fmt.Errorf("financialjuice: build request: %w", err)
	}

	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/137.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "application/rss+xml, application/xml, text/xml, */*")

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("financialjuice: http: %w", err)
	}

	switch resp.StatusCode {
	case http.StatusOK:
		return resp.Body, nil
	case http.StatusTooManyRequests, http.StatusForbidden:
		retryAfter := parseRetryAfter(resp.Header.Get("Retry-After"), time.Now())
		resp.Body.Close()
		s.applyCooldown(retryAfter, resp.StatusCode)
		slog.Info("financialjuice: rate-limited, entering cooldown",
			"status", resp.StatusCode,
			"retry_after", retryAfter.Truncate(time.Second).String())
		return nil, nil
	default:
		resp.Body.Close()
		return nil, fmt.Errorf("financialjuice: unexpected status %d", resp.StatusCode)
	}
}

// cooldownRemaining returns (remaining, true) when a cooldown is active
// as of now. The read is guarded because FetchLatest and applyCooldown
// may race across concurrent tick goroutines.
func (s *FinancialJuiceScraper) cooldownRemaining(now time.Time) (time.Duration, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cooldownUntil.IsZero() || !now.Before(s.cooldownUntil) {
		return 0, false
	}
	return s.cooldownUntil.Sub(now), true
}

// applyCooldown schedules the next permitted fetch at now+d. If d is
// non-positive (no/invalid Retry-After header), defaultCooldown is used.
func (s *FinancialJuiceScraper) applyCooldown(d time.Duration, status int) {
	if d <= 0 {
		d = s.defaultCooldown
	}
	s.mu.Lock()
	s.cooldownUntil = time.Now().Add(d)
	s.lastBlockCode = status
	s.mu.Unlock()
}

func (s *FinancialJuiceScraper) lastBlockStatus() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastBlockCode
}

// parseRetryAfter decodes the RFC 7231 Retry-After header, which is either
// a non-negative integer number of seconds or an HTTP-date. Unparseable
// values yield a zero duration so the caller falls back to the default
// cooldown.
func parseRetryAfter(header string, now time.Time) time.Duration {
	header = strings.TrimSpace(header)
	if header == "" {
		return 0
	}
	if secs, err := strconv.Atoi(header); err == nil && secs >= 0 {
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(header); err == nil {
		if d := t.Sub(now); d > 0 {
			return d
		}
	}
	return 0
}

// RSS types for XML decoding.

type rssFeed struct {
	XMLName xml.Name `xml:"rss"`
	Channel rssChannel `xml:"channel"`
}

type rssChannel struct {
	Items []rssItem `xml:"item"`
}

type rssItem struct {
	Title       string `xml:"title"`
	Link        string `xml:"link"`
	Description string `xml:"description"`
	PubDate     string `xml:"pubDate"`
	GUID        string `xml:"guid"`
}

func decodeRSS(r io.Reader) (*rssFeed, error) {
	var feed rssFeed
	if err := xml.NewDecoder(r).Decode(&feed); err != nil {
		return nil, fmt.Errorf("decode xml: %w", err)
	}
	return &feed, nil
}

// classifySentiment returns a basic sentiment label based on keywords.
func classifySentiment(text string) string {
	lower := strings.ToLower(text)
	if len(lower) > 400 {
		lower = lower[:400]
	}

	bearishWords := []string{"crash", "plunge", "drop", "fall", "decline", "loss", "bearish", "sell-off", "slump", "fear", "attack", "strike", "sanctions", "ban", "cut", "miss", "below"}
	bullishWords := []string{"surge", "rally", "gain", "rise", "climb", "bullish", "breakout", "soar", "jump", "optimism", "beat", "above", "upgrade", "deal", "approve"}

	bearScore := 0
	bullScore := 0
	for _, w := range bearishWords {
		if strings.Contains(lower, w) {
			bearScore++
		}
	}
	for _, w := range bullishWords {
		if strings.Contains(lower, w) {
			bullScore++
		}
	}

	switch {
	case bullScore > bearScore:
		return "bullish"
	case bearScore > bullScore:
		return "bearish"
	default:
		return "neutral"
	}
}

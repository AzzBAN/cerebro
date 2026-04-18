package scrape

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/azhar/cerebro/internal/port"
)

const financialJuiceRSS = "https://www.financialjuice.com/feed.ashx?xy=rss"

// FinancialJuiceScraper fetches market squawks from FinancialJuice via RSS.
type FinancialJuiceScraper struct {
	client *http.Client
}

// NewFinancialJuice creates a FinancialJuiceScraper with a default timeout.
func NewFinancialJuice() *FinancialJuiceScraper {
	return &FinancialJuiceScraper{
		client: &http.Client{Timeout: 30 * time.Second},
	}
}

// NewFinancialJuiceWithTimeout creates a FinancialJuiceScraper with a custom timeout.
func NewFinancialJuiceWithTimeout(timeout time.Duration) *FinancialJuiceScraper {
	return &FinancialJuiceScraper{
		client: &http.Client{Timeout: timeout},
	}
}

// FetchLatest returns recent market squawks from FinancialJuice RSS feed.
func (s *FinancialJuiceScraper) FetchLatest(ctx context.Context, asset string, limit int) ([]port.NewsItem, error) {
	body, err := s.fetchRSS(ctx)
	if err != nil {
		return nil, err
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
		ni := port.NewsItem{
			Title:       title,
			Summary:     item.Description,
			Source:      "FinancialJuice",
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

	if resp.StatusCode == http.StatusForbidden {
		resp.Body.Close()
		slog.Warn("financialjuice: blocked by Cloudflare (HTTP 403)")
		return nil, nil
	}

	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("financialjuice: unexpected status %d", resp.StatusCode)
	}

	return resp.Body, nil
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

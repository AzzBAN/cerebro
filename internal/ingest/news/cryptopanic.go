package news

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/azhar/cerebro/internal/port"
)

const cryptoPanicBase = "https://cryptopanic.com/api/v1/posts/"

// CryptoPanicFeed implements port.NewsFeed using the CryptoPanic API.
type CryptoPanicFeed struct {
	apiKey string
	client *http.Client
}

// NewCryptoPanic creates a CryptoPanic news feed.
func NewCryptoPanic(apiKey string) *CryptoPanicFeed {
	return &CryptoPanicFeed{
		apiKey: apiKey,
		client: &http.Client{Timeout: 10 * time.Second},
	}
}

type cryptoPanicResponse struct {
	Results []struct {
		Title       string `json:"title"`
		URL         string `json:"url"`
		PublishedAt string `json:"published_at"`
		Votes       struct {
			Positive int `json:"positive"`
			Negative int `json:"negative"`
		} `json:"votes"`
	} `json:"results"`
}

// FetchLatest returns up to limit headlines for the given currency symbol.
func (f *CryptoPanicFeed) FetchLatest(ctx context.Context, asset string, limit int) ([]port.NewsItem, error) {
	params := url.Values{
		"auth_token": {f.apiKey},
		"currencies": {asset},
		"kind":       {"news"},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		cryptoPanicBase+"?"+params.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("cryptopanic: build request: %w", err)
	}

	resp, err := f.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cryptopanic: http: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("cryptopanic: status %d", resp.StatusCode)
	}

	var apiResp cryptoPanicResponse
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return nil, fmt.Errorf("cryptopanic: decode: %w", err)
	}

	items := make([]port.NewsItem, 0, min(limit, len(apiResp.Results)))
	for i, r := range apiResp.Results {
		if i >= limit {
			break
		}
		sentiment := "neutral"
		if r.Votes.Positive > r.Votes.Negative*2 {
			sentiment = "bullish"
		} else if r.Votes.Negative > r.Votes.Positive*2 {
			sentiment = "bearish"
		}
		items = append(items, port.NewsItem{
			Title:     r.Title,
			Source:    "cryptopanic",
			URL:       r.URL,
			Sentiment: sentiment,
		})
	}
	return items, nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

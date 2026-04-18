package coinglass

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const baseURL = "https://open-api-v4.coinglass.com"

// Client is the CoinGlass v4 HTTP client.
// All requests carry the CG-API-KEY header.
// Retry policy: 3 attempts with exponential backoff (100ms, 200ms, 400ms).
type Client struct {
	apiKey  string
	http    *http.Client
}

// New creates a CoinGlass client.
func New(apiKey string) *Client {
	return &Client{
		apiKey: apiKey,
		http:   &http.Client{Timeout: 10 * time.Second},
	}
}

// get performs a GET request to the given path and decodes the JSON body into v.
func (c *Client) get(ctx context.Context, path string, params map[string]string, v any) error {
	url := baseURL + path
	if len(params) > 0 {
		url += "?"
		first := true
		for k, val := range params {
			if !first {
				url += "&"
			}
			url += k + "=" + val
			first = false
		}
	}

	var lastErr error
	delay := 100 * time.Millisecond

	for attempt := 0; attempt < 3; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return fmt.Errorf("coinglass: build request: %w", err)
		}
		req.Header.Set("CG-API-KEY", c.apiKey)
		req.Header.Set("Accept", "application/json")

		resp, err := c.http.Do(req)
		if err != nil {
			lastErr = err
			time.Sleep(delay)
			delay *= 2
			continue
		}

		if resp.StatusCode == http.StatusTooManyRequests {
			resp.Body.Close()
			lastErr = fmt.Errorf("coinglass: rate limited (429)")
			time.Sleep(60 * time.Second)
			continue
		}
		if resp.StatusCode >= 500 {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			lastErr = fmt.Errorf("coinglass: server error %d: %s", resp.StatusCode, string(body))
			time.Sleep(delay)
			delay *= 2
			continue
		}
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			return fmt.Errorf("coinglass: unexpected status %d: %s", resp.StatusCode, string(body))
		}

		err = json.NewDecoder(resp.Body).Decode(v)
		resp.Body.Close()
		if err != nil {
			return fmt.Errorf("coinglass: decode %s: %w", path, err)
		}
		return nil
	}
	return fmt.Errorf("coinglass: %s after 3 attempts: %w", path, lastErr)
}

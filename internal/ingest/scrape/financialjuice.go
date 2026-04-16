package scrape

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/azhar/cerebro/internal/port"
)

const financialJuiceURL = "https://www.financialjuice.com/home"

// FinancialJuiceScraper scrapes market squawks from FinancialJuice.
// Uses a standard HTTP client first; chromedp headless browser is the fallback
// for JavaScript-rendered content (production hardening in Phase 9).
type FinancialJuiceScraper struct {
	client *http.Client
}

// NewFinancialJuice creates a FinancialJuiceScraper.
func NewFinancialJuice() *FinancialJuiceScraper {
	return &FinancialJuiceScraper{
		client: &http.Client{Timeout: 30 * time.Second},
	}
}

// FetchLatest returns recent market squawks from FinancialJuice.
// This is a best-effort scraper; errors are logged but not fatal.
func (s *FinancialJuiceScraper) FetchLatest(ctx context.Context, asset string, limit int) ([]port.NewsItem, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, financialJuiceURL, nil)
	if err != nil {
		return nil, fmt.Errorf("financialjuice: build request: %w", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; CerebroBot/1.0)")

	resp, err := s.client.Do(req)
	if err != nil {
		slog.Warn("financialjuice: HTTP request failed; JavaScript rendering may be required",
			"error", err)
		return nil, fmt.Errorf("financialjuice: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("financialjuice: status %d", resp.StatusCode)
	}

	// Full HTML parsing + squawk extraction is Phase 9 hardening via chromedp.
	// Return a placeholder for Phase 5 wiring.
	slog.Debug("financialjuice: page fetched; full squawk extraction requires chromedp (Phase 9)")
	return []port.NewsItem{}, nil
}

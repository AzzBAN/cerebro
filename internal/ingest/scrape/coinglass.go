package scrape

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/azhar/cerebro/internal/domain"
	"github.com/azhar/cerebro/internal/port"
	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
	"github.com/shopspring/decimal"
	"golang.org/x/sync/errgroup"
)

const stealthUserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"

const coinglassBaseURL = "https://www.coinglass.com"

// coinTableEntry holds parsed data for a single coin from the homepage table.
type coinTableEntry struct {
	Symbol       string
	FundingRate  float64
	Volume24h    decimal.Decimal
	OpenInterest decimal.Decimal
	OIChange1h   float64
	OIChange24h  float64
}

// cachedTable holds the last fetched homepage table data with TTL.
type cachedTable struct {
	entries map[string]coinTableEntry
	fetched time.Time
}

// CoinglassScraper implements port.DerivativesFeed by scraping the Coinglass
// website using a headless Chromium instance via chromedp.
// The homepage table is scraped via DOM extraction (API responses are encrypted).
// Per-symbol pages are scraped via DOM extraction.
type CoinglassScraper struct {
	allocCtx   context.Context
	cancel     context.CancelFunc
	timeout    time.Duration
	mu         sync.Mutex
	tableCache *cachedTable
	cacheTTL   time.Duration
}

// NewCoinglassScraper creates a scraper backed by a headless Chromium process.
func NewCoinglassScraper(timeout time.Duration) (*CoinglassScraper, error) {
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", true),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.Flag("disable-blink-features", "AutomationControlled"),
		chromedp.WindowSize(1920, 1080),
	)

	allocCtx, cancel := chromedp.NewExecAllocator(context.Background(), opts...)

	s := &CoinglassScraper{
		allocCtx: allocCtx,
		cancel:   cancel,
		timeout:  timeout,
		cacheTTL: 2 * time.Minute,
	}

	browserCtx, browserCancel := chromedp.NewContext(allocCtx)
	defer browserCancel()

	if err := chromedp.Run(browserCtx, chromedp.Navigate("about:blank")); err != nil {
		cancel()
		return nil, fmt.Errorf("coinglass scraper: chromium launch: %w", err)
	}

	slog.Info("coinglass scraper: headless Chromium started")
	return s, nil
}

func (s *CoinglassScraper) Close() {
	s.cancel()
	slog.Info("coinglass scraper: Chromium process terminated")
}

func (s *CoinglassScraper) FundingRate(ctx context.Context, symbol domain.Symbol) (*domain.FundingRate, error) {
	coin := coinGlassSymbol(symbol)
	entries, err := s.fetchDerivativesTable(ctx)
	if err != nil {
		return nil, fmt.Errorf("coinglass scraper: funding rate %s: %w", coin, err)
	}

	entry, ok := entries[coin]
	if !ok {
		return nil, fmt.Errorf("coinglass scraper: funding rate: coin %s not found in table", coin)
	}

	return &domain.FundingRate{
		Symbol:    symbol,
		Rate:      entry.FundingRate,
		FetchedAt: time.Now().UTC(),
	}, nil
}

func (s *CoinglassScraper) OpenInterest(ctx context.Context, symbol domain.Symbol) (*domain.OpenInterest, error) {
	coin := coinGlassSymbol(symbol)
	entries, err := s.fetchDerivativesTable(ctx)
	if err != nil {
		return nil, fmt.Errorf("coinglass scraper: open interest %s: %w", coin, err)
	}

	entry, ok := entries[coin]
	if !ok {
		return nil, fmt.Errorf("coinglass scraper: open interest: coin %s not found in table", coin)
	}

	return &domain.OpenInterest{
		Symbol:    symbol,
		TotalUSD:  entry.OpenInterest,
		Volume24h: entry.Volume24h,
		Change1h:  entry.OIChange1h,
		Change24h: entry.OIChange24h,
		FetchedAt: time.Now().UTC(),
	}, nil
}

func (s *CoinglassScraper) LiquidationZones(ctx context.Context, symbol domain.Symbol, refPrice decimal.Decimal, pricePct float64) ([]domain.LiquidationZone, error) {
	coin := coinGlassSymbol(symbol)
	url := coinglassBaseURL + "/LiquidationData"

	tabCtx, tabCancel := chromedp.NewContext(s.allocCtx)
	defer tabCancel()

	tabCtx, timeoutCancel := context.WithTimeout(tabCtx, s.timeout)
	defer timeoutCancel()

	var raw string
	if err := chromedp.Run(tabCtx,
		network.Enable(),
		emulation.SetUserAgentOverride(stealthUserAgent),
		chromedp.Navigate(url),
		chromedp.WaitReady("body", chromedp.ByQuery),
		chromedp.Sleep(6*time.Second),
		chromedp.Evaluate(`
			(() => {
				const tables = document.querySelectorAll('table');
				for (const table of tables) {
					const ths = Array.from(table.querySelectorAll('thead th'));
					const headers = ths.map(th => th.textContent.trim());
					if (headers.length < 6) continue;
					const hasSymbol = headers.some(h => /symbol|coin/i.test(h));
					const hasLong = headers.some(h => /long/i.test(h));
					const hasShort = headers.some(h => /short/i.test(h));
					if (!hasSymbol || !hasLong || !hasShort) continue;
					const rows = table.querySelectorAll('tbody tr');
					const entries = [];
					for (const row of rows) {
						const cells = Array.from(row.querySelectorAll('td')).map(td => td.textContent.trim());
						entries.push(cells);
					}
					return JSON.stringify({headers, entries});
				}
				return JSON.stringify({error: 'liquidation table not found'});
			})()
		`, &raw),
	); err != nil {
		return nil, fmt.Errorf("coinglass scraper: liquidations %s: %w", coin, err)
	}

	return parseLiquidationDOM(raw, symbol, refPrice, pricePct), nil
}

func (s *CoinglassScraper) FearGreed(ctx context.Context) (*domain.FearGreedIndex, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.alternative.me/fng/?limit=1", nil)
	if err != nil {
		return nil, fmt.Errorf("coinglass scraper: fear greed request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("coinglass scraper: fear greed fetch: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("coinglass scraper: fear greed read: %w", err)
	}

	return parseAlternativeMeFearGreed(body), nil
}

func (s *CoinglassScraper) Snapshot(ctx context.Context, symbol domain.Symbol) (*domain.DerivativesSnapshot, error) {
	snap := &domain.DerivativesSnapshot{
		Symbol:    symbol,
		FetchedAt: time.Now().UTC(),
	}

	// Use a detached context with the scraper's own timeout so that a
	// tight caller-side deadline (e.g. the LLM's 120 s total budget)
	// doesn't starve the fetches. Parent cancellation is still bridged.
	snapCtx, snapCancel := context.WithTimeout(context.Background(), s.timeout+5*time.Second)
	defer snapCancel()
	go func() {
		select {
		case <-ctx.Done():
			snapCancel()
		case <-snapCtx.Done():
		}
	}()

	g, gctx := errgroup.WithContext(snapCtx)

	var (
		fr *domain.FundingRate
		oi *domain.OpenInterest
		fg *domain.FearGreedIndex
		ls *domain.LongShortRatio
	)

	g.Go(func() error {
		if r, err := s.FundingRate(gctx, symbol); err == nil {
			fr = r
		} else {
			slog.Warn("coinglass scraper: snapshot: funding rate failed", "symbol", symbol, "error", err)
		}
		return nil
	})

	g.Go(func() error {
		if r, err := s.OpenInterest(gctx, symbol); err == nil {
			oi = r
		} else {
			slog.Warn("coinglass scraper: snapshot: open interest failed", "symbol", symbol, "error", err)
		}
		return nil
	})

	g.Go(func() error {
		if r, err := s.FearGreed(gctx); err == nil {
			fg = r
		} else {
			slog.Warn("coinglass scraper: snapshot: fear greed failed", "error", err)
		}
		return nil
	})

	g.Go(func() error {
		if r, err := s.fetchLongShortRatio(gctx, symbol); err == nil {
			ls = r
		} else {
			slog.Warn("coinglass scraper: snapshot: long/short ratio failed", "symbol", symbol, "error", err)
		}
		return nil
	})

	_ = g.Wait()

	if fr != nil {
		snap.FundingRate = *fr
	}
	if oi != nil {
		snap.OpenInterest = *oi
	}
	if fg != nil {
		snap.FearGreed = *fg
	}
	if ls != nil {
		snap.LongShortRatio = *ls
	}

	slog.Info("coinglass scraper: snapshot complete",
		"symbol", symbol,
		"funding_rate", snap.FundingRate.Rate,
		"oi_total_usd", snap.OpenInterest.TotalUSD.String(),
		"volume_24h", snap.OpenInterest.Volume24h.String(),
		"fear_greed", snap.FearGreed.Value,
		"long_short_ratio", snap.LongShortRatio.GlobalRatio)

	return snap, nil
}

func (s *CoinglassScraper) fetchLongShortRatio(ctx context.Context, symbol domain.Symbol) (*domain.LongShortRatio, error) {
	coin := coinGlassSymbol(symbol)
	url := coinglassBaseURL + "/LongShortRatio/" + coin

	tabCtx, tabCancel := chromedp.NewContext(s.allocCtx)
	defer tabCancel()

	tabCtx, timeoutCancel := context.WithTimeout(tabCtx, s.timeout)
	defer timeoutCancel()

	var raw string
	if err := chromedp.Run(tabCtx,
		network.Enable(),
		emulation.SetUserAgentOverride(stealthUserAgent),
		chromedp.Navigate(url),
		chromedp.WaitReady("body", chromedp.ByQuery),
		chromedp.Sleep(6*time.Second),
		chromedp.Evaluate(`
			(() => {
				const tables = document.querySelectorAll('table');
				for (const table of tables) {
					const ths = Array.from(table.querySelectorAll('thead th'));
					const headers = ths.map(th => th.textContent.trim());
					if (headers.length < 6) continue;
					const hasExchange = headers.some(h => /exchange/i.test(h));
					const hasLongShort = headers.some(h => /long/i.test(h) && /short/i.test(h));
					if (!hasExchange || !hasLongShort) continue;
					const rows = table.querySelectorAll('tbody tr');
					const entries = [];
					for (const row of rows) {
						const cells = Array.from(row.querySelectorAll('td')).map(td => td.textContent.trim());
						entries.push(cells);
					}
					return JSON.stringify({headers, entries});
				}
				return JSON.stringify({error: 'long/short table not found'});
			})()
		`, &raw),
	); err != nil {
		return nil, fmt.Errorf("coinglass scraper: long/short %s: %w", coin, err)
	}

	return parseLongShortDOM(raw, symbol), nil
}

// fetchDerivativesTable scrapes the Coinglass homepage derivatives table via
// DOM extraction. Returns a map keyed by coin symbol (e.g. "BTC", "ETH").
// Results are cached for 2 minutes.
func (s *CoinglassScraper) fetchDerivativesTable(ctx context.Context) (map[string]coinTableEntry, error) {
	// Fast path: check cache under lock.
	s.mu.Lock()
	if s.tableCache != nil && time.Since(s.tableCache.fetched) < s.cacheTTL {
		entries := s.tableCache.entries
		s.mu.Unlock()
		return entries, nil
	}
	s.mu.Unlock()

	// Slow path: scrape outside the lock so concurrent calls (e.g. from
	// Snapshot) are not serialized by the browser operation.
	tabCtx, tabCancel := chromedp.NewContext(s.allocCtx)
	defer tabCancel()

	tabCtx, timeoutCancel := context.WithTimeout(tabCtx, s.timeout)
	defer timeoutCancel()

	var tableJSON string
	if err := chromedp.Run(tabCtx,
		network.Enable(),
		emulation.SetUserAgentOverride(stealthUserAgent),
		chromedp.Navigate(coinglassBaseURL+"/"),
		chromedp.WaitReady("body", chromedp.ByQuery),
		chromedp.Sleep(5*time.Second),

		// Extract table[1] rows (the derivatives table with data rows).
		// Columns: 0=fav, 1=rank, 2=symbol, 3=price, 4=price24h%,
		// 5=fundingRate, 6=volume24h, 7=volume24h%, 8=mcap,
		// 9=oi, 10=oi1h%, 11=oi24h%, 12=liquidation24h
		chromedp.Evaluate(`
			(() => {
				const tables = document.querySelectorAll('table');
				if (tables.length < 2) return JSON.stringify({error: 'table not found', count: tables.length});
				const rows = tables[1].querySelectorAll('tbody tr');
				const entries = [];
				for (const row of rows) {
					const cells = row.querySelectorAll('td');
					if (cells.length < 12) continue;
					const symbol = cells[2].textContent.trim();
					if (!symbol) continue;
					entries.push({
						symbol: symbol,
						fundingRate: cells[5].textContent.trim(),
						volume24h: cells[6].textContent.trim(),
						openInterest: cells[9].textContent.trim(),
						oiChange1h: cells[10].textContent.trim(),
						oiChange24h: cells[11].textContent.trim()
					});
				}
				return JSON.stringify(entries);
			})()
		`, &tableJSON),
	); err != nil {
		return nil, fmt.Errorf("scrape homepage table: %w", err)
	}

	entries, err := parseDerivativesTableJSON(tableJSON)
	if err != nil {
		return nil, fmt.Errorf("parse homepage table: %w", err)
	}

	s.mu.Lock()
	s.tableCache = &cachedTable{
		entries: entries,
		fetched: time.Now(),
	}
	s.mu.Unlock()

	slog.Info("coinglass scraper: homepage table scraped", "coins", len(entries))
	return entries, nil
}

// parseDerivativesTableJSON converts the raw JS-extracted JSON into a map.
func parseDerivativesTableJSON(raw string) (map[string]coinTableEntry, error) {
	var items []struct {
		Symbol       string `json:"symbol"`
		FundingRate  string `json:"fundingRate"`
		Volume24h    string `json:"volume24h"`
		OpenInterest string `json:"openInterest"`
		OIChange1h   string `json:"oiChange1h"`
		OIChange24h  string `json:"oiChange24h"`
	}

	if err := json.Unmarshal([]byte(raw), &items); err != nil {
		return nil, fmt.Errorf("unmarshal table JSON: %w", err)
	}

	entries := make(map[string]coinTableEntry, len(items))
	for _, item := range items {
		if item.Symbol == "" {
			continue
		}

		fr := parsePercentage(item.FundingRate)
		vol := parseDollarAmount(item.Volume24h)
		oi := parseDollarAmount(item.OpenInterest)
		oi1h := parsePercentage(item.OIChange1h)
		oi24h := parsePercentage(item.OIChange24h)

		entries[item.Symbol] = coinTableEntry{
			Symbol:       item.Symbol,
			FundingRate:  fr,
			Volume24h:    vol,
			OpenInterest: oi,
			OIChange1h:   oi1h,
			OIChange24h:  oi24h,
		}
	}

	return entries, nil
}

// parsePercentage parses strings like "0.0001%" or "-0.0078%" into a float64.
func parsePercentage(s string) float64 {
	s = strings.TrimSpace(s)
	s = strings.TrimSuffix(s, "%")
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return v
}

// parseDollarAmount parses strings like "$44.99B", "$56.09M", "$1.51T" into decimal.
func parseDollarAmount(s string) decimal.Decimal {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "$")
	s = strings.ReplaceAll(s, ",", "")

	if len(s) == 0 {
		return decimal.Zero
	}

	multiplier := decimal.NewFromInt(1)
	last := s[len(s)-1]
	switch last {
	case 'B':
		multiplier = decimal.NewFromInt(1_000_000_000)
		s = s[:len(s)-1]
	case 'M':
		multiplier = decimal.NewFromInt(1_000_000)
		s = s[:len(s)-1]
	case 'T':
		multiplier = decimal.NewFromInt(1_000_000_000_000)
		s = s[:len(s)-1]
	case 'K', 'k':
		multiplier = decimal.NewFromInt(1_000)
		s = s[:len(s)-1]
	}

	d, err := decimal.NewFromString(s)
	if err != nil {
		return decimal.Zero
	}
	return d.Mul(multiplier)
}

func coinGlassSymbol(sym domain.Symbol) string {
	s := string(sym)
	if idx := strings.Index(s, "/"); idx > 0 {
		return s[:idx]
	}
	if len(s) > 4 && s[len(s)-4:] == "USDT" {
		return s[:len(s)-4]
	}
	return s
}

func parseDecimal(s string) decimal.Decimal {
	cleaned := strings.ReplaceAll(s, ",", "")
	cleaned = strings.ReplaceAll(cleaned, "$", "")
	cleaned = strings.TrimSpace(cleaned)
	d, err := decimal.NewFromString(cleaned)
	if err != nil {
		return decimal.Zero
	}
	return d
}

func parseAlternativeMeFearGreed(body []byte) *domain.FearGreedIndex {
	now := time.Now().UTC()
	result := &domain.FearGreedIndex{FetchedAt: now}

	var envelope struct {
		Data []struct {
			Value          string `json:"value"`
			Classification string `json:"value_classification"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil || len(envelope.Data) == 0 {
		slog.Warn("coinglass scraper: parse alternative.me fear greed", "error", err)
		return result
	}

	v, _ := strconv.Atoi(envelope.Data[0].Value)
	result.Value = v
	result.Category = envelope.Data[0].Classification

	slog.Info("coinglass scraper: fear greed parsed",
		"value", result.Value, "category", result.Category)
	return result
}

func parseLongShortDOM(raw string, symbol domain.Symbol) *domain.LongShortRatio {
	now := time.Now().UTC()
	result := &domain.LongShortRatio{Symbol: symbol, FetchedAt: now}

	var table struct {
		Headers []string   `json:"headers"`
		Entries [][]string `json:"entries"`
		Error   string     `json:"error"`
	}
	if err := json.Unmarshal([]byte(raw), &table); err != nil || table.Error != "" {
		slog.Warn("coinglass scraper: parse long/short DOM", "raw_error", err, "msg", table.Error)
		return result
	}

	// Find the Long/Short column for the shortest timeframe (prefer 1h, then 4h, then 24h).
	colIdx := -1
	for _, keyword := range []string{"1h", "4h", "24h"} {
		for i, h := range table.Headers {
			hLower := strings.ToLower(h)
			if strings.Contains(hLower, keyword) &&
				(strings.Contains(hLower, "long") || strings.Contains(hLower, "/")) {
				colIdx = i
				break
			}
		}
		if colIdx >= 0 {
			break
		}
	}
	if colIdx < 0 {
		slog.Warn("coinglass scraper: long/short column not found", "headers", table.Headers)
		return result
	}

	// Average the ratio values across exchanges.
	var sum float64
	var count int
	for _, cells := range table.Entries {
		if colIdx >= len(cells) {
			continue
		}
		cell := cells[colIdx]
		if v, err := strconv.ParseFloat(cell, 64); err == nil && v > 0 {
			sum += v
			count++
			continue
		}
		if v := parsePercentage(cell); v > 0 {
			sum += v / (100 - v)
			count++
		}
	}

	if count > 0 {
		result.GlobalRatio = sum / float64(count)
		result.TopShortPct = 100 / (1 + result.GlobalRatio)
		result.TopLongPct = 100 - result.TopShortPct
	}

	slog.Info("coinglass scraper: long/short parsed",
		"symbol", symbol, "global_ratio", result.GlobalRatio)
	return result
}

func parseLiquidationDOM(raw string, symbol domain.Symbol, refPrice decimal.Decimal, pricePct float64) []domain.LiquidationZone {
	var table struct {
		Headers []string   `json:"headers"`
		Entries [][]string `json:"entries"`
		Error   string     `json:"error"`
	}
	if err := json.Unmarshal([]byte(raw), &table); err != nil || table.Error != "" {
		slog.Warn("coinglass scraper: parse liquidation DOM", "raw_error", err, "msg", table.Error)
		return nil
	}

	coin := coinGlassSymbol(symbol)

	var symbolCol, longCol, shortCol = -1, -1, -1
	for i, h := range table.Headers {
		hLower := strings.ToLower(h)
		if strings.Contains(hLower, "symbol") || strings.Contains(hLower, "coin") {
			if symbolCol < 0 {
				symbolCol = i
			}
		}
		if strings.Contains(hLower, "1h") && strings.Contains(hLower, "long") {
			longCol = i
		}
		if strings.Contains(hLower, "1h") && strings.Contains(hLower, "short") {
			shortCol = i
		}
	}
	if symbolCol < 0 || (longCol < 0 && shortCol < 0) {
		slog.Warn("coinglass scraper: liquidation columns not found", "headers", table.Headers)
		return nil
	}

	var longAmt, shortAmt decimal.Decimal
	for _, cells := range table.Entries {
		if symbolCol >= len(cells) {
			continue
		}
		cell := cells[symbolCol]
		if !strings.EqualFold(cell, coin) && !strings.HasPrefix(strings.ToUpper(cell), coin) {
			continue
		}
		if longCol >= 0 && longCol < len(cells) {
			longAmt = parseDollarAmount(cells[longCol])
		}
		if shortCol >= 0 && shortCol < len(cells) {
			shortAmt = parseDollarAmount(cells[shortCol])
		}
		break
	}

	if longAmt.IsZero() && shortAmt.IsZero() {
		slog.Warn("coinglass scraper: liquidation data not found for coin", "coin", coin)
		return nil
	}

	halfRange := refPrice.Mul(decimal.NewFromFloat(pricePct / 200))
	var zones []domain.LiquidationZone

	if !longAmt.IsZero() && !refPrice.IsZero() {
		zones = append(zones, domain.LiquidationZone{
			PriceLow:  refPrice.Sub(halfRange),
			PriceHigh: refPrice.Add(halfRange),
			AmountUSD: longAmt,
			Side:      domain.SideSell,
		})
	}

	if !shortAmt.IsZero() && !refPrice.IsZero() {
		zones = append(zones, domain.LiquidationZone{
			PriceLow:  refPrice.Sub(halfRange),
			PriceHigh: refPrice.Add(halfRange),
			AmountUSD: shortAmt,
			Side:      domain.SideBuy,
		})
	}

	slog.Info("coinglass scraper: liquidation zones parsed",
		"symbol", symbol, "zones", len(zones))
	return zones
}

var _ port.DerivativesFeed = (*CoinglassScraper)(nil)

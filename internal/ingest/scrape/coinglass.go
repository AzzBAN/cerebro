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
	"github.com/chromedp/cdproto/cdp"
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
// Per-symbol pages use XHR interception for endpoints not on the homepage.
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
	url := coinglassBaseURL + "/LiquidationData/" + coin

	body, err := s.interceptAPI(ctx, url, matchKeywords("liquidation"))
	if err != nil {
		return nil, fmt.Errorf("coinglass scraper: liquidations %s: %w", coin, err)
	}

	return parseLiquidationResponse(body, symbol, refPrice, pricePct), nil
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

	body, err := s.interceptAPI(ctx, url, matchKeywords("longshortrate"))
	if err != nil {
		return nil, fmt.Errorf("coinglass scraper: long/short %s: %w", coin, err)
	}

	return parseLongShortResponse(body, symbol), nil
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

// urlMatcher determines whether an intercepted XHR/fetch URL is relevant.
type urlMatcher func(url string) bool

// matchKeywords returns a matcher that checks all keywords appear in the URL
// (case-insensitive).
func matchKeywords(keywords ...string) urlMatcher {
	return func(url string) bool {
		lower := strings.ToLower(url)
		for _, kw := range keywords {
			if !strings.Contains(lower, strings.ToLower(kw)) {
				return false
			}
		}
		return true
	}
}

// interceptAPI navigates to url and listens for XHR/fetch responses that match
// the given urlMatcher. It returns the first matching response body as raw JSON.
func (s *CoinglassScraper) interceptAPI(ctx context.Context, pageURL string, matcher urlMatcher) (json.RawMessage, error) {
	tabCtx, tabCancel := chromedp.NewContext(s.allocCtx)
	defer tabCancel()

	tabCtx, timeoutCancel := context.WithTimeout(tabCtx, s.timeout)
	defer timeoutCancel()

	bodyCh := make(chan json.RawMessage, 1)

	chromedp.ListenTarget(tabCtx, func(ev interface{}) {
		respEvt, ok := ev.(*network.EventResponseReceived)
		if !ok {
			return
		}

		respURL := respEvt.Response.URL

		if !strings.Contains(respURL, "capi.coinglass.com/api/") ||
			strings.Contains(respURL, "/strapi/") {
			return
		}
		if respEvt.Response.Status == 204 || respEvt.Response.Status == 304 {
			return
		}
		if !matcher(respURL) {
			return
		}

		slog.Debug("coinglass scraper: intercepted API response",
			"url", respURL, "status", respEvt.Response.Status)

		go func(requestID network.RequestID) {
			if tabCtx.Err() != nil {
				return
			}
			cc := chromedp.FromContext(tabCtx)
			if cc == nil || cc.Target == nil {
				return
			}
			execCtx := cdp.WithExecutor(tabCtx, cc.Target)
			body, err := network.GetResponseBody(requestID).Do(execCtx)
			if err != nil {
				slog.Debug("coinglass scraper: response body not available",
					"url", respURL, "error", err)
				return
			}

			select {
			case bodyCh <- json.RawMessage(body):
			default:
			}
		}(respEvt.RequestID)
	})

	if err := chromedp.Run(tabCtx,
		network.Enable(),
		emulation.SetUserAgentOverride(stealthUserAgent),
		chromedp.Navigate(pageURL),
		chromedp.WaitReady("body", chromedp.ByQuery),
		chromedp.Sleep(2*time.Second),
	); err != nil {
		return nil, fmt.Errorf("navigate %s: %w", pageURL, err)
	}

	select {
	case body := <-bodyCh:
		if len(body) == 0 {
			return nil, fmt.Errorf("empty response from %s", pageURL)
		}
		return body, nil
	case <-tabCtx.Done():
		return nil, fmt.Errorf("timeout waiting for API response from %s: %w", pageURL, tabCtx.Err())
	}
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

func parseAPIResponse(body json.RawMessage) (json.RawMessage, error) {
	var envelope struct {
		Success bool            `json:"success"`
		Code    string          `json:"code"`
		Data    json.RawMessage `json:"data"`
		Message string          `json:"message"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return body, nil
	}
	if envelope.Message != "" && !envelope.Success {
		return nil, fmt.Errorf("API error: %s", envelope.Message)
	}
	if envelope.Data != nil {
		return envelope.Data, nil
	}
	return body, nil
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

func parseLongShortResponse(body json.RawMessage, symbol domain.Symbol) *domain.LongShortRatio {
	now := time.Now().UTC()
	result := &domain.LongShortRatio{Symbol: symbol, FetchedAt: now}

	data, err := parseAPIResponse(body)
	if err != nil {
		slog.Warn("coinglass scraper: parse long/short envelope", "error", err)
		return result
	}

	var arr []struct {
		GlobalRatio float64 `json:"globalRatio"`
		TopLongPct  float64 `json:"topLongPct"`
		TopShortPct float64 `json:"topShortPct"`
	}
	if err := json.Unmarshal(data, &arr); err == nil && len(arr) > 0 {
		result.GlobalRatio = arr[0].GlobalRatio
		result.TopLongPct = arr[0].TopLongPct
		result.TopShortPct = arr[0].TopShortPct
	}

	slog.Info("coinglass scraper: long/short parsed",
		"symbol", symbol, "global_ratio", result.GlobalRatio)
	return result
}

func parseLiquidationResponse(body json.RawMessage, symbol domain.Symbol, refPrice decimal.Decimal, pricePct float64) []domain.LiquidationZone {
	data, err := parseAPIResponse(body)
	if err != nil {
		slog.Warn("coinglass scraper: parse liquidation envelope", "error", err)
		return nil
	}

	var rawZones []struct {
		Price     float64 `json:"price"`
		AmountUSD float64 `json:"amountUSD"`
		Side      string  `json:"side"`
	}
	if err := json.Unmarshal(data, &rawZones); err != nil {
		return nil
	}

	var zones []domain.LiquidationZone
	for _, z := range rawZones {
		price := decimal.NewFromFloat(z.Price)
		amount := decimal.NewFromFloat(z.AmountUSD)
		if price.IsZero() {
			continue
		}

		if pricePct > 0 && !refPrice.IsZero() {
			lower := refPrice.Mul(decimal.NewFromFloat(1 - pricePct/100))
			upper := refPrice.Mul(decimal.NewFromFloat(1 + pricePct/100))
			if price.LessThan(lower) || price.GreaterThan(upper) {
				continue
			}
		}

		side := domain.SideSell
		if strings.EqualFold(z.Side, "short") {
			side = domain.SideBuy
		}

		zones = append(zones, domain.LiquidationZone{
			PriceLow:  price.Mul(decimal.NewFromFloat(0.995)),
			PriceHigh: price.Mul(decimal.NewFromFloat(1.005)),
			AmountUSD: amount,
			Side:      side,
		})
	}

	slog.Info("coinglass scraper: liquidation zones parsed",
		"symbol", symbol, "zones", len(zones))
	return zones
}

var _ port.DerivativesFeed = (*CoinglassScraper)(nil)

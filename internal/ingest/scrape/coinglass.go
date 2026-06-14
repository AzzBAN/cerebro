package scrape

import (
	"context"
	"encoding/json"
	"errors"
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

// ErrSymbolNotCovered is returned when Coinglass simply does not list the
// requested coin in its derivatives table (e.g. XAU is a precious-metals
// perp on Binance but doesn't appear on Coinglass). This is a structural
// "not supported here" rather than a transient failure, so callers
// (Snapshot, screening) should treat it as INFO/DEBUG, not WARN, and
// avoid retrying.
var ErrSymbolNotCovered = errors.New("coinglass scraper: symbol not covered")

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
	mu         sync.Mutex    // guards tableCache reads/writes
	scrapeMu   sync.Mutex    // serializes homepage scrapes to prevent concurrent tab stampede
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
		return nil, fmt.Errorf("coinglass scraper: funding rate: coin %s: %w", coin, ErrSymbolNotCovered)
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
		return nil, fmt.Errorf("coinglass scraper: open interest: coin %s: %w", coin, ErrSymbolNotCovered)
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

	// logSnapshotErr demotes a structurally-unsupported-symbol error to
	// DEBUG so unsupported assets (e.g. XAU/USDT-PERP, which Binance lists
	// but Coinglass does not) don't spam WARN every cycle. Real failures
	// (network timeouts, parse errors, table-not-found) stay WARN.
	logSnapshotErr := func(msg string, err error) {
		if errors.Is(err, ErrSymbolNotCovered) {
			slog.Debug(msg+" (symbol not covered by Coinglass)",
				"symbol", symbol, "error", err)
			return
		}
		slog.Warn(msg, "symbol", symbol, "error", err)
	}

	g.Go(func() error {
		if r, err := s.FundingRate(gctx, symbol); err == nil {
			fr = r
		} else {
			logSnapshotErr("coinglass scraper: snapshot: funding rate failed", err)
		}
		return nil
	})

	g.Go(func() error {
		if r, err := s.OpenInterest(gctx, symbol); err == nil {
			oi = r
		} else {
			logSnapshotErr("coinglass scraper: snapshot: open interest failed", err)
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
			logSnapshotErr("coinglass scraper: snapshot: long/short ratio failed", err)
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

	// Fast-path: skip non-crypto tickers (e.g. XAU, the gold synthetic)
	// outright — Coinglass does not list them and the per-symbol page
	// just returns an empty shell.
	if !isCoinglassSupportedCoin(coin) {
		return nil, fmt.Errorf("coinglass scraper: long/short %s: %w", coin, ErrSymbolNotCovered)
	}

	// Fast-path: if the coin isn't on the homepage derivatives table, the
	// per-symbol /LongShortRatio/<coin> page will 404 or hang. Short-circuit
	// before spending ~6s of Chromium time on a doomed navigation. We
	// tolerate a derivatives-table fetch error here (transient Coinglass
	// outage) and fall through to attempt the navigation anyway, which
	// preserves prior behaviour.
	if entries, err := s.fetchDerivativesTable(ctx); err == nil {
		if _, ok := entries[coin]; !ok {
			return nil, fmt.Errorf("coinglass scraper: long/short %s: %w", coin, ErrSymbolNotCovered)
		}
	}

	url := coinglassBaseURL + "/LongShortRatio/" + coin

	tabCtx, tabCancel := chromedp.NewContext(s.allocCtx)
	defer tabCancel()

	tabCtx, timeoutCancel := context.WithTimeout(tabCtx, s.timeout)
	defer timeoutCancel()

	// DOM scan: as of 2026 the LongShortRatio page serves a compact
	// summary table with columns roughly shaped like:
	//
	//     Type  | Long/Short | Sentiment
	//     1h    | 0.98       | Neutral
	//     4h    | 1.12       | Bullish
	//     24h   | 0.85       | Bearish
	//
	// There is no longer an Exchange column, so the old 6-column +
	// hasExchange gate is dropped. The scan now accepts any table whose
	// headers include a "Long/Short" (or "Long / Short") column, and
	// additionally surfaces all table layouts found on the page so the
	// operator can diagnose future redesigns from the log.
	var raw string
	if err := chromedp.Run(tabCtx,
		network.Enable(),
		emulation.SetUserAgentOverride(stealthUserAgent),
		chromedp.Navigate(url),
		chromedp.WaitReady("body", chromedp.ByQuery),
		chromedp.Sleep(6*time.Second),
		chromedp.Evaluate(`
			(() => {
				const allTables = [];
				for (const table of document.querySelectorAll('table')) {
					const ths = Array.from(table.querySelectorAll('thead th'));
					const headers = ths.map(th => th.textContent.trim());
					const rows = table.querySelectorAll('tbody tr');
					const entries = [];
					for (const row of rows) {
						const cells = Array.from(row.querySelectorAll('td')).map(td => td.textContent.trim());
						entries.push(cells);
					}
					allTables.push({headers, entries});
					const hasLongShort = headers.some(h => /long\s*\/\s*short/i.test(h) || (/long/i.test(h) && /short/i.test(h)));
					if (hasLongShort && entries.length > 0) {
						return JSON.stringify({headers, entries});
					}
				}
				return JSON.stringify({error: 'long/short table not found', seen: allTables});
			})()
		`, &raw),
	); err != nil {
		return nil, fmt.Errorf("coinglass scraper: long/short %s: %w", coin, err)
	}

	return parseLongShortDOM(raw, symbol), nil
}

// isCoinglassSupportedCoin filters obvious non-crypto symbols out of the
// Coinglass pipeline. The Coinglass universe is crypto derivatives only
// (BTC, ETH, SOL, …) — synthetic/commodity tickers such as XAU (gold)
// route through Binance but have no Coinglass equivalent and would waste
// a full Chromium navigation on every snapshot tick.
func isCoinglassSupportedCoin(coin string) bool {
	switch strings.ToUpper(strings.TrimSpace(coin)) {
	case "", "XAU", "XAG", "XAUUSD", "XAGUSD":
		return false
	}
	return true
}

// fetchDerivativesTable scrapes the Coinglass homepage derivatives table via
// DOM extraction. Returns a map keyed by coin symbol (e.g. "BTC", "ETH").
// Results are cached for 2 minutes.
func (s *CoinglassScraper) fetchDerivativesTable(ctx context.Context) (map[string]coinTableEntry, error) {
	// Fast path: check cache under read lock.
	if cached := s.getCachedTable(); cached != nil {
		return cached, nil
	}

	// Slow path: serialize scrapes so concurrent callers (e.g. FundingRate +
	// OpenInterest fired from the same Snapshot) don't each open a browser tab.
	s.scrapeMu.Lock()
	defer s.scrapeMu.Unlock()

	// Double-check: another goroutine may have populated the cache while we waited.
	if cached := s.getCachedTable(); cached != nil {
		return cached, nil
	}

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
		// Wait for the derivatives table to appear in the DOM before extracting.
		// Falls back to a fixed sleep if the selector doesn't appear in time
		// (Coinglass sometimes renders via client-side hydration).
		chromedp.ActionFunc(func(ctx context.Context) error {
			waitCtx, waitCancel := context.WithTimeout(ctx, 8*time.Second)
			defer waitCancel()
			err := chromedp.WaitVisible("table tbody tr", chromedp.ByQuery).Do(waitCtx)
			if err != nil {
				slog.Debug("coinglass scraper: WaitVisible timed out; falling back to sleep", "error", err)
				chromedp.Sleep(5 * time.Second).Do(ctx)
			}
			return nil
		}),

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

// getCachedTable returns the cached entries if still valid, or nil.
func (s *CoinglassScraper) getCachedTable() map[string]coinTableEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.tableCache != nil && time.Since(s.tableCache.fetched) < s.cacheTTL {
		return s.tableCache.entries
	}
	return nil
}

// parseDerivativesTableJSON converts the raw JS-extracted JSON into a map.
func parseDerivativesTableJSON(raw string) (map[string]coinTableEntry, error) {
	// The JS extractor returns an error object when the table is not found
	// (e.g. page didn't fully render). Detect this before attempting array
	// unmarshal so we get a clear error message.
	var errObj struct {
		Error string `json:"error"`
		Count int    `json:"count"`
	}
	if json.Unmarshal([]byte(raw), &errObj) == nil && errObj.Error != "" {
		return nil, fmt.Errorf("DOM extraction failed: %s (tables found: %d)", errObj.Error, errObj.Count)
	}

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
		// `seen` is populated by the DOM scanner only when no matching
		// table was found. It carries every table shape encountered on
		// the page so the operator can diagnose selector drift from the
		// log without re-running the scraper.
		Seen []struct {
			Headers []string   `json:"headers"`
			Entries [][]string `json:"entries"`
		} `json:"seen"`
	}
	if err := json.Unmarshal([]byte(raw), &table); err != nil {
		slog.Warn("coinglass scraper: parse long/short DOM",
			"symbol", symbol, "raw_error", err, "preview", firstN(raw, 200))
		return result
	}
	if table.Error != "" {
		// Downgrade to Debug — missing long/short data is expected for
		// less popular coins and for Coinglass page rebuilds. Include
		// the first seen-table shape so operators can update the selector.
		var firstHeaders []string
		if len(table.Seen) > 0 {
			firstHeaders = table.Seen[0].Headers
		}
		slog.Debug("coinglass scraper: long/short table not found",
			"symbol", symbol, "msg", table.Error,
			"tables_on_page", len(table.Seen),
			"first_headers", firstHeaders)
		return result
	}

	// Resolve the "Long/Short" column. Prefer an explicit combined
	// column ("Long/Short", "Long / Short") and fall back to any header
	// that mentions both "long" and "short" (covers historical layouts
	// like "Long/Short 1h").
	colIdx := findLongShortColumn(table.Headers)
	if colIdx < 0 {
		slog.Debug("coinglass scraper: long/short column not found",
			"symbol", symbol, "headers", table.Headers)
		return result
	}

	// Prefer the 1h row when the Type column is present; otherwise
	// average across whatever rows we got (legacy per-exchange layouts).
	typeIdx := findTypeColumn(table.Headers)
	if v, ok := extractPreferredRatio(table.Entries, typeIdx, colIdx); ok {
		result.GlobalRatio = v
	} else {
		result.GlobalRatio = averageRatio(table.Entries, colIdx)
	}

	if result.GlobalRatio > 0 {
		result.TopShortPct = 100 / (1 + result.GlobalRatio)
		result.TopLongPct = 100 - result.TopShortPct
		slog.Info("coinglass scraper: long/short parsed",
			"symbol", symbol, "global_ratio", result.GlobalRatio)
	} else {
		slog.Debug("coinglass scraper: long/short DOM yielded no numeric ratio",
			"symbol", symbol, "headers", table.Headers, "rows", len(table.Entries))
	}
	return result
}

// findLongShortColumn returns the index of the header that represents the
// long/short ratio cell. -1 when absent.
//
// Search order:
//  1. Per-timeframe headers in preference order (1h → 4h → 24h) — legacy
//     per-exchange layout that embeds the timeframe in the header, e.g.
//     "Long/Short 1h".
//  2. A bare "Long/Short" / "Long / Short" header — current summary
//     layout where the timeframe is a separate `Type` column.
//  3. Any header containing both "long" and "short" as a last resort.
func findLongShortColumn(headers []string) int {
	lowered := make([]string, len(headers))
	for i, h := range headers {
		lowered[i] = strings.ToLower(h)
	}

	// Pass 1: "Long/Short <tf>" with preferred timeframes.
	for _, tf := range []string{"1h", "4h", "24h"} {
		for i, h := range lowered {
			if (strings.Contains(h, "long/short") || strings.Contains(h, "long / short")) &&
				strings.Contains(h, tf) {
				return i
			}
		}
	}
	// Pass 2: bare "Long/Short" column (no timeframe embedded).
	for i, h := range lowered {
		if strings.Contains(h, "long/short") || strings.Contains(h, "long / short") {
			return i
		}
	}
	// Pass 3: any single header mentioning both words.
	for i, h := range lowered {
		if strings.Contains(h, "long") && strings.Contains(h, "short") {
			return i
		}
	}
	return -1
}

// findTypeColumn locates a "Type" / "Timeframe" column. -1 when absent.
func findTypeColumn(headers []string) int {
	for i, h := range headers {
		lower := strings.ToLower(strings.TrimSpace(h))
		if lower == "type" || lower == "timeframe" || lower == "interval" {
			return i
		}
	}
	return -1
}

// extractPreferredRatio pulls the ratio from the first row whose Type
// column matches one of the preferred timeframes, in order of preference.
// Returns (value, true) on success.
func extractPreferredRatio(entries [][]string, typeIdx, colIdx int) (float64, bool) {
	if typeIdx < 0 {
		return 0, false
	}
	for _, want := range []string{"1h", "4h", "24h"} {
		for _, cells := range entries {
			if typeIdx >= len(cells) || colIdx >= len(cells) {
				continue
			}
			if strings.EqualFold(strings.TrimSpace(cells[typeIdx]), want) {
				if v, ok := parseLongShortCell(cells[colIdx]); ok && v > 0 {
					return v, true
				}
			}
		}
	}
	return 0, false
}

// averageRatio averages the numeric values of the long/short column
// across all entries. Non-numeric or non-positive cells are skipped.
func averageRatio(entries [][]string, colIdx int) float64 {
	var sum float64
	var count int
	for _, cells := range entries {
		if colIdx >= len(cells) {
			continue
		}
		if v, ok := parseLongShortCell(cells[colIdx]); ok && v > 0 {
			sum += v
			count++
		}
	}
	if count == 0 {
		return 0
	}
	return sum / float64(count)
}

// parseLongShortCell accepts both absolute ratio values ("1.25") and
// percentage pairs ("55.5%" meaning "55.5% long"), converting the latter
// to the canonical ratio long/short = pct / (100 - pct).
func parseLongShortCell(cell string) (float64, bool) {
	cell = strings.TrimSpace(cell)
	if cell == "" {
		return 0, false
	}
	if v, err := strconv.ParseFloat(cell, 64); err == nil {
		return v, true
	}
	if v := parsePercentage(cell); v > 0 && v < 100 {
		return v / (100 - v), true
	}
	return 0, false
}

// firstN returns s truncated to n runes for log previews.
func firstN(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n]) + "…"
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

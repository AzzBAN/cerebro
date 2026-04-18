package scrape

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/azhar/cerebro/internal/domain"
	"github.com/azhar/cerebro/internal/port"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
	"github.com/shopspring/decimal"
)

const coinglassBaseURL = "https://www.coinglass.com"

// CoinglassScraper implements port.DerivativesFeed by scraping the Coinglass
// website using a headless Chromium instance via chromedp.
// It intercepts XHR/fetch network responses to capture data from Coinglass's
// internal API, bypassing DOM parsing entirely.
type CoinglassScraper struct {
	allocCtx context.Context
	cancel   context.CancelFunc
	timeout  time.Duration
	mu       sync.Mutex
}

// NewCoinglassScraper creates a scraper backed by a headless Chromium process.
func NewCoinglassScraper(timeout time.Duration) (*CoinglassScraper, error) {
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", true),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.WindowSize(1920, 1080),
	)

	allocCtx, cancel := chromedp.NewExecAllocator(context.Background(), opts...)

	s := &CoinglassScraper{
		allocCtx: allocCtx,
		cancel:   cancel,
		timeout:  timeout,
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

// Close terminates the headless Chromium process.
func (s *CoinglassScraper) Close() {
	s.cancel()
	slog.Info("coinglass scraper: Chromium process terminated")
}

func (s *CoinglassScraper) FundingRate(ctx context.Context, symbol domain.Symbol) (*domain.FundingRate, error) {
	coin := coinGlassSymbol(symbol)
	url := coinglassBaseURL + "/FundingRate"

	body, err := s.interceptAPI(ctx, url, "/api/futures/funding-rate")
	if err != nil {
		return nil, fmt.Errorf("coinglass scraper: funding rate %s: %w", coin, err)
	}

	return parseFundingRateResponse(body, symbol), nil
}

func (s *CoinglassScraper) OpenInterest(ctx context.Context, symbol domain.Symbol) (*domain.OpenInterest, error) {
	coin := coinGlassSymbol(symbol)
	url := coinglassBaseURL + "/OpenInterest"

	body, err := s.interceptAPI(ctx, url, "/api/futures/open-interest")
	if err != nil {
		return nil, fmt.Errorf("coinglass scraper: open interest %s: %w", coin, err)
	}

	return parseOpenInterestResponse(body, symbol), nil
}

func (s *CoinglassScraper) LiquidationZones(ctx context.Context, symbol domain.Symbol, refPrice decimal.Decimal, pricePct float64) ([]domain.LiquidationZone, error) {
	coin := coinGlassSymbol(symbol)
	url := coinglassBaseURL + "/LiquidationData"

	body, err := s.interceptAPI(ctx, url, "/api/futures/liquidation")
	if err != nil {
		return nil, fmt.Errorf("coinglass scraper: liquidations %s: %w", coin, err)
	}

	return parseLiquidationResponse(body, symbol, refPrice, pricePct), nil
}

func (s *CoinglassScraper) FearGreed(ctx context.Context) (*domain.FearGreedIndex, error) {
	url := coinglassBaseURL + "/FearAndGreedIndex"

	body, err := s.interceptAPI(ctx, url, "/api/index/fear-greed")
	if err != nil {
		return nil, fmt.Errorf("coinglass scraper: fear greed: %w", err)
	}

	return parseFearGreedResponse(body), nil
}

func (s *CoinglassScraper) Snapshot(ctx context.Context, symbol domain.Symbol) (*domain.DerivativesSnapshot, error) {
	snap := &domain.DerivativesSnapshot{
		Symbol:    symbol,
		FetchedAt: time.Now().UTC(),
	}

	if fr, err := s.FundingRate(ctx, symbol); err == nil {
		snap.FundingRate = *fr
	} else {
		slog.Warn("coinglass scraper: snapshot: funding rate failed", "symbol", symbol, "error", err)
	}

	if oi, err := s.OpenInterest(ctx, symbol); err == nil {
		snap.OpenInterest = *oi
	} else {
		slog.Warn("coinglass scraper: snapshot: open interest failed", "symbol", symbol, "error", err)
	}

	if fg, err := s.FearGreed(ctx); err == nil {
		snap.FearGreed = *fg
	} else {
		slog.Warn("coinglass scraper: snapshot: fear greed failed", "error", err)
	}

	if ls, err := s.fetchLongShortRatio(ctx, symbol); err == nil {
		snap.LongShortRatio = *ls
	} else {
		slog.Warn("coinglass scraper: snapshot: long/short ratio failed", "symbol", symbol, "error", err)
	}

	slog.Info("coinglass scraper: snapshot complete",
		"symbol", symbol,
		"funding_rate", snap.FundingRate.Rate,
		"oi_total_usd", snap.OpenInterest.TotalUSD.String(),
		"fear_greed", snap.FearGreed.Value,
		"long_short_ratio", snap.LongShortRatio.GlobalRatio)

	return snap, nil
}

func (s *CoinglassScraper) fetchLongShortRatio(ctx context.Context, symbol domain.Symbol) (*domain.LongShortRatio, error) {
	coin := coinGlassSymbol(symbol)
	url := coinglassBaseURL + "/LongShortRatio"

	body, err := s.interceptAPI(ctx, url, "/api/futures/long-short")
	if err != nil {
		return nil, fmt.Errorf("coinglass scraper: long/short %s: %w", coin, err)
	}

	return parseLongShortResponse(body, symbol), nil
}

// interceptAPI navigates to url and listens for XHR/fetch responses whose URL
// contains apiPathPrefix. It returns the first matching response body as raw JSON.
func (s *CoinglassScraper) interceptAPI(ctx context.Context, pageURL, apiPathPrefix string) (json.RawMessage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	tabCtx, tabCancel := chromedp.NewContext(s.allocCtx)
	defer tabCancel()

	timeout := s.timeout
	if deadline, ok := ctx.Deadline(); ok {
		if d := time.Until(deadline); d < timeout {
			timeout = d
		}
	}
	tabCtx, timeoutCancel := context.WithTimeout(tabCtx, timeout)
	defer timeoutCancel()

	bodyCh := make(chan json.RawMessage, 1)

	chromedp.ListenTarget(tabCtx, func(ev interface{}) {
		respEvt, ok := ev.(*network.EventResponseReceived)
		if !ok {
			return
		}

		respURL := respEvt.Response.URL
		if !strings.Contains(respURL, apiPathPrefix) {
			return
		}

		slog.Debug("coinglass scraper: intercepted API response",
			"url", respURL, "status", respEvt.Response.Status)

		go func(requestID network.RequestID) {
			cdp := network.GetResponseBody(requestID)
			body, err := cdp.Do(tabCtx)
			if err != nil {
				slog.Warn("coinglass scraper: failed to read response body",
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
		chromedp.Navigate(pageURL),
		chromedp.WaitReady("body", chromedp.ByQuery),
		chromedp.Sleep(5*time.Second),
	); err != nil {
		return nil, fmt.Errorf("navigate %s: %w", pageURL, err)
	}

	select {
	case body := <-bodyCh:
		if len(body) == 0 {
			return nil, fmt.Errorf("empty response from %s", apiPathPrefix)
		}
		return body, nil
	case <-tabCtx.Done():
		return nil, fmt.Errorf("timeout waiting for %s API response: %w", apiPathPrefix, tabCtx.Err())
	}
}

func coinGlassSymbol(sym domain.Symbol) string {
	s := string(sym)
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

// parseAPIResponse handles the common CoinGlass API envelope:
// {"success":true,"data":...} or {"code":"0","data":...}
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

func parseFundingRateResponse(body json.RawMessage, symbol domain.Symbol) *domain.FundingRate {
	now := time.Now().UTC()
	result := &domain.FundingRate{Symbol: symbol, FetchedAt: now}

	data, err := parseAPIResponse(body)
	if err != nil {
		slog.Warn("coinglass scraper: parse funding rate envelope", "error", err)
		return result
	}

	// Try array format first: [{"rate":0.0001,...}]
	var arr []struct {
		Rate            float64 `json:"rate"`
		NextFundingTime int64   `json:"nextFundingTime"`
	}
	if err := json.Unmarshal(data, &arr); err == nil && len(arr) > 0 {
		result.Rate = arr[0].Rate
		if arr[0].NextFundingTime > 0 {
			result.NextFundingTime = time.Unix(arr[0].NextFundingTime/1000, 0).UTC()
		}
		return result
	}

	// Try object format: {"rate":0.0001,...}
	var obj struct {
		Rate            float64 `json:"rate"`
		NextFundingTime int64   `json:"nextFundingTime"`
	}
	if err := json.Unmarshal(data, &obj); err == nil {
		result.Rate = obj.Rate
		if obj.NextFundingTime > 0 {
			result.NextFundingTime = time.Unix(obj.NextFundingTime/1000, 0).UTC()
		}
	}

	slog.Info("coinglass scraper: funding rate parsed",
		"symbol", symbol, "rate", result.Rate)
	return result
}

func parseOpenInterestResponse(body json.RawMessage, symbol domain.Symbol) *domain.OpenInterest {
	now := time.Now().UTC()
	result := &domain.OpenInterest{Symbol: symbol, FetchedAt: now}

	data, err := parseAPIResponse(body)
	if err != nil {
		slog.Warn("coinglass scraper: parse open interest envelope", "error", err)
		return result
	}

	var arr []struct {
		OpenInterest float64 `json:"openInterest"`
		Change1h     float64 `json:"change1h"`
		Change4h     float64 `json:"change4h"`
		Change24h    float64 `json:"change24h"`
	}
	if err := json.Unmarshal(data, &arr); err == nil && len(arr) > 0 {
		result.TotalUSD = decimal.NewFromFloat(arr[0].OpenInterest)
		result.Change1h = arr[0].Change1h
		result.Change4h = arr[0].Change4h
		result.Change24h = arr[0].Change24h
	}

	slog.Info("coinglass scraper: open interest parsed",
		"symbol", symbol, "total_usd", result.TotalUSD.String())
	return result
}

func parseFearGreedResponse(body json.RawMessage) *domain.FearGreedIndex {
	now := time.Now().UTC()
	result := &domain.FearGreedIndex{FetchedAt: now}

	data, err := parseAPIResponse(body)
	if err != nil {
		slog.Warn("coinglass scraper: parse fear greed envelope", "error", err)
		return result
	}

	var arr []struct {
		Value          int    `json:"value"`
		Classification string `json:"classification"`
	}
	if err := json.Unmarshal(data, &arr); err == nil && len(arr) > 0 {
		result.Value = arr[0].Value
		result.Category = arr[0].Classification
	}

	if result.Category == "" {
		switch {
		case result.Value >= 75:
			result.Category = "Extreme Greed"
		case result.Value >= 55:
			result.Category = "Greed"
		case result.Value >= 45:
			result.Category = "Neutral"
		case result.Value >= 25:
			result.Category = "Fear"
		default:
			result.Category = "Extreme Fear"
		}
	}

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

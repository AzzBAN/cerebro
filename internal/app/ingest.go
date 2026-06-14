package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"sort"
	"strings"
	"time"

	"github.com/azhar/cerebro/internal/config"
	"github.com/azhar/cerebro/internal/domain"
	"github.com/azhar/cerebro/internal/execution/paper"
	"github.com/azhar/cerebro/internal/ingest/news/cryptopanic"
	"github.com/azhar/cerebro/internal/ingest/scrape"
	"github.com/azhar/cerebro/internal/marketdata"
	"github.com/azhar/cerebro/internal/port"
	"github.com/azhar/cerebro/internal/tui"
	"github.com/shopspring/decimal"
	"golang.org/x/sync/errgroup"
)

// runSyntheticFeeder generates realistic random-walk candles for every
// timeframe listed in each enabled symbol's config. This simulates live market
// data in paper mode without requiring an API connection.
//
// Price model: momentum-biased random walk (85% direction persistence).
// Candles for ALL configured timeframes are emitted at each tick so that
// strategies on 1m, 5m, 15m, and 1h timeframes all receive data.
func runSyntheticFeeder(
	ctx context.Context,
	hub *marketdata.Hub,
	venues []config.VenueConfig,
	interval time.Duration,
	tuiRunner *tui.Runner,
	metrics *runtimeMetrics,
) error {
	feeds := collectAllTimeframeFeeds(venues)
	if len(feeds) == 0 {
		return fmt.Errorf("runSyntheticFeeder: no enabled symbols in markets config")
	}

	slog.Info("synthetic market feeder started",
		"feeds", len(feeds),
		"interval", interval.String())

	type symState struct {
		price     decimal.Decimal
		prevPrice decimal.Decimal
		direction int // +1 (up) or -1 (down)
		volFactor decimal.Decimal
	}

	// Initialise per-symbol state (shared across all timeframes for that symbol).
	stateMap := make(map[domain.Symbol]*symState)
	for _, f := range feeds {
		if _, ok := stateMap[f.symbol]; ok {
			continue
		}
		seedPrice := decimal.NewFromFloat(100 + rand.Float64()*900)
		stateMap[f.symbol] = &symState{
			price:     seedPrice,
			prevPrice: seedPrice,
			direction: 1,
			volFactor: decimal.NewFromFloat(0.003 + rand.Float64()*0.007),
		}
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case ts := <-ticker.C:
			// Advance each symbol's random walk once per tick.
			for sym, st := range stateMap {
				// 15% probability of direction reversal (creates RSI swings).
				if rand.Float64() < 0.15 {
					st.direction = -st.direction
				}
				pctMove := st.volFactor.Mul(decimal.NewFromFloat(0.5 + rand.Float64()))
				delta := st.price.Mul(pctMove)
				if st.direction < 0 {
					delta = delta.Neg()
				}
				newPrice := st.price.Add(delta)
				if newPrice.LessThan(decimal.NewFromFloat(0.01)) {
					newPrice = decimal.NewFromFloat(0.01)
					st.direction = 1
				}

				open := st.price
				closePx := newPrice
				high := open
				low := closePx
				if closePx.GreaterThan(open) {
					high, low = closePx, open
				}
				spread := st.price.Mul(decimal.NewFromFloat(0.0001))
				high = high.Add(st.price.Mul(decimal.NewFromFloat(rand.Float64() * 0.001)))
				low = low.Sub(st.price.Mul(decimal.NewFromFloat(rand.Float64() * 0.001)))

				priceChange := closePx.Sub(st.prevPrice)
				priceChangePct := decimal.Zero
				if !st.prevPrice.IsZero() {
					priceChangePct = priceChange.Div(st.prevPrice).Mul(decimal.NewFromFloat(100))
				}
				vol24h := decimal.NewFromFloat(1e8 + rand.Float64()*2e9)

				hub.PublishQuote(domain.Quote{
					Symbol:             sym,
					Bid:                closePx.Sub(spread),
					Ask:                closePx.Add(spread),
					Mid:                closePx,
					Last:               closePx,
					PriceChange:        priceChange,
					PriceChangePercent: priceChangePct,
					Volume24h:          vol24h,
					Timestamp:          ts.UTC(),
				})

				slog.Debug("quote",
					"symbol", sym,
					"mid", closePx.StringFixed(4),
					"bid", closePx.Sub(spread).StringFixed(4),
					"ask", closePx.Add(spread).StringFixed(4),
				)

				st.prevPrice = st.price
				st.price = newPrice
				stateMap[sym] = st

				// Emit a candle for every configured timeframe.
				for _, f := range feeds {
					if f.symbol != sym {
						continue
					}
					hub.PublishCandle(domain.Candle{
						Symbol:    sym,
						Timeframe: f.timeframe,
						OpenTime:  ts.UTC().Add(-interval),
						CloseTime: ts.UTC(),
						Open:      open,
						High:      high,
						Low:       low,
						Close:     closePx,
						Volume:    decimal.NewFromFloat(10 + rand.Float64()*90),
						Closed:    true,
					})
					metrics.candlesProduced.Add(1)
				}
			}
		}
	}
}

// runFillMonitor drives the paper matcher on every new candle, simulating
// order fills at market price.
func runFillMonitor(
	ctx context.Context,
	hub *marketdata.Hub,
	matcher *paper.Matcher,
	tuiRunner *tui.Runner,
	metrics *runtimeMetrics,
) error {
	_, candles := hub.Subscribe()
	slog.Info("fill monitor started")
	for {
		select {
		case <-ctx.Done():
			return nil
		case evt, ok := <-candles:
			if !ok {
				return nil
			}
			matcher.OnCandle(ctx, evt.Candle)
			metrics.candlesConsumedByFiller.Add(1)
			slog.Debug("fill-monitor candle",
				"symbol", evt.Candle.Symbol,
				"tf", evt.Candle.Timeframe,
				"close", evt.Candle.Close.StringFixed(4),
			)
		}
	}
}

// startIngestRunners launches periodic ingest goroutines for CoinGlass,
// CryptoPanic + FinancialJuice (combined), and Finnhub feeds. tuiRunner is
// optional; when non-nil, macro indicators are pushed to the TUI's Macro
// panel after each CoinGlass cycle and the merged news digest is pushed to
// the News panel after each news cycle.
//
// newsFeed is the CryptoPanic feed (nil when disabled). fjFeed is the
// FinancialJuice feed (nil when disabled). When both are enabled they run
// inside a single news runner: the combined refresher pulls from each in
// turn, dedupes by ID, sorts newest-first, writes the merged stream to
// `news:latest`, and pushes the top 10 to the TUI.
func startIngestRunners(
	ctx context.Context,
	g *errgroup.Group,
	cfg *config.Config,
	cache port.Cache,
	derivFeed port.DerivativesFeed,
	newsFeed port.NewsFeed,
	fjFeed port.NewsFeed,
	calFeed port.CalendarFeed,
	tuiRunner *tui.Runner,
) {
	symbols := collectSymbolList(cfg.Markets)

	if cfg.Ingest.CoinGlass.Enabled && derivFeed != nil {
		interval := durOrDefault(cfg.Ingest.CoinGlass.IntervalMinutes, 30) * time.Minute
		timeout := durOrDefault(cfg.Ingest.CoinGlass.TimeoutSeconds, 10) * time.Second
		runner := scrape.NewRunner("coinglass", interval, timeout, func(ctx context.Context) error {
			snaps := make(map[domain.Symbol]*domain.DerivativesSnapshot, len(symbols))
			for _, sym := range symbols {
				snap, err := derivFeed.Snapshot(ctx, sym)
				if err != nil {
					slog.Warn("coinglass: snapshot failed", "symbol", sym, "error", err)
					continue
				}
				snaps[sym] = snap
				b, _ := json.Marshal(snap)
				_ = cache.Set(ctx, fmt.Sprintf("derivatives:%s", sym), b, interval*2)
				slog.Debug("coinglass: snapshot cached", "symbol", sym)
			}
			if tuiRunner != nil {
				if macro, ok := buildMacroSnapshot(snaps, symbols); ok {
					tuiRunner.SendMacro(macro)
				}
			}
			return nil
		})
		g.Go(func() error { return runner.Run(ctx) })
	}

	// News runner — combines CryptoPanic + FinancialJuice when both are
	// enabled. Picks interval/timeout from the primary (CryptoPanic if on,
	// else FinancialJuice). When both are enabled, FinancialJuice is
	// scraped on the CryptoPanic cadence, which is fine — its RSS endpoint
	// has no published rate limit beyond Cloudflare's generic 429 backoff.
	cpEnabled := cfg.Ingest.CryptoPanic.Enabled && newsFeed != nil
	fjEnabled := cfg.Ingest.FinancialJuice.Enabled && fjFeed != nil
	if cpEnabled || fjEnabled {
		var (
			interval time.Duration
			timeout  time.Duration
			name     string
		)
		switch {
		case cpEnabled && fjEnabled:
			interval = durOrDefault(cfg.Ingest.CryptoPanic.IntervalMinutes, 15) * time.Minute
			timeout = durOrDefault(cfg.Ingest.CryptoPanic.TimeoutSeconds, 15) * time.Second
			name = "news"
		case cpEnabled:
			interval = durOrDefault(cfg.Ingest.CryptoPanic.IntervalMinutes, 15) * time.Minute
			timeout = durOrDefault(cfg.Ingest.CryptoPanic.TimeoutSeconds, 15) * time.Second
			name = "cryptopanic"
		default: // fjEnabled
			interval = durOrDefault(cfg.Ingest.FinancialJuice.IntervalMinutes, 10) * time.Minute
			timeout = durOrDefault(cfg.Ingest.FinancialJuice.TimeoutSeconds, 30) * time.Second
			name = "financialjuice"
		}
		maxItems := cfg.Ingest.CryptoPanic.MaxItems
		if maxItems <= 0 {
			maxItems = 50
		}
		currencies := cfg.Ingest.CryptoPanic.Currencies
		cpFeed := newsFeed
		if !cpEnabled {
			cpFeed = nil
		}
		fjArg := fjFeed
		if !fjEnabled {
			fjArg = nil
		}
		runner := scrape.NewRunner(name, interval, timeout, func(ctx context.Context) error {
			return refreshNewsCache(ctx, cpFeed, fjArg, cache, tuiRunner, currencies, maxItems, interval)
		})
		g.Go(func() error { return runner.Run(ctx) })
	}

	if cfg.Ingest.Finnhub.Enabled && calFeed != nil {
		interval := durOrDefault(cfg.Ingest.Finnhub.IntervalMinutes, 60) * time.Minute
		timeout := durOrDefault(cfg.Ingest.Finnhub.TimeoutSeconds, 15) * time.Second
		runner := scrape.NewRunner("finnhub", interval, timeout, func(ctx context.Context) error {
			events, err := calFeed.UpcomingEvents(ctx, 24)
			if err != nil {
				return err
			}
			b, _ := json.Marshal(events)
			_ = cache.Set(ctx, "calendar:upcoming", b, interval*2)
			slog.Debug("finnhub: calendar cached", "events", len(events))
			return nil
		})
		g.Go(func() error { return runner.Run(ctx) })
	}
}

// buildMacroSnapshot builds a tui.MacroSnapshot from the per-symbol derivative
// snapshots produced by a CoinGlass cycle. It picks the BTC snapshot first
// (preferring USDT-PERP, then USDT spot) so the Macro panel shows the most
// reliable cross-market reading. Returns ok=false when no usable snapshot
// is available.
func buildMacroSnapshot(snaps map[domain.Symbol]*domain.DerivativesSnapshot, symbols []domain.Symbol) (tui.MacroSnapshot, bool) {
	if len(snaps) == 0 {
		return tui.MacroSnapshot{}, false
	}
	pick := pickMacroSymbol(snaps, symbols)
	if pick == nil {
		return tui.MacroSnapshot{}, false
	}
	return tui.MacroSnapshot{
		FearGreed:       pick.FearGreed,
		BTCFundingRate:  pick.FundingRate,
		BTCOpenInterest: pick.OpenInterest,
		BTCLongShort:    pick.LongShortRatio,
		UpdatedAt:       time.Now().UTC(),
	}, true
}

// pickMacroSymbol prefers BTC over other bases, and PERP over spot, since the
// macro panel summarises the leading market. Falls back to the first symbol
// in the configured order if no BTC variant is available.
func pickMacroSymbol(snaps map[domain.Symbol]*domain.DerivativesSnapshot, symbols []domain.Symbol) *domain.DerivativesSnapshot {
	var btcPerp, btcSpot, anyPerp, anyFirst *domain.DerivativesSnapshot
	for _, sym := range symbols {
		s, ok := snaps[sym]
		if !ok {
			continue
		}
		if anyFirst == nil {
			anyFirst = s
		}
		isBTC := containsFold(string(sym), "BTC")
		isPerp := containsFold(string(sym), "PERP")
		switch {
		case isBTC && isPerp && btcPerp == nil:
			btcPerp = s
		case isBTC && !isPerp && btcSpot == nil:
			btcSpot = s
		case isPerp && anyPerp == nil:
			anyPerp = s
		}
	}
	switch {
	case btcPerp != nil:
		return btcPerp
	case btcSpot != nil:
		return btcSpot
	case anyPerp != nil:
		return anyPerp
	default:
		return anyFirst
	}
}

// containsFold is a small case-insensitive substring check that avoids pulling
// in a regex or strings.EqualFold on substrings. ASCII-only.
func containsFold(s, sub string) bool {
	if len(sub) == 0 {
		return true
	}
	if len(sub) > len(s) {
		return false
	}
	lower := func(b byte) byte {
		if b >= 'A' && b <= 'Z' {
			return b + 32
		}
		return b
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		match := true
		for j := 0; j < len(sub); j++ {
			if lower(s[i+j]) != lower(sub[j]) {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

// refreshNewsCache runs one combined news ingest tick. It pulls from the
// CryptoPanic feed (when set) — global stream plus any per-currency feeds
// listed in config — and from the FinancialJuice feed (when set), then
// dedupes the combined result by NewsItem.ID, sorts newest-first, caps to
// maxItems and writes the result to Redis under "news:latest" plus per-asset
// keys "news:by_asset:<CODE>" (CryptoPanic only — FJ has no asset-keyed
// query). When tuiRunner is non-nil the top 10 merged items are pushed to
// the TUI's News panel.
//
// Either feed may be nil; passing both nil is a no-op.
func refreshNewsCache(
	ctx context.Context,
	cpFeed port.NewsFeed,
	fjFeed port.NewsFeed,
	cache port.Cache,
	tuiRunner *tui.Runner,
	currencies []string,
	maxItems int,
	interval time.Duration,
) error {
	if cpFeed == nil && fjFeed == nil {
		return nil
	}

	seen := make(map[string]struct{}, maxItems*2)
	var combined []port.NewsItem
	var cpCount, fjCount int

	appendUnique := func(items []port.NewsItem) int {
		added := 0
		for _, it := range items {
			if it.ID == "" {
				it.ID = it.URL
			}
			if it.ID == "" {
				continue
			}
			if _, dup := seen[it.ID]; dup {
				continue
			}
			seen[it.ID] = struct{}{}
			combined = append(combined, it)
			added++
		}
		return added
	}

	// 1) CryptoPanic global feed + per-currency feeds.
	//
	// When the client is inside a 429 cooldown, the global call returns
	// ErrRateLimited immediately and every per-asset call would do the
	// same. Skip the per-asset loop in that case: the Redis cache (TTL =
	// interval × 3) continues to serve the previous tick's snapshot to
	// the agent and the TUI, and we log a single INFO line instead of
	// spraying WARN for every asset.
	if cpFeed != nil {
		rateLimited := false
		items, err := cpFeed.FetchLatest(ctx, "", maxItems)
		switch {
		case err == nil:
			cpCount += appendUnique(items)
		case errors.Is(err, cryptopanic.ErrRateLimited):
			rateLimited = true
			slog.Info("cryptopanic: rate-limited, skipping tick (cache still warm)", "error", err)
		default:
			slog.Warn("cryptopanic: global fetch failed", "error", err)
		}

		if !rateLimited {
			for _, asset := range currencies {
				asset = strings.ToUpper(strings.TrimSpace(asset))
				if asset == "" {
					continue
				}
				assetItems, err := cpFeed.FetchLatest(ctx, asset, maxItems)
				if err != nil {
					if errors.Is(err, cryptopanic.ErrRateLimited) {
						// Global succeeded but we tripped mid-loop —
						// stop; the rest would only hit the cooldown.
						slog.Info("cryptopanic: rate-limited mid-tick, stopping per-asset fetches", "asset", asset)
						break
					}
					slog.Warn("cryptopanic: asset fetch failed", "asset", asset, "error", err)
					continue
				}
				if b, err := json.Marshal(assetItems); err == nil {
					_ = cache.Set(ctx, "news:by_asset:"+asset, b, interval*3)
				}
				cpCount += appendUnique(assetItems)
			}
		}
	}

	// 2) FinancialJuice — single global RSS pull (no asset-keyed query).
	if fjFeed != nil {
		items, err := fjFeed.FetchLatest(ctx, "", maxItems)
		if err != nil {
			slog.Warn("financialjuice: fetch failed", "error", err)
		} else {
			fjCount += appendUnique(items)
		}
	}

	if len(combined) == 0 {
		slog.Debug("news: tick produced no items")
		return nil
	}

	// 3) Sort newest-first, cap to maxItems.
	sort.Slice(combined, func(i, j int) bool {
		return combined[i].PublishedAt.After(combined[j].PublishedAt)
	})
	if len(combined) > maxItems {
		combined = combined[:maxItems]
	}

	// 4) Cache the merged global list.
	if b, err := json.Marshal(combined); err == nil {
		_ = cache.Set(ctx, "news:latest", b, interval*3)
	}

	// 5) Push a digest to the TUI.
	if tuiRunner != nil {
		top := combined
		if len(top) > 10 {
			top = top[:10]
		}
		tuiRunner.SendNews(tui.NewsSnapshot{
			Items:     top,
			UpdatedAt: time.Now().UTC(),
		})
	}

	slog.Info("news: cache refreshed",
		"items", len(combined),
		"cryptopanic", cpCount,
		"financialjuice", fjCount,
		"assets", len(currencies))
	return nil
}

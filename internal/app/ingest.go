package app

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"sort"
	"time"

	"github.com/azhar/cerebro/internal/config"
	"github.com/azhar/cerebro/internal/domain"
	"github.com/azhar/cerebro/internal/execution/paper"
	"github.com/azhar/cerebro/internal/ingest/scrape"
	"github.com/azhar/cerebro/internal/marketdata"
	"github.com/azhar/cerebro/internal/port"
	"github.com/azhar/cerebro/internal/risk"
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
	uiSink multiSink,
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
	uiSink multiSink,
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
// FinancialJuice, and Finnhub feeds. tuiRunner is optional; when non-nil,
// macro indicators are pushed to the TUI's Macro panel after each CoinGlass
// cycle and the news digest is pushed to the News panel after each news cycle.
//
// fjFeed is the FinancialJuice feed (nil when disabled). When enabled it runs
// inside the news runner: the refresher pulls items, dedupes by ID, sorts
// newest-first, writes the stream to `news:latest`, and pushes the top 10 to
// the TUI.
func startIngestRunners(
	ctx context.Context,
	g *errgroup.Group,
	cfg *config.Config,
	cache port.Cache,
	derivFeed port.DerivativesFeed,
	newsFeed port.NewsFeed,
	fjFeed port.NewsFeed,
	calFeed port.CalendarFeed,
	cal *risk.CalendarBlackout,
	uiSink multiSink,
) {
	symbols := collectSymbolList(cfg.Markets)

	if (cfg.Ingest.CoinGlass.Enabled || cfg.Ingest.CoinGlassScraper.Enabled) && derivFeed != nil {
		interval := durOrDefault(cfg.Ingest.CoinGlass.IntervalMinutes, 30) * time.Minute
		timeout := durOrDefault(cfg.Ingest.CoinGlass.TimeoutSeconds, 10) * time.Second
		if !cfg.Ingest.CoinGlass.Enabled && cfg.Ingest.CoinGlassScraper.Enabled {
			interval = durOrDefault(cfg.Ingest.CoinGlassScraper.IntervalMinutes, 5) * time.Minute
			timeout = durOrDefault(cfg.Ingest.CoinGlassScraper.TimeoutSeconds, 90) * time.Second
		}
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
			if len(uiSink) > 0 {
				if macro, ok := buildMacroSnapshot(snaps, symbols); ok {
					uiSink.SendMacro(macro)
				}
			}
			return nil
		})
		g.Go(func() error { return runner.Run(ctx) })
	}

	// News runner — FinancialJuice RSS feed.
	fjEnabled := cfg.Ingest.FinancialJuice.Enabled && fjFeed != nil
	if fjEnabled {
		interval := durOrDefault(cfg.Ingest.FinancialJuice.IntervalMinutes, 10) * time.Minute
		timeout := durOrDefault(cfg.Ingest.FinancialJuice.TimeoutSeconds, 30) * time.Second
		const maxItems = 50
		runner := scrape.NewRunner("financialjuice", interval, timeout, func(ctx context.Context) error {
			return refreshNewsCache(ctx, fjFeed, cache, uiSink, maxItems, interval)
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
			if cal != nil {
				cal.Update(events)
			}
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

// refreshNewsCache runs one news ingest tick. It pulls from the
// FinancialJuice feed, dedupes by NewsItem.ID, sorts newest-first, caps to
// maxItems and writes the result to Redis under "news:latest". When uiSink is
// non-empty the top 10 items are pushed to the TUI's News panel.
//
// fjFeed may be nil, in which case this is a no-op.
func refreshNewsCache(
	ctx context.Context,
	fjFeed port.NewsFeed,
	cache port.Cache,
	uiSink multiSink,
	maxItems int,
	interval time.Duration,
) error {
	if fjFeed == nil {
		return nil
	}

	seen := make(map[string]struct{}, maxItems*2)
	var combined []port.NewsItem
	var fjCount int

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

	// FinancialJuice — single global RSS pull (no asset-keyed query).
	if items, err := fjFeed.FetchLatest(ctx, "", maxItems); err != nil {
		slog.Warn("financialjuice: fetch failed", "error", err)
	} else {
		fjCount += appendUnique(items)
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
	if len(uiSink) > 0 {
		top := combined
		if len(top) > 10 {
			top = top[:10]
		}
		uiSink.SendNews(tui.NewsSnapshot{
			Items:     top,
			UpdatedAt: time.Now().UTC(),
		})
	}

	slog.Info("news: cache refreshed",
		"items", len(combined),
		"financialjuice", fjCount)
	return nil
}

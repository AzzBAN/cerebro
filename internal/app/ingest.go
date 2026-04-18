package app

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"time"

	"github.com/azhar/cerebro/internal/config"
	"github.com/azhar/cerebro/internal/domain"
	"github.com/azhar/cerebro/internal/execution/paper"
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
// CryptoPanic/FinancialJuice, and Finnhub feeds.
func startIngestRunners(
	ctx context.Context,
	g *errgroup.Group,
	cfg *config.Config,
	cache port.Cache,
	derivFeed port.DerivativesFeed,
	newsFeed port.NewsFeed,
	calFeed port.CalendarFeed,
) {
	symbols := collectSymbolList(cfg.Markets)

	if cfg.Ingest.CoinGlass.Enabled && derivFeed != nil {
		interval := durOrDefault(cfg.Ingest.CoinGlass.IntervalMinutes, 30) * time.Minute
		timeout := durOrDefault(cfg.Ingest.CoinGlass.TimeoutSeconds, 10) * time.Second
		runner := scrape.NewRunner("coinglass", interval, timeout, func(ctx context.Context) error {
			for _, sym := range symbols {
				snap, err := derivFeed.Snapshot(ctx, sym)
				if err != nil {
					slog.Warn("coinglass: snapshot failed", "symbol", sym, "error", err)
					continue
				}
				b, _ := json.Marshal(snap)
				_ = cache.Set(ctx, fmt.Sprintf("derivatives:%s", sym), b, interval*2)
				slog.Debug("coinglass: snapshot cached", "symbol", sym)
			}
			return nil
		})
		g.Go(func() error { return runner.Run(ctx) })
	}

	if cfg.Ingest.CryptoPanic.Enabled && newsFeed != nil {
		interval := durOrDefault(cfg.Ingest.CryptoPanic.IntervalMinutes, 15) * time.Minute
		timeout := durOrDefault(cfg.Ingest.CryptoPanic.TimeoutSeconds, 10) * time.Second
		runner := scrape.NewRunner("cryptopanic", interval, timeout, func(ctx context.Context) error {
			_, err := newsFeed.FetchLatest(ctx, "BTC", 1)
			if err != nil {
				slog.Warn("cryptopanic: ping failed", "error", err)
			}
			return nil
		})
		g.Go(func() error { return runner.Run(ctx) })
	} else if cfg.Ingest.FinancialJuice.Enabled && newsFeed != nil {
		interval := durOrDefault(cfg.Ingest.FinancialJuice.IntervalMinutes, 10) * time.Minute
		timeout := durOrDefault(cfg.Ingest.FinancialJuice.TimeoutSeconds, 30) * time.Second
		runner := scrape.NewRunner("financialjuice", interval, timeout, func(ctx context.Context) error {
			_, err := newsFeed.FetchLatest(ctx, "", 10)
			if err != nil {
					slog.Debug("financialjuice: scrape failed", "error", err)
			}
			return nil
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

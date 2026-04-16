package futures

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand"
	"time"

	gobinancefutures "github.com/adshao/go-binance/v2/futures"
	"github.com/azhar/cerebro/internal/domain"
	"github.com/azhar/cerebro/internal/marketdata"
	"github.com/shopspring/decimal"
)

const (
	reconnectBase   = 500 * time.Millisecond
	reconnectMax    = 30 * time.Second
	alertAfterFails = 5
)

// KlinesWS subscribes to Binance USDT-M Futures kline WebSocket streams and
// publishes closed candles to the Hub. Reconnect loop with exponential backoff.
//
// The WS endpoint is determined by the package-level flags in go-binance/futures:
//   - gobinancefutures.UseTestnet = true  →  wss://stream.binancefuture.com/market/stream?streams=
//   - gobinancefutures.UseDemo    = true  →  wss://fstream.binancefuture.com/market/stream?streams=
//   - both false (default)               →  wss://fstream.binance.com/market/stream?streams=  ← mainnet
//
// The /market/ path is the new path required after the 2026-04-23 legacy URL
// decommission. go-binance v2.8.11 already uses this path. Kline streams are
// fully public — no API key or authentication is required.
type KlinesWS struct {
	hub       *marketdata.Hub
	symbols   []domain.Symbol
	timeframe domain.Timeframe
	notifyFn  func(string)
}

// NewKlinesWS creates a Futures KlinesWS.
func NewKlinesWS(
	hub *marketdata.Hub,
	symbols []domain.Symbol,
	timeframe domain.Timeframe,
	notifyFn func(string),
) *KlinesWS {
	return &KlinesWS{
		hub:       hub,
		symbols:   symbols,
		timeframe: timeframe,
		notifyFn:  notifyFn,
	}
}

// Run starts the WS stream with reconnect loop.
func (k *KlinesWS) Run(ctx context.Context) error {
	failures := 0
	delay := reconnectBase

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if err := k.connect(ctx); err != nil {
			failures++
			slog.Warn("futures klines WS disconnected",
				"venue", "binance_futures", "timeframe", k.timeframe,
				"attempt", failures, "error", err)

			if failures >= alertAfterFails && k.notifyFn != nil {
				go k.notifyFn(fmt.Sprintf(
					"[ALERT] Binance Futures kline WS: %d consecutive failures. Last: %v",
					failures, err))
			}

			jitter := time.Duration(rand.Int63n(int64(100 * time.Millisecond)))
			sleep := delay + jitter
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(sleep):
			}
			if delay*2 < reconnectMax {
				delay *= 2
			} else {
				delay = reconnectMax
			}
			continue
		}
		failures = 0
		delay = reconnectBase
	}
}

func (k *KlinesWS) connect(ctx context.Context) error {
	syms := make([]string, len(k.symbols))
	for i, s := range k.symbols {
		syms[i] = string(s)
	}

	slog.Info("futures klines WS connecting",
		"endpoint", futuresWSEndpoint(),
		"symbols", len(syms),
		"timeframe", k.timeframe,
	)

	symbolIntervalPair := make([][2]string, len(syms))
	for i, s := range syms {
		symbolIntervalPair[i] = [2]string{s, string(k.timeframe)}
	}

	doneC, stopC, err := gobinancefutures.WsCombinedKlineServe(
		buildSymbolIntervalMap(symbolIntervalPair),
		func(event *gobinancefutures.WsKlineEvent) {
			if !event.Kline.IsFinal {
				return
			}
			c, parseErr := klineEventToCandle(event, k.timeframe)
			if parseErr != nil {
				slog.Error("futures kline parse error", "error", parseErr)
				return
			}
			k.hub.PublishCandle(c)
		},
		func(err error) {
			slog.Error("futures kline WS error callback", "error", err)
		},
	)
	if err != nil {
		return fmt.Errorf("futures WsCombinedKlineServe: %w", err)
	}

	select {
	case <-ctx.Done():
		stopC <- struct{}{}
		return ctx.Err()
	case <-doneC:
		return fmt.Errorf("futures WS stream closed by server")
	}
}

// futuresWSEndpoint mirrors the logic inside go-binance's getCombinedMarketEndpoint()
// so we can log the exact URL that will be dialled before connecting.
// WsCombinedKlineServe uses the /market/ path (required post 2026-04-23 legacy decommission).
func futuresWSEndpoint() string {
	switch {
	case gobinancefutures.UseTestnet:
		return gobinancefutures.BaseCombinedMarketTestnetURL
	case gobinancefutures.UseDemo:
		return gobinancefutures.BaseCombinedMarketDemoURL
	default:
		return gobinancefutures.BaseCombinedMarketMainURL
	}
}

func buildSymbolIntervalMap(pairs [][2]string) map[string]string {
	m := make(map[string]string, len(pairs))
	for _, p := range pairs {
		m[p[0]] = p[1]
	}
	return m
}

func klineEventToCandle(e *gobinancefutures.WsKlineEvent, tf domain.Timeframe) (domain.Candle, error) {
	parse := func(s string) (decimal.Decimal, error) {
		d, err := decimal.NewFromString(s)
		if err != nil {
			return decimal.Zero, fmt.Errorf("parse %q: %w", s, err)
		}
		return d, nil
	}
	open, err := parse(e.Kline.Open)
	if err != nil {
		return domain.Candle{}, err
	}
	high, err := parse(e.Kline.High)
	if err != nil {
		return domain.Candle{}, err
	}
	low, err := parse(e.Kline.Low)
	if err != nil {
		return domain.Candle{}, err
	}
	close_, err := parse(e.Kline.Close)
	if err != nil {
		return domain.Candle{}, err
	}
	vol, err := parse(e.Kline.Volume)
	if err != nil {
		return domain.Candle{}, err
	}

	return domain.Candle{
		Symbol:    domain.Symbol(e.Symbol),
		Timeframe: tf,
		OpenTime:  time.Unix(e.Kline.StartTime/1000, 0).UTC(),
		CloseTime: time.Unix(e.Kline.EndTime/1000, 0).UTC(),
		Open:      open,
		High:      high,
		Low:       low,
		Close:     close_,
		Volume:    vol,
		Closed:    e.Kline.IsFinal,
	}, nil
}

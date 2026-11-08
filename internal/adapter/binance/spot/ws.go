package spot

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"sync"
	"time"

	gobinance "github.com/adshao/go-binance/v2"
	"github.com/azhar/cerebro/internal/domain"
	"github.com/azhar/cerebro/internal/marketdata"
)

const (
	reconnectBase   = 500 * time.Millisecond
	reconnectMax    = 30 * time.Second
	alertAfterFails = 5
)

// KlinesWS subscribes to Binance Spot kline (candle) WebSocket streams for the
// given symbols and timeframe, publishing closed candles to the hub.
// It owns the reconnect loop with exponential backoff + jitter.
//
// The WS endpoint is controlled by the package-level flags in go-binance:
//   - gobinance.UseTestnet = true  →  wss://stream.testnet.binance.vision/stream?streams=
//   - gobinance.UseDemo    = true  →  wss://demo-stream.binance.com/stream?streams=
//   - both false (default)        →  wss://stream.binance.com:9443/stream?streams=   ← mainnet
//
// Kline streams are fully public — no API key or client auth is required.
type KlinesWS struct {
	mu          sync.RWMutex
	hub         *marketdata.Hub
	symbols     []domain.Symbol
	timeframe   domain.Timeframe
	notifyFn    func(msg string) // optional; called after alertAfterFails consecutive failures
	reconnectCh chan struct{}
}

// NewKlinesWS creates a Spot KlinesWS.
// notifyFn is called (non-blocking) after alertAfterFails consecutive WS failures.
func NewKlinesWS(
	hub *marketdata.Hub,
	symbols []domain.Symbol,
	timeframe domain.Timeframe,
	notifyFn func(string),
) *KlinesWS {
	return &KlinesWS{
		hub:         hub,
		symbols:     symbols,
		timeframe:   timeframe,
		notifyFn:    notifyFn,
		reconnectCh: make(chan struct{}, 1),
	}
}

// UpdateSymbols updates the symbol list and triggers a reconnection to subscribe
// to the new set of symbols. This allows dynamic subscription management at runtime.
//
// No-op (and no reconnect) if newSymbols is set-equal to the current
// subscription. Without this guard the 5-minute discovery watcher would tear
// down the WS on every poll even when nothing changed, surfacing as a stream
// of "spot klines WS disconnected … error=context canceled" warnings that
// eventually trip the alertAfterFails threshold.
func (k *KlinesWS) UpdateSymbols(newSymbols []domain.Symbol) {
	k.mu.Lock()
	if symbolSetEqual(k.symbols, newSymbols) {
		k.mu.Unlock()
		return
	}
	oldCount := len(k.symbols)
	k.symbols = newSymbols
	k.mu.Unlock()

	slog.Info("updating kline subscription",
		"old_count", oldCount,
		"new_count", len(newSymbols),
		"timeframe", k.timeframe)

	// Signal reconnect (non-blocking)
	select {
	case k.reconnectCh <- struct{}{}:
	default:
		// Reconnect already pending
	}
}

// symbolSetEqual reports whether a and b contain the same symbols, regardless
// of order or duplicates. Cheap for the small lists (≤ a few hundred) carried
// by markets.yaml + discovery candidates.
func symbolSetEqual(a, b []domain.Symbol) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	set := make(map[domain.Symbol]struct{}, len(a))
	for _, s := range a {
		set[s] = struct{}{}
	}
	for _, s := range b {
		if _, ok := set[s]; !ok {
			return false
		}
		delete(set, s)
	}
	return len(set) == 0
}

// getSymbols returns a snapshot of the current symbol list
func (k *KlinesWS) getSymbols() []domain.Symbol {
	k.mu.RLock()
	defer k.mu.RUnlock()
	return append([]domain.Symbol(nil), k.symbols...)
}

// Run starts the WS stream and reconnects on failure until ctx is cancelled.
func (k *KlinesWS) Run(ctx context.Context) error {
	failures := 0
	delay := reconnectBase

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// Create a child context for this connection
		connCtx, connCancel := context.WithCancel(ctx)

		// Watch for reconnection signals. When UpdateSymbols fires we want to
		// tear down the current connection and resubscribe, but we must NOT
		// treat that as a failure for backoff/alert purposes — symbol churn
		// from the 5-minute discovery watcher would otherwise generate a
		// "disconnected" warning per poll and trip the alert threshold.
		reconnectRequested := false
		go func() {
			select {
			case <-k.reconnectCh:
				slog.Info("reconnection requested, closing current connection",
					"venue", "binance_spot", "timeframe", k.timeframe)
				reconnectRequested = true
				connCancel()
			case <-connCtx.Done():
			}
		}()

		err := k.connect(connCtx)
		connCancel()

		if err != nil {
			// Symbol-driven reconnect: not a failure. Don't increment the
			// failure counter, don't back off, don't alert.
			if reconnectRequested && errors.Is(err, context.Canceled) && ctx.Err() == nil {
				failures = 0
				delay = reconnectBase
				continue
			}
			// Parent shutdown — propagate cleanly without a "disconnected" warning.
			if ctx.Err() != nil {
				return ctx.Err()
			}

			failures++
			slog.Warn("spot klines WS disconnected",
				"venue", "binance_spot",
				"timeframe", k.timeframe,
				"attempt", failures,
				"error", err,
			)

			if failures >= alertAfterFails && k.notifyFn != nil {
				go k.notifyFn(fmt.Sprintf(
					"[ALERT] Binance Spot kline WS: %d consecutive failures. Last error: %v",
					failures, err,
				))
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
	symbols := k.getSymbols()
	syms := make([]string, len(symbols))
	for i, s := range symbols {
		syms[i] = domain.ToExchangeSymbol(s)
	}

	slog.Info("spot klines WS connecting",
		"endpoint", spotWSEndpoint(),
		"symbols", len(syms),
		"timeframe", k.timeframe,
	)

	doneC, stopC, err := gobinance.WsCombinedKlineServe(
		buildSymbolIntervalMap(syms, string(k.timeframe)),
		func(event *gobinance.WsKlineEvent) {
			// Keep ticker tape alive in live/demo mode by publishing a quote on
			// every kline update (not only closed candles).
			if q, qErr := klineEventToQuote(event); qErr == nil {
				k.hub.PublishQuote(q)
			}

			if !event.Kline.IsFinal {
				return
			}
			c, parseErr := klineEventToCandle(event, k.timeframe)
			if parseErr != nil {
				slog.Error("spot kline parse error", "error", parseErr)
				return
			}
			k.hub.PublishCandle(c)
		},
		func(err error) {
			slog.Error("spot kline WS error callback", "error", err)
		},
	)
	if err != nil {
		return fmt.Errorf("WsCombinedKlineServe: %w", err)
	}

	select {
	case <-ctx.Done():
		stopC <- struct{}{}
		return ctx.Err()
	case <-doneC:
		return fmt.Errorf("WS stream closed by server")
	}
}

// spotWSEndpoint mirrors the logic inside go-binance's getCombinedEndpoint()
// so we can log the exact URL that will be dialled before connecting.
func spotWSEndpoint() string {
	switch {
	case gobinance.UseTestnet:
		return gobinance.BaseCombinedTestnetURL
	case gobinance.UseDemo:
		return gobinance.BaseCombinedDemoURL
	default:
		return gobinance.BaseCombinedMainURL
	}
}

func buildSymbolIntervalMap(symbols []string, interval string) map[string]string {
	m := make(map[string]string, len(symbols))
	for _, s := range symbols {
		m[s] = interval
	}
	return m
}

func klineEventToCandle(e *gobinance.WsKlineEvent, tf domain.Timeframe) (domain.Candle, error) {
	sym, err := domain.NormalizeExchangeSymbol(e.Symbol, domain.ContractSpot)
	if err != nil {
		return domain.Candle{}, fmt.Errorf("symbol: %w", err)
	}
	open, err := parseDecimal(e.Kline.Open)
	if err != nil {
		return domain.Candle{}, fmt.Errorf("open: %w", err)
	}
	high, err := parseDecimal(e.Kline.High)
	if err != nil {
		return domain.Candle{}, fmt.Errorf("high: %w", err)
	}
	low, err := parseDecimal(e.Kline.Low)
	if err != nil {
		return domain.Candle{}, fmt.Errorf("low: %w", err)
	}
	close_, err := parseDecimal(e.Kline.Close)
	if err != nil {
		return domain.Candle{}, fmt.Errorf("close: %w", err)
	}
	vol, err := parseDecimal(e.Kline.Volume)
	if err != nil {
		return domain.Candle{}, fmt.Errorf("volume: %w", err)
	}

	return domain.Candle{
		Symbol:    sym,
		Timeframe: tf,
		OpenTime:  msToTime(e.Kline.StartTime),
		CloseTime: msToTime(e.Kline.EndTime),
		Open:      open,
		High:      high,
		Low:       low,
		Close:     close_,
		Volume:    vol,
		Closed:    e.Kline.IsFinal,
	}, nil
}

func klineEventToQuote(e *gobinance.WsKlineEvent) (domain.Quote, error) {
	sym, err := domain.NormalizeExchangeSymbol(e.Symbol, domain.ContractSpot)
	if err != nil {
		return domain.Quote{}, fmt.Errorf("quote symbol: %w", err)
	}
	mid, err := parseDecimal(e.Kline.Close)
	if err != nil {
		return domain.Quote{}, fmt.Errorf("quote close: %w", err)
	}
	ts := msToTime(e.Kline.EndTime)
	return domain.Quote{
		Symbol:    sym,
		Bid:       mid,
		Ask:       mid,
		Mid:       mid,
		Last:      mid,
		Timestamp: ts,
	}, nil
}

func msToTime(ms int64) time.Time {
	return time.Unix(ms/1000, (ms%1000)*int64(time.Millisecond)).UTC()
}

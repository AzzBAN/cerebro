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

// BookTickerWS subscribes to Binance USDT-M Futures bookTicker WebSocket
// streams and publishes real-time best bid/ask quotes to the Hub.
type BookTickerWS struct {
	hub      *marketdata.Hub
	symbols  []domain.Symbol
	notifyFn func(string)
}

// NewBookTickerWS creates a Futures BookTickerWS.
func NewBookTickerWS(
	hub *marketdata.Hub,
	symbols []domain.Symbol,
	notifyFn func(string),
) *BookTickerWS {
	return &BookTickerWS{
		hub:      hub,
		symbols:  symbols,
		notifyFn: notifyFn,
	}
}

// Run starts the bookTicker WS stream with reconnect loop.
func (b *BookTickerWS) Run(ctx context.Context) error {
	failures := 0
	delay := reconnectBase

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if err := b.connect(ctx); err != nil {
			failures++
			slog.Warn("futures bookTicker WS disconnected",
				"venue", "binance_futures",
				"attempt", failures,
				"error", err,
			)

			if failures >= alertAfterFails && b.notifyFn != nil {
				go b.notifyFn(fmt.Sprintf(
					"[ALERT] Binance Futures bookTicker WS: %d consecutive failures. Last: %v",
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

func (b *BookTickerWS) connect(ctx context.Context) error {
	syms := make([]string, len(b.symbols))
	for i, s := range b.symbols {
		syms[i] = domain.ToExchangeSymbol(s)
	}

	slog.Info("futures bookTicker WS connecting",
		"symbols", len(syms),
	)

	doneC, stopC, err := gobinancefutures.WsCombinedBookTickerServe(
		syms,
		func(event *gobinancefutures.WsBookTickerEvent) {
			q, qErr := bookTickerToQuote(event)
			if qErr != nil {
				slog.Error("futures bookTicker parse error", "error", qErr)
				return
			}
			b.hub.PublishQuote(q)
		},
		func(err error) {
			slog.Error("futures bookTicker WS error callback", "error", err)
		},
	)
	if err != nil {
		return fmt.Errorf("futures WsCombinedBookTickerServe: %w", err)
	}

	select {
	case <-ctx.Done():
		stopC <- struct{}{}
		return ctx.Err()
	case <-doneC:
		return fmt.Errorf("futures bookTicker WS stream closed by server")
	}
}

var decimalTwo = decimal.NewFromInt(2)

func bookTickerToQuote(e *gobinancefutures.WsBookTickerEvent) (domain.Quote, error) {
	sym, err := domain.NormalizeExchangeSymbol(e.Symbol, domain.ContractFuturesPerp)
	if err != nil {
		return domain.Quote{}, fmt.Errorf("bookTicker symbol: %w", err)
	}
	bid, err := decimal.NewFromString(e.BestBidPrice)
	if err != nil {
		return domain.Quote{}, fmt.Errorf("bookTicker bid: %w", err)
	}
	ask, err := decimal.NewFromString(e.BestAskPrice)
	if err != nil {
		return domain.Quote{}, fmt.Errorf("bookTicker ask: %w", err)
	}
	mid := bid.Add(ask).Div(decimalTwo)
	return domain.Quote{
		Symbol: sym,
		Bid:    bid,
		Ask:    ask,
		Mid:    mid,
	}, nil
}

package spot

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand"
	"time"

	gobinance "github.com/adshao/go-binance/v2"
	"github.com/azhar/cerebro/internal/domain"
	"github.com/azhar/cerebro/internal/marketdata"
)

// BookTickerWS subscribes to Binance Spot bookTicker WebSocket streams and
// publishes real-time best bid/ask quotes to the Hub.
type BookTickerWS struct {
	hub      *marketdata.Hub
	symbols  []domain.Symbol
	notifyFn func(string)
}

// NewBookTickerWS creates a Spot BookTickerWS.
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

// Run starts the bookTicker WS stream with reconnect loop until ctx is cancelled.
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
			slog.Warn("spot bookTicker WS disconnected",
				"venue", "binance_spot",
				"attempt", failures,
				"error", err,
			)

			if failures >= alertAfterFails && b.notifyFn != nil {
				go b.notifyFn(fmt.Sprintf(
					"[ALERT] Binance Spot bookTicker WS: %d consecutive failures. Last error: %v",
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

	slog.Info("spot bookTicker WS connecting",
		"symbols", len(syms),
	)

	doneC, stopC, err := gobinance.WsCombinedBookTickerServe(
		syms,
		func(event *gobinance.WsBookTickerEvent) {
			q, qErr := bookTickerToQuote(event)
			if qErr != nil {
				slog.Error("spot bookTicker parse error", "error", qErr)
				return
			}
			b.hub.PublishQuote(q)
		},
		func(err error) {
			slog.Error("spot bookTicker WS error callback", "error", err)
		},
	)
	if err != nil {
		return fmt.Errorf("WsCombinedBookTickerServe: %w", err)
	}

	select {
	case <-ctx.Done():
		stopC <- struct{}{}
		return ctx.Err()
	case <-doneC:
		return fmt.Errorf("bookTicker WS stream closed by server")
	}
}

func bookTickerToQuote(e *gobinance.WsBookTickerEvent) (domain.Quote, error) {
	sym, err := domain.NormalizeExchangeSymbol(e.Symbol, domain.ContractSpot)
	if err != nil {
		return domain.Quote{}, fmt.Errorf("bookTicker symbol: %w", err)
	}
	bid, err := parseDecimal(e.BestBidPrice)
	if err != nil {
		return domain.Quote{}, fmt.Errorf("bookTicker bid: %w", err)
	}
	ask, err := parseDecimal(e.BestAskPrice)
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

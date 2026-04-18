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

// TickerWS subscribes to Binance USDT-M Futures 24hr ticker WebSocket streams
// and publishes price change, change %, and volume data to the Hub.
type TickerWS struct {
	hub      *marketdata.Hub
	symbols  []domain.Symbol
	notifyFn func(string)
}

// NewTickerWS creates a Futures TickerWS.
func NewTickerWS(
	hub *marketdata.Hub,
	symbols []domain.Symbol,
	notifyFn func(string),
) *TickerWS {
	return &TickerWS{
		hub:      hub,
		symbols:  symbols,
		notifyFn: notifyFn,
	}
}

// Run starts the 24hr ticker WS stream with reconnect loop.
func (t *TickerWS) Run(ctx context.Context) error {
	failures := 0
	delay := reconnectBase

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if err := t.connect(ctx); err != nil {
			failures++
			slog.Warn("futures 24hr ticker WS disconnected",
				"venue", "binance_futures",
				"attempt", failures,
				"error", err,
			)

			if failures >= alertAfterFails && t.notifyFn != nil {
				go t.notifyFn(fmt.Sprintf(
					"[ALERT] Binance Futures 24hr ticker WS: %d consecutive failures. Last: %v",
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

func (t *TickerWS) connect(ctx context.Context) error {
	wanted := make(map[string]bool, len(t.symbols))
	for _, s := range t.symbols {
		wanted[domain.ToExchangeSymbol(s)] = true
	}

	slog.Info("futures 24hr ticker WS connecting",
		"symbols", len(wanted),
	)

	doneC, stopC, err := gobinancefutures.WsAllMarketTickerServe(
		func(events gobinancefutures.WsAllMarketTickerEvent) {
			for _, event := range events {
				if !wanted[event.Symbol] {
					continue
				}
				q, qErr := marketTickerToQuote(event)
				if qErr != nil {
					slog.Error("futures 24hr ticker parse error", "error", qErr)
					continue
				}
				t.hub.PublishQuote(q)
			}
		},
		func(err error) {
			slog.Error("futures 24hr ticker WS error callback", "error", err)
		},
	)
	if err != nil {
		return fmt.Errorf("futures WsAllMarketTickerServe: %w", err)
	}

	select {
	case <-ctx.Done():
		stopC <- struct{}{}
		return ctx.Err()
	case <-doneC:
		return fmt.Errorf("futures 24hr ticker WS stream closed by server")
	}
}

func marketTickerToQuote(e *gobinancefutures.WsMarketTickerEvent) (domain.Quote, error) {
	sym, err := domain.NormalizeExchangeSymbol(e.Symbol, domain.ContractFuturesPerp)
	if err != nil {
		return domain.Quote{}, fmt.Errorf("ticker symbol: %w", err)
	}
	last, err := decimal.NewFromString(e.ClosePrice)
	if err != nil {
		return domain.Quote{}, fmt.Errorf("ticker last: %w", err)
	}
	chg, err := decimal.NewFromString(e.PriceChange)
	if err != nil {
		return domain.Quote{}, fmt.Errorf("ticker change: %w", err)
	}
	chgPct, err := decimal.NewFromString(e.PriceChangePercent)
	if err != nil {
		return domain.Quote{}, fmt.Errorf("ticker change%%: %w", err)
	}
	vol, err := decimal.NewFromString(e.QuoteVolume)
	if err != nil {
		return domain.Quote{}, fmt.Errorf("ticker volume: %w", err)
	}
	return domain.Quote{
		Symbol:             sym,
		Last:               last,
		PriceChange:        chg,
		PriceChangePercent: chgPct,
		Volume24h:          vol,
	}, nil
}

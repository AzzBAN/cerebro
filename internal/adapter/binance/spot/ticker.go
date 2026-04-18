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

// TickerWS subscribes to Binance Spot 24hr ticker WebSocket streams and
// publishes price change, change %, and volume data to the Hub.
type TickerWS struct {
	hub      *marketdata.Hub
	symbols  []domain.Symbol
	notifyFn func(string)
}

// NewTickerWS creates a Spot TickerWS.
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

// Run starts the 24hr ticker WS stream with reconnect loop until ctx is cancelled.
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
			slog.Warn("spot 24hr ticker WS disconnected",
				"venue", "binance_spot",
				"attempt", failures,
				"error", err,
			)

			if failures >= alertAfterFails && t.notifyFn != nil {
				go t.notifyFn(fmt.Sprintf(
					"[ALERT] Binance Spot 24hr ticker WS: %d consecutive failures. Last error: %v",
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
	syms := make([]string, len(t.symbols))
	for i, s := range t.symbols {
		syms[i] = domain.ToExchangeSymbol(s)
	}

	slog.Info("spot 24hr ticker WS connecting",
		"symbols", len(syms),
	)

	doneC, stopC, err := gobinance.WsCombinedMarketStatServe(
		syms,
		func(event *gobinance.WsMarketStatEvent) {
			q, qErr := marketStatToQuote(event)
			if qErr != nil {
				slog.Error("spot 24hr ticker parse error", "error", qErr)
				return
			}
			t.hub.PublishQuote(q)
		},
		func(err error) {
			slog.Error("spot 24hr ticker WS error callback", "error", err)
		},
	)
	if err != nil {
		return fmt.Errorf("WsCombinedMarketStatServe: %w", err)
	}

	select {
	case <-ctx.Done():
		stopC <- struct{}{}
		return ctx.Err()
	case <-doneC:
		return fmt.Errorf("24hr ticker WS stream closed by server")
	}
}

func marketStatToQuote(e *gobinance.WsMarketStatEvent) (domain.Quote, error) {
	sym, err := domain.NormalizeExchangeSymbol(e.Symbol, domain.ContractSpot)
	if err != nil {
		return domain.Quote{}, fmt.Errorf("ticker symbol: %w", err)
	}
	last, err := parseDecimal(e.LastPrice)
	if err != nil {
		return domain.Quote{}, fmt.Errorf("ticker last: %w", err)
	}
	chg, err := parseDecimal(e.PriceChange)
	if err != nil {
		return domain.Quote{}, fmt.Errorf("ticker change: %w", err)
	}
	chgPct, err := parseDecimal(e.PriceChangePercent)
	if err != nil {
		return domain.Quote{}, fmt.Errorf("ticker change%: %w", err)
	}
	vol, err := parseDecimal(e.QuoteVolume)
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

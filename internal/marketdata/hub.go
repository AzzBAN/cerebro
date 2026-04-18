package marketdata

import (
	"context"
	"log/slog"
	"sync"

	"github.com/azhar/cerebro/internal/domain"
)

const (
	defaultChanBuffer = 64
)

// QuoteEvent carries the latest quote for a symbol.
type QuoteEvent struct {
	Quote domain.Quote
}

// CandleEvent carries a newly closed candle.
type CandleEvent struct {
	Candle domain.Candle
}

// subscriber holds a pair of channels for a single consumer.
type subscriber struct {
	quotes  chan QuoteEvent
	candles chan CandleEvent
}

// Hub is the central market-data fan-out point.
// WS connector goroutines push normalised events in; strategy goroutines and
// the TUI read from their own buffered subscriber channels.
// A slow subscriber never blocks the WS reader — its channel drops the oldest
// item when full (drop-oldest backpressure).
//
// Quotes arrive from multiple WS streams (bookTicker, 24hr ticker). The Hub
// merges partial updates into a per-symbol accumulated state before fanning
// out so subscribers always see the full picture.
type Hub struct {
	mu            sync.RWMutex
	subscribers   []*subscriber
	latestQuotes  map[string]domain.Quote
	quotesMu      sync.Mutex
}

// NewHub creates an empty Hub.
func NewHub() *Hub {
	return &Hub{latestQuotes: make(map[string]domain.Quote)}
}

// Subscribe registers a consumer and returns read-only channels for quotes and candles.
// The returned channels are closed when the Hub is closed.
func (h *Hub) Subscribe() (<-chan QuoteEvent, <-chan CandleEvent) {
	s := &subscriber{
		quotes:  make(chan QuoteEvent, defaultChanBuffer),
		candles: make(chan CandleEvent, defaultChanBuffer),
	}
	h.mu.Lock()
	h.subscribers = append(h.subscribers, s)
	h.mu.Unlock()
	return s.quotes, s.candles
}

// PublishQuote merges the incoming quote into the per-symbol accumulated state,
// then fans out the merged QuoteEvent to all subscribers.
// Non-blocking: if a subscriber's buffer is full, the oldest item is dropped.
func (h *Hub) PublishQuote(q domain.Quote) {
	key := string(q.Symbol)
	h.quotesMu.Lock()
	existing := h.latestQuotes[key]
	merged := mergeQuote(existing, q)
	h.latestQuotes[key] = merged
	h.quotesMu.Unlock()

	evt := QuoteEvent{Quote: merged}
	h.mu.RLock()
	defer h.mu.RUnlock()
	for _, s := range h.subscribers {
		select {
		case s.quotes <- evt:
		default:
			// drain one and retry — drop-oldest semantics
			select {
			case <-s.quotes:
			default:
			}
			select {
			case s.quotes <- evt:
			default:
			}
		}
	}
}

// mergeQuote overlays non-zero fields from incoming onto base.
func mergeQuote(base, inc domain.Quote) domain.Quote {
	if !inc.Bid.IsZero() {
		base.Bid = inc.Bid
	}
	if !inc.Ask.IsZero() {
		base.Ask = inc.Ask
	}
	if !inc.Mid.IsZero() {
		base.Mid = inc.Mid
	}
	if !inc.Last.IsZero() {
		base.Last = inc.Last
	}
	if !inc.PriceChange.IsZero() {
		base.PriceChange = inc.PriceChange
	}
	if !inc.PriceChangePercent.IsZero() {
		base.PriceChangePercent = inc.PriceChangePercent
	}
	if !inc.Volume24h.IsZero() {
		base.Volume24h = inc.Volume24h
	}
	if !inc.Timestamp.IsZero() {
		base.Timestamp = inc.Timestamp
	}
	base.Symbol = inc.Symbol
	return base
}

// PublishCandle fans out a CandleEvent to all subscribers.
// Non-blocking with drop-oldest backpressure.
func (h *Hub) PublishCandle(c domain.Candle) {
	evt := CandleEvent{Candle: c}
	h.mu.RLock()
	defer h.mu.RUnlock()
	for _, s := range h.subscribers {
		select {
		case s.candles <- evt:
		default:
			select {
			case <-s.candles:
			default:
			}
			select {
			case s.candles <- evt:
			default:
			}
		}
	}
}

// LatestQuote returns the most recent merged quote for a symbol.
// Returns false if no quote has been received for that symbol.
func (h *Hub) LatestQuote(symbol domain.Symbol) (domain.Quote, bool) {
	h.quotesMu.Lock()
	q, ok := h.latestQuotes[string(symbol)]
	h.quotesMu.Unlock()
	return q, ok
}

// Close closes all subscriber channels, signalling consumers to stop.
func (h *Hub) Close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, s := range h.subscribers {
		close(s.quotes)
		close(s.candles)
	}
	h.subscribers = nil
}

// Replay drives a deterministic sequence of candles into the hub for backtesting.
// It blocks until all candles are published or ctx is cancelled.
func (h *Hub) Replay(ctx context.Context, candles []domain.Candle) {
	for _, c := range candles {
		select {
		case <-ctx.Done():
			return
		default:
			h.PublishCandle(c)
		}
	}
	slog.Debug("replay complete", "candles", len(candles))
}

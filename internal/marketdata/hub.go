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
type Hub struct {
	mu          sync.RWMutex
	subscribers []*subscriber
}

// NewHub creates an empty Hub.
func NewHub() *Hub {
	return &Hub{}
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

// PublishQuote fans out a QuoteEvent to all subscribers.
// Non-blocking: if a subscriber's buffer is full, the oldest item is dropped.
func (h *Hub) PublishQuote(q domain.Quote) {
	evt := QuoteEvent{Quote: q}
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

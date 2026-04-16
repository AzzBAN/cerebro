package execution

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/azhar/cerebro/internal/domain"
)

const workerChanBuffer = 32

// Router routes OrderIntents to the correct venue worker channel.
// One channel per broker — this preserves the single-writer invariant.
type Router struct {
	channels map[domain.Venue]chan OrderRequest
}

// NewRouter creates a Router. For each venue, caller must also create a Worker
// reading from the returned channel (via Channel).
func NewRouter(venues []domain.Venue) *Router {
	chs := make(map[domain.Venue]chan OrderRequest, len(venues))
	for _, v := range venues {
		chs[v] = make(chan OrderRequest, workerChanBuffer)
	}
	return &Router{channels: chs}
}

// Channel returns the input channel for a given venue (used to wire Workers).
func (r *Router) Channel(venue domain.Venue) (<-chan OrderRequest, bool) {
	ch, ok := r.channels[venue]
	if !ok {
		return nil, false
	}
	return ch, true
}

// Route sends an OrderIntent to the appropriate venue worker.
// The venue is determined by looking up the intent's symbol in the configured
// symbol-to-venue map. Defaults to BinanceSpot if not found.
// If the worker channel is full, it returns an error (back-pressure).
func (r *Router) Route(ctx context.Context, intent domain.OrderIntent, venue domain.Venue) (OrderResponse, error) {
	if venue == "" {
		venue = domain.VenueBinanceSpot
	}
	ch, ok := r.channels[venue]
	if !ok {
		return OrderResponse{}, fmt.Errorf("no worker registered for venue %q", venue)
	}

	respCh := make(chan OrderResponse, 1)
	req := OrderRequest{Intent: intent, RespCh: respCh}

	select {
	case ch <- req:
	case <-ctx.Done():
		return OrderResponse{}, ctx.Err()
	default:
		slog.Warn("execution worker channel full; order dropped",
			"symbol", intent.Symbol, "side", intent.Side)
		return OrderResponse{}, fmt.Errorf("execution worker channel full")
	}

	select {
	case resp := <-respCh:
		return resp, resp.Err
	case <-ctx.Done():
		return OrderResponse{}, ctx.Err()
	}
}

// Close shuts down all worker channels.
func (r *Router) Close() {
	for _, ch := range r.channels {
		close(ch)
	}
}

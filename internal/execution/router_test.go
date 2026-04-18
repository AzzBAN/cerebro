package execution

import (
	"context"
	"testing"

	"github.com/azhar/cerebro/internal/domain"
)

func TestRouter_RouteSuccess(t *testing.T) {
	venues := []domain.Venue{domain.VenueBinanceSpot}
	r := NewRouter(venues)
	defer r.Close()

	ch, ok := r.Channel(domain.VenueBinanceSpot)
	if !ok {
		t.Fatal("expected channel for binance_spot")
	}

	intent := domain.OrderIntent{ID: "test-1", Symbol: "BTC/USDT", Side: domain.SideBuy}

	// Start a fake worker that reads from the channel.
	go func() {
		req := <-ch
		req.RespCh <- OrderResponse{BrokerOrderID: "broker-1"}
	}()

	resp, err := r.Route(context.Background(), intent, domain.VenueBinanceSpot)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.BrokerOrderID != "broker-1" {
		t.Errorf("got broker ID %q, want %q", resp.BrokerOrderID, "broker-1")
	}
}

func TestRouter_UnknownVenue(t *testing.T) {
	r := NewRouter([]domain.Venue{domain.VenueBinanceSpot})
	defer r.Close()

	_, err := r.Route(context.Background(), domain.OrderIntent{}, domain.VenueBinanceFutures)
	if err == nil {
		t.Fatal("expected error for unknown venue")
	}
}

func TestRouter_DefaultVenue(t *testing.T) {
	r := NewRouter([]domain.Venue{domain.VenueBinanceSpot})
	defer r.Close()

	ch, _ := r.Channel(domain.VenueBinanceSpot)
	go func() {
		req := <-ch
		req.RespCh <- OrderResponse{BrokerOrderID: "ok"}
	}()

	// Empty venue should default to binance_spot.
	resp, err := r.Route(context.Background(), domain.OrderIntent{ID: "x"}, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.BrokerOrderID != "ok" {
		t.Errorf("got %q", resp.BrokerOrderID)
	}
}

func TestRouter_ChannelNotFound(t *testing.T) {
	r := NewRouter(nil)
	_, ok := r.Channel(domain.VenueBinanceSpot)
	if ok {
		t.Error("expected no channel for empty router")
	}
}

func TestRouter_CancelledContext(t *testing.T) {
	r := NewRouter([]domain.Venue{domain.VenueBinanceSpot})
	defer r.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := r.Route(ctx, domain.OrderIntent{ID: "x"}, domain.VenueBinanceSpot)
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
}

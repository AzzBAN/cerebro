package positionproposal

import (
	"context"
	"testing"

	"github.com/azhar/cerebro/internal/domain"
	"github.com/shopspring/decimal"
)

type fakeAdjuster struct {
	cancelled  []domain.BracketResponse
	placed     []domain.BracketRequest
	protective map[domain.Symbol]domain.BracketResponse
	recorded   map[domain.Symbol]domain.BracketResponse
}

func (f *fakeAdjuster) PlaceBracket(_ context.Context, req domain.BracketRequest) (domain.BracketResponse, error) {
	f.placed = append(f.placed, req)
	return domain.BracketResponse{Symbol: req.Symbol, StopOrderID: "new-stop"}, nil
}

func (f *fakeAdjuster) CancelBracket(_ context.Context, resp domain.BracketResponse) error {
	f.cancelled = append(f.cancelled, resp)
	return nil
}

func (f *fakeAdjuster) ProtectiveBracket(sym domain.Symbol) (domain.BracketResponse, bool) {
	r, ok := f.protective[sym]
	return r, ok
}

func (f *fakeAdjuster) Record(sym domain.Symbol, resp domain.BracketResponse) {
	if f.recorded == nil {
		f.recorded = map[domain.Symbol]domain.BracketResponse{}
	}
	f.recorded[sym] = resp
}

func TestApplyAdjustment_CancelsThenPlacesAndRecords(t *testing.T) {
	fa := &fakeAdjuster{protective: map[domain.Symbol]domain.BracketResponse{
		"BTC/USDT-PERP": {Symbol: "BTC/USDT-PERP", StopOrderID: "old-stop", TakeProfitOrderID: "old-tp"},
	}}
	apply := ApplyAdjustment(fa, fa, fa)
	p := Proposal{
		Symbol: "BTC/USDT-PERP", Venue: domain.VenueBinanceFutures, Side: domain.SideBuy,
		ProposedStop: decimal.NewFromInt(61000), ProposedTP: decimal.NewFromInt(72000),
	}
	if err := apply(context.Background(), p); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(fa.cancelled) != 1 || fa.cancelled[0].StopOrderID != "old-stop" {
		t.Fatalf("expected old protection cancelled, got %+v", fa.cancelled)
	}
	if len(fa.placed) != 1 || !fa.placed[0].StopLoss.Equal(decimal.NewFromInt(61000)) {
		t.Fatalf("expected new bracket at proposed stop, got %+v", fa.placed)
	}
	if _, ok := fa.recorded["BTC/USDT-PERP"]; !ok {
		t.Fatal("expected new bracket recorded in tracker")
	}
}

func TestApplyAdjustment_NoExistingProtection_StillPlaces(t *testing.T) {
	fa := &fakeAdjuster{protective: map[domain.Symbol]domain.BracketResponse{}}
	apply := ApplyAdjustment(fa, fa, fa)
	p := Proposal{
		Symbol: "ETH/USDT", Venue: domain.VenueBinanceSpot, Side: domain.SideBuy,
		ProposedStop: decimal.NewFromInt(2800), ProposedTP: decimal.NewFromInt(3500),
	}
	if err := apply(context.Background(), p); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(fa.cancelled) != 0 {
		t.Fatalf("nothing to cancel, got %+v", fa.cancelled)
	}
	if len(fa.placed) != 1 {
		t.Fatalf("expected bracket placed, got %d", len(fa.placed))
	}
}

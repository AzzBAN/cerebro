package paper

import (
	"context"
	"testing"
	"time"

	"github.com/azhar/cerebro/internal/domain"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// bracketStubStore is the bare minimum TradeStore used by the bracket tests.
// It records every trade saved so assertions can inspect the exit fill.
type bracketStubStore struct {
	trades []domain.Trade
}

func (s *bracketStubStore) SaveIntent(_ context.Context, _ domain.OrderIntent) error {
	return nil
}
func (s *bracketStubStore) UpdateIntentStatus(_ context.Context, _ string, _ domain.OrderStatus, _ string) error {
	return nil
}
func (s *bracketStubStore) SaveTrade(_ context.Context, t domain.Trade) error {
	s.trades = append(s.trades, t)
	return nil
}
func (s *bracketStubStore) TradesByWindow(_ context.Context, _, _ time.Time) ([]domain.Trade, error) {
	return s.trades, nil
}

func bdec(s string) decimal.Decimal {
	d, err := decimal.NewFromString(s)
	if err != nil {
		panic(err)
	}
	return d
}

// fillEntry places, fills, and brackets a long position. Returns the
// intent ID used so callers can assert on the resulting trade.
func fillEntry(t *testing.T, b *Book, m *Matcher, symbol domain.Symbol, side domain.Side, sl, tp decimal.Decimal) string {
	t.Helper()
	id := uuid.New().String()
	intent := domain.OrderIntent{
		ID:          id,
		Symbol:      symbol,
		Venue:       domain.VenueBinanceSpot,
		Side:        side,
		Quantity:    bdec("1"),
		StopLoss:    sl,
		TakeProfit1: tp,
		CreatedAt:   time.Now().UTC(),
	}
	if _, err := m.PlaceOrder(context.Background(), intent); err != nil {
		t.Fatalf("PlaceOrder: %v", err)
	}
	// Open bar: entry fills at this candle's Open.
	openTime := time.Now().UTC()
	m.OnCandle(context.Background(), domain.Candle{
		Symbol: symbol, Timeframe: domain.TF1m,
		OpenTime: openTime, CloseTime: openTime.Add(time.Minute),
		Open: bdec("100"), High: bdec("101"), Low: bdec("99"), Close: bdec("100"),
		Closed: true,
	})
	if _, err := m.PlaceBracket(context.Background(), domain.BracketRequest{
		ParentIntentID: id,
		Symbol:         symbol,
		Venue:          domain.VenueBinanceSpot,
		Side:           side,
		Quantity:       bdec("1"),
		StopLoss:       sl,
		TakeProfit:     tp,
	}); err != nil {
		t.Fatalf("PlaceBracket: %v", err)
	}
	return id
}

func TestPaperBracket_LongStopTriggers(t *testing.T) {
	b := NewBook()
	store := &bracketStubStore{}
	m := NewMatcher(b, store, 0) // zero commission

	symbol := domain.Symbol("BTC/USDT")
	fillEntry(t, b, m, symbol, domain.SideBuy, bdec("95"), bdec("110"))

	// Next candle: low touches the stop at 95 → exit at 95.
	m.OnCandle(context.Background(), domain.Candle{
		Symbol: symbol, Timeframe: domain.TF1m,
		OpenTime: time.Now().Add(time.Minute), CloseTime: time.Now().Add(2 * time.Minute),
		Open: bdec("100"), High: bdec("101"), Low: bdec("94"), Close: bdec("96"),
		Closed: true,
	})

	if len(store.trades) != 2 {
		t.Fatalf("expected 2 trades (entry + exit); got %d", len(store.trades))
	}
	exit := store.trades[1]
	if exit.Side != domain.SideSell {
		t.Errorf("exit side = %s, want sell", exit.Side)
	}
	if !exit.FillPrice.Equal(bdec("95")) {
		t.Errorf("exit fill = %s, want 95 (stop price)", exit.FillPrice)
	}
	// Bracket must be gone after firing.
	if len(b.Brackets()) != 0 {
		t.Errorf("expected zero brackets after trigger, got %d", len(b.Brackets()))
	}
	// Position must be gone after firing.
	if len(b.Positions()) != 0 {
		t.Errorf("expected zero positions after trigger, got %d", len(b.Positions()))
	}
}

func TestPaperBracket_LongTPTriggers(t *testing.T) {
	b := NewBook()
	store := &bracketStubStore{}
	m := NewMatcher(b, store, 0)

	symbol := domain.Symbol("BTC/USDT")
	fillEntry(t, b, m, symbol, domain.SideBuy, bdec("95"), bdec("110"))

	// Next candle: high touches TP at 110 without breaching the stop.
	// Stop takes priority only when both trigger; here only TP triggers.
	m.OnCandle(context.Background(), domain.Candle{
		Symbol: symbol, Timeframe: domain.TF1m,
		OpenTime: time.Now().Add(time.Minute), CloseTime: time.Now().Add(2 * time.Minute),
		Open: bdec("101"), High: bdec("111"), Low: bdec("100.5"), Close: bdec("109"),
		Closed: true,
	})

	if len(store.trades) != 2 {
		t.Fatalf("expected 2 trades; got %d", len(store.trades))
	}
	exit := store.trades[1]
	if !exit.FillPrice.Equal(bdec("110")) {
		t.Errorf("exit fill = %s, want 110 (tp price)", exit.FillPrice)
	}
}

func TestPaperBracket_ShortStopTriggers(t *testing.T) {
	b := NewBook()
	store := &bracketStubStore{}
	m := NewMatcher(b, store, 0)

	symbol := domain.Symbol("BTC/USDT")
	fillEntry(t, b, m, symbol, domain.SideSell, bdec("105"), bdec("90"))

	// Short entry opened at OpenPrice 100. Next candle spikes high to 106
	// which breaches the 105 stop → close at 105.
	m.OnCandle(context.Background(), domain.Candle{
		Symbol: symbol, Timeframe: domain.TF1m,
		OpenTime: time.Now().Add(time.Minute), CloseTime: time.Now().Add(2 * time.Minute),
		Open: bdec("100"), High: bdec("106"), Low: bdec("99"), Close: bdec("104"),
		Closed: true,
	})

	exit := store.trades[1]
	if exit.Side != domain.SideBuy {
		t.Errorf("short exit side = %s, want buy", exit.Side)
	}
	if !exit.FillPrice.Equal(bdec("105")) {
		t.Errorf("short exit fill = %s, want 105 (stop)", exit.FillPrice)
	}
}

func TestPaperBracket_StopWinsOverTP_WhenBothCrossed(t *testing.T) {
	b := NewBook()
	store := &bracketStubStore{}
	m := NewMatcher(b, store, 0)

	symbol := domain.Symbol("BTC/USDT")
	fillEntry(t, b, m, symbol, domain.SideBuy, bdec("95"), bdec("110"))

	// Wide candle straddles both SL and TP. Stop must win.
	m.OnCandle(context.Background(), domain.Candle{
		Symbol: symbol, Timeframe: domain.TF1m,
		OpenTime: time.Now().Add(time.Minute), CloseTime: time.Now().Add(2 * time.Minute),
		Open: bdec("100"), High: bdec("115"), Low: bdec("90"), Close: bdec("105"),
		Closed: true,
	})

	exit := store.trades[1]
	if !exit.FillPrice.Equal(bdec("95")) {
		t.Errorf("expected stop to win (95); got %s", exit.FillPrice)
	}
}

func TestPaperBracket_Cancel(t *testing.T) {
	b := NewBook()
	store := &bracketStubStore{}
	m := NewMatcher(b, store, 0)

	symbol := domain.Symbol("BTC/USDT")
	id := fillEntry(t, b, m, symbol, domain.SideBuy, bdec("95"), bdec("110"))

	if err := m.CancelBracket(context.Background(), domain.BracketResponse{
		ListID: "paper-br-" + id,
		Symbol: symbol,
	}); err != nil {
		t.Fatalf("CancelBracket: %v", err)
	}

	// A subsequent candle that would have triggered must no-op.
	m.OnCandle(context.Background(), domain.Candle{
		Symbol: symbol, Timeframe: domain.TF1m,
		OpenTime: time.Now().Add(time.Minute), CloseTime: time.Now().Add(2 * time.Minute),
		Open: bdec("100"), High: bdec("115"), Low: bdec("80"), Close: bdec("90"),
		Closed: true,
	})

	// Only the entry trade should have been saved.
	if len(store.trades) != 1 {
		t.Errorf("expected only entry trade after cancel; got %d", len(store.trades))
	}
}

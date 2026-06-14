package paper

import (
	"context"
	"testing"
	"time"

	"github.com/azhar/cerebro/internal/config"
	"github.com/azhar/cerebro/internal/domain"
	"github.com/azhar/cerebro/internal/risk"
	"github.com/shopspring/decimal"
)

// recordingPnL is a stub port.PnLReporter that captures every realised PnL
// the matcher reports on close.
type recordingPnL struct {
	calls []decimal.Decimal
}

func (r *recordingPnL) UpdatePnL(p decimal.Decimal) { r.calls = append(r.calls, p) }

// paperStubCache is a no-op port.Cache so a real *risk.Gate can run Check
// inside this package's tests without a Redis dependency.
type paperStubCache struct{}

func (paperStubCache) Set(context.Context, string, []byte, time.Duration) error { return nil }
func (paperStubCache) Get(context.Context, string) ([]byte, error)              { return nil, nil }
func (paperStubCache) Delete(context.Context, string) error                     { return nil }
func (paperStubCache) IncrBy(context.Context, string, int64, time.Duration) (int64, error) {
	return 0, nil
}
func (paperStubCache) Keys(context.Context, string) ([]string, error) { return nil, nil }
func (paperStubCache) Exists(context.Context, string) (bool, error)   { return false, nil }

// TestMatcher_BracketExit_FeedsRealizedPnL proves the close path computes a
// correctly-signed realised PnL and reports it to the PnLReporter. Before the
// C2 fix the reporter was never called, so drawdown/daily-loss limits stayed
// at zero forever.
func TestMatcher_BracketExit_FeedsRealizedPnL(t *testing.T) {
	const symbol = domain.Symbol("BTC/USDT")

	tests := []struct {
		name     string
		side     domain.Side
		sl, tp   decimal.Decimal
		exitLow  decimal.Decimal
		exitHigh decimal.Decimal
		wantPnL  decimal.Decimal // zero commission → exact
	}{
		{
			name: "long stop-loss realises a loss",
			side: domain.SideBuy,
			sl:   bdec("50"), tp: bdec("200"),
			exitLow: bdec("40"), exitHigh: bdec("60"),
			wantPnL: bdec("-50"), // (50-100)*1
		},
		{
			name: "long take-profit realises a profit",
			side: domain.SideBuy,
			sl:   bdec("50"), tp: bdec("150"),
			exitLow: bdec("120"), exitHigh: bdec("160"),
			wantPnL: bdec("50"), // (150-100)*1
		},
		{
			name: "short stop-loss realises a loss",
			side: domain.SideSell,
			sl:   bdec("150"), tp: bdec("50"),
			exitLow: bdec("140"), exitHigh: bdec("160"),
			wantPnL: bdec("-50"), // (100-150)*1
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := NewBook()
			store := &bracketStubStore{}
			m := NewMatcher(b, store, 0) // zero commission for exact arithmetic
			rec := &recordingPnL{}
			m.SetPnLReporter(rec)

			// fillEntry fills the entry at the bar Open (100) and attaches a
			// bracket; see bracket_test.go.
			fillEntry(t, b, m, symbol, tt.side, tt.sl, tt.tp)

			// Exit bar crosses one of the bracket legs.
			m.OnCandle(context.Background(), domain.Candle{
				Symbol: symbol, Timeframe: domain.TF1m,
				OpenTime:  time.Now().Add(time.Minute),
				CloseTime: time.Now().Add(2 * time.Minute),
				Open:      bdec("100"), High: tt.exitHigh, Low: tt.exitLow, Close: tt.exitLow,
				Closed: true,
			})

			if len(rec.calls) != 1 {
				t.Fatalf("expected exactly 1 UpdatePnL call, got %d", len(rec.calls))
			}
			if !rec.calls[0].Equal(tt.wantPnL) {
				t.Errorf("realised PnL = %s, want %s", rec.calls[0], tt.wantPnL)
			}
		})
	}
}

// TestMatcher_BracketExit_TripsDrawdownLimit drives a real losing close
// through the matcher and asserts the drawdown limit on a real *risk.Gate
// now trips — the end-to-end proof that UpdatePnL is wired through the close
// path. The pre-fix gate would have allowed the signal forever.
func TestMatcher_BracketExit_TripsDrawdownLimit(t *testing.T) {
	const symbol = domain.Symbol("BTC/USDT")

	b := NewBook()
	store := &bracketStubStore{}
	m := NewMatcher(b, store, 0)

	// 0.4% max drawdown on 10k equity → a -50 close (0.5%) must trip it.
	gate := risk.NewGate(
		config.RiskConfig{MaxDrawdownPct: 0.4},
		paperStubCache{},
		risk.NewCalendarBlackout(),
	)
	gate.SetStartingEquity(decimal.NewFromInt(10_000))
	m.SetPnLReporter(gate)

	sig := domain.Signal{Symbol: symbol, Side: domain.SideBuy, Strategy: "test"}

	// Before any close the gate allows new risk.
	if err := gate.Check(context.Background(), sig, nil); err != nil {
		t.Fatalf("gate should allow before any loss, got %v", err)
	}

	// Long entry at 100, stop at 50, qty 1 → realised PnL -50.
	fillEntry(t, b, m, symbol, domain.SideBuy, bdec("50"), bdec("200"))
	m.OnCandle(context.Background(), domain.Candle{
		Symbol: symbol, Timeframe: domain.TF1m,
		OpenTime:  time.Now().Add(time.Minute),
		CloseTime: time.Now().Add(2 * time.Minute),
		Open:      bdec("100"), High: bdec("60"), Low: bdec("40"), Close: bdec("45"),
		Closed: true,
	})

	// The losing close must now push session drawdown past the limit.
	if err := gate.Check(context.Background(), sig, nil); err == nil {
		t.Fatal("drawdown limit should trip after a losing close was fed through the matcher")
	}
}

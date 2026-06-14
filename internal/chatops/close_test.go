package chatops

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/azhar/cerebro/internal/config"
	"github.com/azhar/cerebro/internal/domain"
	"github.com/azhar/cerebro/internal/port"
	"github.com/azhar/cerebro/internal/risk"
	"github.com/shopspring/decimal"
)

// stubBroker returns a canned position list and satisfies just enough of
// port.Broker for the dispatcher's read paths.
type stubBroker struct {
	venue     domain.Venue
	positions []domain.Position
	posErr    error
}

func (s *stubBroker) Connect(_ context.Context) error                                 { return nil }
func (s *stubBroker) StreamQuotes(_ context.Context, _ []domain.Symbol) (<-chan domain.Quote, error) {
	return nil, nil
}
func (s *stubBroker) PlaceOrder(_ context.Context, _ domain.OrderIntent) (string, error) {
	return "", nil
}
func (s *stubBroker) PlaceBracket(_ context.Context, _ domain.BracketRequest) (domain.BracketResponse, error) {
	return domain.BracketResponse{}, nil
}
func (s *stubBroker) CancelOrder(_ context.Context, _ domain.CancelRequest) error       { return nil }
func (s *stubBroker) CancelBracket(_ context.Context, _ domain.BracketResponse) error   { return nil }
func (s *stubBroker) Positions(_ context.Context) ([]domain.Position, error) {
	if s.posErr != nil {
		return nil, s.posErr
	}
	return s.positions, nil
}
func (s *stubBroker) Balance(_ context.Context) (port.AccountBalance, error) {
	return port.AccountBalance{}, nil
}
func (s *stubBroker) Venue() domain.Venue { return s.venue }

// stubAudit is a no-op AuditStore for the dispatcher test path.
type stubAudit struct{}

func (stubAudit) SaveEvent(_ context.Context, _ domain.AuditEvent) error { return nil }

// captureCloser records the positions CloseFn was called with. The dispatcher
// expects CloseFn to return (brokerOrderID, error).
type captureCloser struct {
	closed []domain.Position
	err    error
}

func (c *captureCloser) close(_ context.Context, pos domain.Position) (string, error) {
	c.closed = append(c.closed, pos)
	if c.err != nil {
		return "", c.err
	}
	return "ord-" + string(pos.Symbol), nil
}

// newTestDispatcher builds a Dispatcher with the given positions + closer.
// A fresh risk.Gate is provided per test to avoid cross-test state.
func newTestDispatcher(positions []domain.Position, closer *captureCloser) *Dispatcher {
	gate := risk.NewGate(config.RiskConfig{}, nil, nil)
	return New(Deps{
		RiskGate:    gate,
		Brokers:     []port.Broker{&stubBroker{venue: domain.VenueBinanceSpot, positions: positions}},
		AuditStore:  stubAudit{},
		AllowlistFn: func(actorID string) bool { return actorID == "telegram:op" },
		CloseFn:     closer.close,
	}, 30)
}

func pos(symbol string, side domain.Side, qty string) domain.Position {
	q, _ := decimal.NewFromString(qty)
	return domain.Position{
		Symbol:   domain.Symbol(symbol),
		Venue:    domain.VenueBinanceSpot,
		Side:     side,
		Quantity: q,
	}
}

func TestHandleClose_UnknownSymbol(t *testing.T) {
	d := newTestDispatcher(nil, &captureCloser{})
	reply := d.Dispatch(context.Background(), "telegram:op", "/close BTC/USDT")
	if !strings.Contains(reply, "no open position") {
		t.Errorf("expected 'no open position' error, got %q", reply)
	}
}

func TestHandleClose_ConfirmationFlow(t *testing.T) {
	closer := &captureCloser{}
	d := newTestDispatcher([]domain.Position{pos("BTC/USDT", domain.SideBuy, "0.1")}, closer)

	// First call → arm confirmation.
	reply := d.Dispatch(context.Background(), "telegram:op", "/close BTC/USDT")
	if !strings.Contains(reply, "Send `/close") {
		t.Errorf("expected confirmation prompt, got %q", reply)
	}
	if len(closer.closed) != 0 {
		t.Errorf("close must NOT submit on the first call; got %d", len(closer.closed))
	}

	// Second call within window → execute.
	reply = d.Dispatch(context.Background(), "telegram:op", "/close BTC/USDT")
	if !strings.Contains(reply, "Close order submitted") {
		t.Errorf("expected success reply, got %q", reply)
	}
	if len(closer.closed) != 1 {
		t.Fatalf("expected 1 close, got %d", len(closer.closed))
	}
	if closer.closed[0].Symbol != "BTC/USDT" {
		t.Errorf("closed wrong symbol: %s", closer.closed[0].Symbol)
	}
}

func TestHandleClose_AcceptsLooseSymbolForms(t *testing.T) {
	positions := []domain.Position{pos("BTC/USDT", domain.SideBuy, "0.1")}
	for _, input := range []string{"BTCUSDT", "btc/usdt", "BTC-USDT", "  btc/USDT  "} {
		closer := &captureCloser{}
		d := newTestDispatcher(positions, closer)
		// Arm.
		d.Dispatch(context.Background(), "telegram:op", "/close "+input)
		// Confirm must also work with the same free-form input.
		reply := d.Dispatch(context.Background(), "telegram:op", "/close "+input)
		if !strings.Contains(reply, "Close order submitted") {
			t.Errorf("input %q: expected success, got %q", input, reply)
		}
	}
}

func TestHandleClose_AllAliasesFlatten(t *testing.T) {
	closer := &captureCloser{}
	d := newTestDispatcher([]domain.Position{
		pos("BTC/USDT", domain.SideBuy, "0.1"),
		pos("ETH/USDT", domain.SideSell, "1"),
	}, closer)

	// `/close all` arms flatten confirmation.
	reply := d.Dispatch(context.Background(), "telegram:op", "/close all")
	if !strings.Contains(reply, "close ALL") {
		t.Errorf("expected flatten confirmation prompt, got %q", reply)
	}
	// Confirm via /flatten.
	reply = d.Dispatch(context.Background(), "telegram:op", "/flatten")
	if !strings.Contains(reply, "FLATTEN submitted 2/2") {
		t.Errorf("expected 2/2 submission, got %q", reply)
	}
	if len(closer.closed) != 2 {
		t.Errorf("expected 2 closes, got %d", len(closer.closed))
	}
}

func TestHandleClose_CloseFnNilReturnsError(t *testing.T) {
	d := New(Deps{
		RiskGate:    risk.NewGate(config.RiskConfig{}, nil, nil),
		Brokers:     []port.Broker{&stubBroker{venue: domain.VenueBinanceSpot, positions: []domain.Position{pos("BTC/USDT", domain.SideBuy, "1")}}},
		AuditStore:  stubAudit{},
		AllowlistFn: func(a string) bool { return a == "telegram:op" },
	}, 30)
	reply := d.Dispatch(context.Background(), "telegram:op", "/close BTC/USDT")
	if !strings.Contains(reply, "execution router not wired") {
		t.Errorf("expected wiring error, got %q", reply)
	}
}

func TestHandleFlatten_ReportsPerLegFailures(t *testing.T) {
	closer := &captureCloser{err: errors.New("forced failure")}
	d := newTestDispatcher([]domain.Position{
		pos("BTC/USDT", domain.SideBuy, "0.1"),
		pos("ETH/USDT", domain.SideSell, "1"),
	}, closer)

	// Arm.
	d.Dispatch(context.Background(), "telegram:op", "/flatten")
	// Confirm.
	reply := d.Dispatch(context.Background(), "telegram:op", "/flatten")
	if !strings.Contains(reply, "0/2") {
		t.Errorf("expected 0/2 submitted, got %q", reply)
	}
	if !strings.Contains(reply, "BTC/USDT: forced failure") {
		t.Errorf("expected per-leg error report, got %q", reply)
	}
}

func TestHandleClose_ConfirmationExpires(t *testing.T) {
	closer := &captureCloser{}
	d := newTestDispatcher([]domain.Position{pos("BTC/USDT", domain.SideBuy, "0.1")}, closer)
	d.confirmTimeoutS = 0 // any elapsed time beyond zero expires

	// Arm.
	d.Dispatch(context.Background(), "telegram:op", "/close BTC/USDT")
	// Simulate passage of time beyond the confirmation window.
	time.Sleep(time.Millisecond)
	reply := d.Dispatch(context.Background(), "telegram:op", "/close BTC/USDT")
	// Expired → treated as a fresh first call; we should see the prompt
	// again, NOT a submitted close.
	if !strings.Contains(reply, "Send `/close") {
		t.Errorf("expected re-armed prompt after expiry, got %q", reply)
	}
	if len(closer.closed) != 0 {
		t.Errorf("expected zero closes after expired confirmation, got %d", len(closer.closed))
	}
}

func TestHandleClose_Unauthorised(t *testing.T) {
	closer := &captureCloser{}
	d := newTestDispatcher([]domain.Position{pos("BTC/USDT", domain.SideBuy, "1")}, closer)
	reply := d.Dispatch(context.Background(), "telegram:attacker", "/close BTC/USDT")
	if !strings.Contains(reply, "Access denied") {
		t.Errorf("expected denial, got %q", reply)
	}
	if len(closer.closed) != 0 {
		t.Errorf("denied actor must not trigger close, got %d", len(closer.closed))
	}
}

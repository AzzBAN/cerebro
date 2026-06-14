package chatops

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/azhar/cerebro/internal/config"
	"github.com/azhar/cerebro/internal/domain"
	"github.com/azhar/cerebro/internal/port"
	"github.com/azhar/cerebro/internal/risk"
)

// newConfirmDispatcher builds a Dispatcher wired with confirm/reject closures
// that record their calls, mirroring the per-venue queue routing in runtime.go.
func newConfirmDispatcher(confirmErr, rejectErr error) (*Dispatcher, *[]string, *[]string) {
	var confirmed, rejected []string
	gate := risk.NewGate(config.RiskConfig{}, nil, nil)
	d := New(Deps{
		RiskGate:    gate,
		Brokers:     []port.Broker{&stubBroker{venue: domain.VenueBinanceFutures}},
		AuditStore:  stubAudit{},
		AllowlistFn: func(actorID string) bool { return actorID == "telegram:op" },
		ConfirmActionFn: func(_ context.Context, id string) error {
			confirmed = append(confirmed, id)
			return confirmErr
		},
		RejectActionFn: func(id string) error {
			rejected = append(rejected, id)
			return rejectErr
		},
	}, 30)
	return d, &confirmed, &rejected
}

func TestHandleConfirm_Success(t *testing.T) {
	d, confirmed, _ := newConfirmDispatcher(nil, nil)
	reply := d.Dispatch(context.Background(), "telegram:op", "/confirm abc-123")
	if !strings.Contains(reply, "confirmed and executed") {
		t.Errorf("expected success reply, got %q", reply)
	}
	if len(*confirmed) != 1 || (*confirmed)[0] != "abc-123" {
		t.Errorf("expected confirm of abc-123, got %v", *confirmed)
	}
}

func TestHandleConfirm_MissingID(t *testing.T) {
	d, confirmed, _ := newConfirmDispatcher(nil, nil)
	reply := d.Dispatch(context.Background(), "telegram:op", "/confirm")
	if !strings.Contains(reply, "Usage:") {
		t.Errorf("expected usage hint, got %q", reply)
	}
	if len(*confirmed) != 0 {
		t.Errorf("missing ID must not call ConfirmActionFn, got %v", *confirmed)
	}
}

func TestHandleConfirm_UnknownID(t *testing.T) {
	d, _, _ := newConfirmDispatcher(errors.New("no pending action with id \"zzz\""), nil)
	reply := d.Dispatch(context.Background(), "telegram:op", "/confirm zzz")
	if !strings.Contains(reply, "Confirm failed") {
		t.Errorf("expected failure reply, got %q", reply)
	}
}

func TestHandleConfirm_NotWired(t *testing.T) {
	d := New(Deps{
		RiskGate:    risk.NewGate(config.RiskConfig{}, nil, nil),
		Brokers:     []port.Broker{&stubBroker{venue: domain.VenueBinanceFutures}},
		AuditStore:  stubAudit{},
		AllowlistFn: func(a string) bool { return a == "telegram:op" },
	}, 30)
	reply := d.Dispatch(context.Background(), "telegram:op", "/confirm abc-123")
	if !strings.Contains(reply, "not wired") {
		t.Errorf("expected not-wired error, got %q", reply)
	}
}

func TestHandleReject_Success(t *testing.T) {
	d, _, rejected := newConfirmDispatcher(nil, nil)
	reply := d.Dispatch(context.Background(), "telegram:op", "/reject abc-123")
	if !strings.Contains(reply, "rejected") {
		t.Errorf("expected rejection reply, got %q", reply)
	}
	if len(*rejected) != 1 || (*rejected)[0] != "abc-123" {
		t.Errorf("expected reject of abc-123, got %v", *rejected)
	}
}

func TestHandleReject_MissingID(t *testing.T) {
	d, _, rejected := newConfirmDispatcher(nil, nil)
	reply := d.Dispatch(context.Background(), "telegram:op", "/reject")
	if !strings.Contains(reply, "Usage:") {
		t.Errorf("expected usage hint, got %q", reply)
	}
	if len(*rejected) != 0 {
		t.Errorf("missing ID must not call RejectActionFn, got %v", *rejected)
	}
}

func TestHandleReject_NotWired(t *testing.T) {
	d := New(Deps{
		RiskGate:    risk.NewGate(config.RiskConfig{}, nil, nil),
		Brokers:     []port.Broker{&stubBroker{venue: domain.VenueBinanceFutures}},
		AuditStore:  stubAudit{},
		AllowlistFn: func(a string) bool { return a == "telegram:op" },
	}, 30)
	reply := d.Dispatch(context.Background(), "telegram:op", "/reject abc-123")
	if !strings.Contains(reply, "not wired") {
		t.Errorf("expected not-wired error, got %q", reply)
	}
}

func TestHandleConfirm_Unauthorised(t *testing.T) {
	d, confirmed, _ := newConfirmDispatcher(nil, nil)
	reply := d.Dispatch(context.Background(), "telegram:attacker", "/confirm abc-123")
	if !strings.Contains(reply, "Access denied") {
		t.Errorf("expected denial, got %q", reply)
	}
	if len(*confirmed) != 0 {
		t.Errorf("denied actor must not confirm, got %v", *confirmed)
	}
}

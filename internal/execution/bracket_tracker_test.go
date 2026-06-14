package execution

import (
	"testing"

	"github.com/azhar/cerebro/internal/domain"
)

func TestBracketTracker_RecordAndHas(t *testing.T) {
	tr := NewBracketTracker()
	sym := domain.Symbol("BTCUSDT")

	if tr.Has(sym) {
		t.Fatal("expected no bracket initially")
	}
	tr.Record(sym, domain.BracketResponse{StopOrderID: "s1", Symbol: sym})
	if !tr.Has(sym) {
		t.Fatal("expected bracket recorded")
	}
	tr.Remove(sym)
	if tr.Has(sym) {
		t.Fatal("expected bracket removed")
	}
}

func TestBracketTracker_Symbols(t *testing.T) {
	tr := NewBracketTracker()
	tr.Record("BTCUSDT", domain.BracketResponse{StopOrderID: "s1"})
	tr.Record("ETHUSDT", domain.BracketResponse{StopOrderID: "s2"})
	if got := len(tr.Symbols()); got != 2 {
		t.Fatalf("expected 2 tracked symbols, got %d", got)
	}
}

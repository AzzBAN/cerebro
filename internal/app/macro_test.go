package app

import (
	"testing"
	"time"

	"github.com/azhar/cerebro/internal/domain"
	"github.com/shopspring/decimal"
)

func TestPickMacroSymbol_PrefersBTCPerp(t *testing.T) {
	now := time.Now().UTC()
	mk := func(rate float64) *domain.DerivativesSnapshot {
		return &domain.DerivativesSnapshot{
			FundingRate: domain.FundingRate{Rate: rate, FetchedAt: now},
		}
	}
	btcSpot := mk(0.0001)
	btcPerp := mk(0.0002)
	ethPerp := mk(0.0003)

	syms := []domain.Symbol{
		domain.Symbol("ETH/USDT-PERP"),
		domain.Symbol("BTC/USDT"),
		domain.Symbol("BTC/USDT-PERP"),
	}
	snaps := map[domain.Symbol]*domain.DerivativesSnapshot{
		domain.Symbol("ETH/USDT-PERP"): ethPerp,
		domain.Symbol("BTC/USDT"):      btcSpot,
		domain.Symbol("BTC/USDT-PERP"): btcPerp,
	}
	got := pickMacroSymbol(snaps, syms)
	if got != btcPerp {
		t.Errorf("expected BTC/USDT-PERP snapshot, got rate %v", got.FundingRate.Rate)
	}
}

func TestPickMacroSymbol_FallsBackToBTCSpot(t *testing.T) {
	now := time.Now().UTC()
	btcSpot := &domain.DerivativesSnapshot{FundingRate: domain.FundingRate{Rate: 0.1, FetchedAt: now}}
	ethPerp := &domain.DerivativesSnapshot{FundingRate: domain.FundingRate{Rate: 0.2, FetchedAt: now}}

	syms := []domain.Symbol{
		domain.Symbol("ETH/USDT-PERP"),
		domain.Symbol("BTC/USDT"),
	}
	snaps := map[domain.Symbol]*domain.DerivativesSnapshot{
		domain.Symbol("ETH/USDT-PERP"): ethPerp,
		domain.Symbol("BTC/USDT"):      btcSpot,
	}
	got := pickMacroSymbol(snaps, syms)
	if got != btcSpot {
		t.Errorf("expected BTC/USDT spot fallback, got %v", got.FundingRate.Rate)
	}
}

func TestPickMacroSymbol_FallsBackToFirstWhenNoBTC(t *testing.T) {
	now := time.Now().UTC()
	ethPerp := &domain.DerivativesSnapshot{FundingRate: domain.FundingRate{Rate: 0.5, FetchedAt: now}}
	xau := &domain.DerivativesSnapshot{FundingRate: domain.FundingRate{Rate: 0.7, FetchedAt: now}}

	syms := []domain.Symbol{
		domain.Symbol("XAU/USDT"),
		domain.Symbol("ETH/USDT-PERP"),
	}
	snaps := map[domain.Symbol]*domain.DerivativesSnapshot{
		domain.Symbol("XAU/USDT"):      xau,
		domain.Symbol("ETH/USDT-PERP"): ethPerp,
	}
	got := pickMacroSymbol(snaps, syms)
	// No BTC; ETH PERP should win over XAU spot.
	if got != ethPerp {
		t.Errorf("expected ETH PERP fallback, got %v", got.FundingRate.Rate)
	}
}

func TestBuildMacroSnapshot_EmptyReturnsFalse(t *testing.T) {
	if _, ok := buildMacroSnapshot(nil, nil); ok {
		t.Error("expected ok=false for nil inputs")
	}
	if _, ok := buildMacroSnapshot(map[domain.Symbol]*domain.DerivativesSnapshot{}, []domain.Symbol{}); ok {
		t.Error("expected ok=false for empty inputs")
	}
}

func TestBuildMacroSnapshot_PopulatesFields(t *testing.T) {
	now := time.Now().UTC()
	btcPerp := &domain.DerivativesSnapshot{
		FundingRate:    domain.FundingRate{Rate: -0.000033, FetchedAt: now},
		OpenInterest:   domain.OpenInterest{TotalUSD: decimal.NewFromInt(54_000_000_000), Change24h: 1.2, FetchedAt: now},
		FearGreed:      domain.FearGreedIndex{Value: 29, Category: "Fear", FetchedAt: now},
		LongShortRatio: domain.LongShortRatio{GlobalRatio: 0.98, FetchedAt: now},
	}
	syms := []domain.Symbol{domain.Symbol("BTC/USDT-PERP")}
	snaps := map[domain.Symbol]*domain.DerivativesSnapshot{domain.Symbol("BTC/USDT-PERP"): btcPerp}

	macro, ok := buildMacroSnapshot(snaps, syms)
	if !ok {
		t.Fatal("expected ok=true")
	}
	if macro.FearGreed.Value != 29 {
		t.Errorf("FearGreed.Value = %d, want 29", macro.FearGreed.Value)
	}
	if macro.BTCFundingRate.Rate != -0.000033 {
		t.Errorf("FundingRate.Rate = %v, want -0.000033", macro.BTCFundingRate.Rate)
	}
	if macro.BTCOpenInterest.TotalUSD.IsZero() {
		t.Error("expected non-zero OpenInterest")
	}
	if macro.BTCLongShort.GlobalRatio != 0.98 {
		t.Errorf("LongShortRatio.GlobalRatio = %v, want 0.98", macro.BTCLongShort.GlobalRatio)
	}
	if macro.UpdatedAt.IsZero() {
		t.Error("expected UpdatedAt to be set")
	}
}

func TestContainsFold(t *testing.T) {
	tests := []struct {
		s, sub string
		want   bool
	}{
		{"BTC/USDT-PERP", "btc", true},
		{"BTC/USDT-PERP", "PERP", true},
		{"BTC/USDT-PERP", "perp", true},
		{"ETH/USDT", "BTC", false},
		{"abc", "", true},
		{"a", "abc", false},
	}
	for _, tt := range tests {
		if got := containsFold(tt.s, tt.sub); got != tt.want {
			t.Errorf("containsFold(%q, %q) = %v, want %v", tt.s, tt.sub, got, tt.want)
		}
	}
}

package screening

import (
	"testing"
	"time"

	"github.com/azhar/cerebro/internal/domain"
	"github.com/shopspring/decimal"
)

func TestBuildTradePlan_BuySide_GeometryIsCorrect(t *testing.T) {
	f := Features{
		Symbol:           "BTCUSDT",
		Venue:            domain.VenueBinanceFutures,
		BaseAsset:        "BTC",
		LastPrice:        decimal.NewFromInt(60_000),
		PriceChangePct24: 5.0,
	}
	fit := StrategyFit{Strategy: "trend_following", Confidence: 0.75, Reasons: []string{"OI Δ24h ≥ 5%"}}
	params := PlanParams{StopLossPct: 1.0, TP1RR: 2.0, TP2RR: 4.0, EntryWidthPct: 0.1}

	plan, ok := BuildTradePlan(f, domain.RegimeTrending, domain.SideBuy,
		fit, domain.BiasBullish, params, time.Now())
	if !ok {
		t.Fatal("plan should build")
	}

	if plan.Side != domain.SideBuy {
		t.Errorf("side: want buy, got %s", plan.Side)
	}
	if plan.EntryHigh.Cmp(plan.EntryLow) != 1 {
		t.Errorf("entry zone: high (%s) should exceed low (%s)",
			plan.EntryHigh, plan.EntryLow)
	}
	if plan.StopLoss.Cmp(plan.EntryLow) != -1 {
		t.Errorf("buy SL must sit below entry low: SL=%s low=%s",
			plan.StopLoss, plan.EntryLow)
	}
	if plan.TakeProfit1.Cmp(plan.LastPrice) != 1 {
		t.Errorf("buy TP1 must sit above last price: tp=%s last=%s",
			plan.TakeProfit1, plan.LastPrice)
	}
	if plan.TakeProfit2.Cmp(plan.TakeProfit1) != 1 {
		t.Errorf("TP2 should sit beyond TP1")
	}
	if plan.RRRatio != 2.0 {
		t.Errorf("rr_ratio: want 2.0, got %f", plan.RRRatio)
	}
	if plan.Strategy != "trend_following" {
		t.Errorf("strategy: want trend_following, got %s", plan.Strategy)
	}
	if plan.Bias != domain.BiasBullish {
		t.Errorf("bias: want bullish, got %s", plan.Bias)
	}
}

func TestBuildTradePlan_SellSide_GeometryIsCorrect(t *testing.T) {
	f := Features{
		Symbol:    "PEPEUSDT",
		LastPrice: decimal.NewFromFloat(0.000010),
	}
	fit := StrategyFit{Strategy: "squeeze_fade", Confidence: 0.7}

	plan, ok := BuildTradePlan(f, domain.RegimeSqueeze, domain.SideSell, fit,
		domain.BiasBearish, DefaultPlanParams(), time.Now())
	if !ok {
		t.Fatal("plan should build")
	}
	if plan.StopLoss.Cmp(plan.EntryHigh) != 1 {
		t.Errorf("sell SL must sit above entry high: SL=%s high=%s",
			plan.StopLoss, plan.EntryHigh)
	}
	if plan.TakeProfit1.Cmp(plan.LastPrice) != -1 {
		t.Errorf("sell TP1 must sit below last price")
	}
}

func TestBuildTradePlan_RejectsZeroPrice(t *testing.T) {
	f := Features{LastPrice: decimal.Zero}
	if _, ok := BuildTradePlan(f, domain.RegimeTrending, domain.SideBuy,
		StrategyFit{}, domain.BiasNeutral, DefaultPlanParams(), time.Now()); ok {
		t.Error("plan should reject zero last price")
	}
}

func TestBuildTradePlan_RejectsBadParams(t *testing.T) {
	f := Features{LastPrice: decimal.NewFromInt(100)}
	bad := PlanParams{StopLossPct: 0, TP1RR: 1.0}
	if _, ok := BuildTradePlan(f, domain.RegimeTrending, domain.SideBuy,
		StrategyFit{}, domain.BiasNeutral, bad, time.Now()); ok {
		t.Error("plan should reject zero StopLossPct")
	}
}

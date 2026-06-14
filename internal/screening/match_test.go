package screening

import (
	"testing"

	"github.com/azhar/cerebro/internal/domain"
)

func TestMatchStrategies_RespectsEnabledSet(t *testing.T) {
	f := Features{PriceChangePct24: 8, OIChange24hPct: 6}
	enabled := EnabledFromList([]domain.StrategyName{"trend_following"})

	got := MatchStrategies(f, domain.RegimeTrending, domain.SideBuy, enabled, 5)
	if len(got) != 1 || got[0].Strategy != "trend_following" {
		t.Fatalf("want only trend_following, got %+v", got)
	}
}

func TestMatchStrategies_AlignmentBumpsConfidence(t *testing.T) {
	enabled := EnabledFromList([]domain.StrategyName{"squeeze_fade", "mean_reversion", "funding_arb"})

	hot := Features{FundingRate: 0.15, LongShortRatio: 2.5}
	cold := Features{FundingRate: 0.0, LongShortRatio: 1.0}

	hotFits := MatchStrategies(hot, domain.RegimeSqueeze, domain.SideSell, enabled, 1)
	coldFits := MatchStrategies(cold, domain.RegimeSqueeze, domain.SideSell, enabled, 1)
	if len(hotFits) == 0 || len(coldFits) == 0 {
		t.Fatalf("expected at least one fit each")
	}
	if hotFits[0].Confidence <= coldFits[0].Confidence {
		t.Errorf("extreme funding should score higher than neutral; hot=%.3f cold=%.3f",
			hotFits[0].Confidence, coldFits[0].Confidence)
	}
	// reasons should mention the funding extreme on the hot row
	if len(hotFits[0].Reasons) == 0 {
		t.Errorf("hot fit should carry reason strings")
	}
}

func TestMatchStrategies_UnknownRegimeReturnsEmpty(t *testing.T) {
	got := MatchStrategies(Features{}, domain.RegimeUnknown, domain.SideBuy, nil, 1)
	if len(got) != 0 {
		t.Errorf("unknown regime: want empty, got %+v", got)
	}
}

func TestMatchStrategies_TopNCap(t *testing.T) {
	enabled := EnabledFromList([]domain.StrategyName{
		"squeeze_fade", "mean_reversion", "funding_arb",
	})
	got := MatchStrategies(Features{}, domain.RegimeSqueeze, domain.SideSell, enabled, 2)
	if len(got) != 2 {
		t.Errorf("topN cap not enforced: got %d", len(got))
	}
}

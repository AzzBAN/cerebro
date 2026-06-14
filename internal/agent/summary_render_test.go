package agent

import (
	"strings"
	"testing"
	"time"

	"github.com/azhar/cerebro/internal/domain"
)

func TestRenderScreeningSummary(t *testing.T) {
	cached := time.Now().UTC()
	biases := []domain.BiasResult{
		{
			Symbol:    "BTC/USDT",
			Score:     domain.BiasBullish,
			Reasoning: "Strong volume confirmation; funding neutral.",
			CachedAt:  cached,
		},
		{
			Symbol:    "ETH/USDT",
			Score:     domain.BiasBearish,
			Reasoning: "Breaking down below 50-MA with rising short interest.",
			CachedAt:  cached,
		},
	}

	opps := []domain.ScreeningOpportunity{
		{
			Symbol:     "BTC/USDT",
			Venue:      "binance_spot",
			Side:       "buy",
			Confidence: 0.72,
			Reasoning:  "Best relative strength vs peers",
		},
		{
			Symbol:     "XAU/USDT-PERP",
			Venue:      "binance_futures",
			Side:       "sell",
			Confidence: 0.55,
			Reasoning:  "FOMC within 2h — stepping aside",
			Avoided:    true,
		},
	}

	out := renderScreeningSummary(biases, opps)

	// Must be markdown with required sections.
	for _, required := range []string{
		"### 📊 Market Overview",
		"### 🧭 Per-Symbol Bias",
		"### 🎯 Top Opportunities",
		"BTC/USDT",
		"ETH/USDT",
		"AVOIDED",
	} {
		if !strings.Contains(out, required) {
			t.Errorf("rendered summary missing %q\n---\n%s", required, out)
		}
	}

	// Bullish vs bearish split should trigger the watchlist divergence note.
	if !strings.Contains(out, "### 👀 Watchlist") {
		t.Error("expected Watchlist section when bullish and bearish both > 0")
	}

	// Empty opportunities path.
	out2 := renderScreeningSummary(biases, nil)
	if !strings.Contains(out2, "No actionable opportunities") {
		t.Errorf("empty opportunities path did not produce the fallback line:\n%s", out2)
	}
}

func TestRenderScreeningSummary_AllNeutral(t *testing.T) {
	biases := []domain.BiasResult{
		{Symbol: "BTC/USDT", Score: domain.BiasNeutral, Reasoning: "Range-bound."},
		{Symbol: "ETH/USDT", Score: domain.BiasNeutral, Reasoning: "Range-bound."},
	}
	out := renderScreeningSummary(biases, nil)
	if !strings.Contains(out, "Mixed / neutral") {
		t.Errorf("expected neutral overview, got:\n%s", out)
	}
	// No divergence, no avoided → no Watchlist section at all.
	if strings.Contains(out, "### Watchlist") {
		t.Errorf("unexpected Watchlist section when nothing to watch:\n%s", out)
	}
}

func TestRenderScreeningSummary_EmptyBiases(t *testing.T) {
	out := renderScreeningSummary(nil, nil)
	// We still produce the overview section even with zero biases; the
	// counts will all be zero. The caller (runSummary) guards against
	// rendering empty summaries; here we just verify the function is safe.
	if !strings.Contains(out, "### 📊 Market Overview") {
		t.Errorf("expected header even with empty input, got:\n%s", out)
	}
}

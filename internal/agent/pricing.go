package agent

import "strings"

// Pricing is per-million-token USD pricing for a single provider/model.
// Kept as a package-level table so the Runtime can estimate daily spend
// without plumbing pricing config through every call site.
//
// Values are conservative published list prices at the time of writing —
// keep them in sync with the provider's dashboard. When a model is not in
// the table the estimator returns 0 and CostTracker still records the
// token counts (which are authoritative from the API response).
type Pricing struct {
	InputPerMTok       float64 // USD per 1M uncached input tokens
	CachedInputPerMTok float64 // USD per 1M cached-read input tokens
	OutputPerMTok      float64 // USD per 1M output tokens
}

// known pricing, lookup key is strings.ToLower(modelID). Partial prefixes
// match so "claude-haiku-4-5-20250514" matches "claude-haiku".
var modelPricing = []struct {
	prefix string
	p      Pricing
}{
	// Anthropic
	{"claude-haiku", Pricing{InputPerMTok: 1.00, CachedInputPerMTok: 0.10, OutputPerMTok: 5.00}},
	{"claude-3-haiku", Pricing{InputPerMTok: 0.25, CachedInputPerMTok: 0.03, OutputPerMTok: 1.25}},
	{"claude-sonnet", Pricing{InputPerMTok: 3.00, CachedInputPerMTok: 0.30, OutputPerMTok: 15.00}},
	{"claude-opus", Pricing{InputPerMTok: 15.00, CachedInputPerMTok: 1.50, OutputPerMTok: 75.00}},

	// Google
	{"gemini-1.5-flash", Pricing{InputPerMTok: 0.075, OutputPerMTok: 0.30}},
	{"gemini-1.5-pro", Pricing{InputPerMTok: 1.25, OutputPerMTok: 5.00}},
	{"gemini-2.0-flash", Pricing{InputPerMTok: 0.10, OutputPerMTok: 0.40}},
	{"gemini-2.5-flash", Pricing{InputPerMTok: 0.30, OutputPerMTok: 2.50}},
	{"gemini-2.5-pro", Pricing{InputPerMTok: 1.25, OutputPerMTok: 10.00}},

	// OpenAI
	{"gpt-4o-mini", Pricing{InputPerMTok: 0.15, OutputPerMTok: 0.60}},
	{"gpt-4o", Pricing{InputPerMTok: 2.50, OutputPerMTok: 10.00}},
	{"gpt-4.1-mini", Pricing{InputPerMTok: 0.40, OutputPerMTok: 1.60}},
	{"gpt-4.1", Pricing{InputPerMTok: 2.00, OutputPerMTok: 8.00}},
	{"o3-mini", Pricing{InputPerMTok: 1.10, OutputPerMTok: 4.40}},

	// OpenRouter / MiniMax (rough estimate — varies by provider routing)
	{"minimax", Pricing{InputPerMTok: 0.30, OutputPerMTok: 1.20}},
}

// LookupPricing returns the per-million-token pricing for the given model,
// or the zero value when the model is unknown. Matching is case-insensitive
// and prefix-based so versioned model IDs resolve to their family's pricing.
func LookupPricing(modelID string) Pricing {
	key := strings.ToLower(modelID)
	for _, entry := range modelPricing {
		if strings.Contains(key, entry.prefix) {
			return entry.p
		}
	}
	return Pricing{}
}

// EstimateCostMicroUSD returns the estimated USD cost in **micro-dollars**
// (10⁻⁶ USD, i.e. millionths of a dollar) for the given token counts. This
// resolution is necessary because many providers charge well below $0.01
// per typical call — rounding to whole cents would record 0 and lose the
// usage entirely. 1 cent = 10 000 μUSD, $1 = 1 000 000 μUSD.
//
// Returns 0 when pricing is the zero value (unknown model).
func (p Pricing) EstimateCostMicroUSD(inputTokens, outputTokens, cachedInputTokens int) int64 {
	if p.InputPerMTok == 0 && p.OutputPerMTok == 0 && p.CachedInputPerMTok == 0 {
		return 0
	}
	// Uncached input = total input - cached input. Cached may exceed input
	// in pathological provider responses; clamp to keep math sane.
	uncached := inputTokens - cachedInputTokens
	if uncached < 0 {
		uncached = 0
	}
	// Per-MTok prices × tokens ÷ 1M → USD. Multiply by 1M → μUSD. The ÷1M
	// and ×1M cancel, so the result is simply prices × tokens.
	micro := float64(uncached)*p.InputPerMTok +
		float64(cachedInputTokens)*p.CachedInputPerMTok +
		float64(outputTokens)*p.OutputPerMTok
	if micro < 0 {
		return 0
	}
	return int64(micro + 0.5)
}

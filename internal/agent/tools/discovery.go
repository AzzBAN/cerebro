package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/azhar/cerebro/internal/port"
)

// discoveryCacheKey mirrors agent.DiscoveryCacheKey. It is duplicated here
// to avoid an import cycle (agent → agent/tools → agent). Keep in sync.
const discoveryCacheKey = "discovery:candidates"

// GetDiscoveryCandidates exposes the cached Phase 0 discovery candidate list
// to the screening agent. The list is refreshed by ScreeningAgent.runCycle
// before Phase 1 each cycle, so by the time Phase 2 invokes this tool the
// data is at most one cycle old.
//
// The tool is deliberately argument-less: discovery output is the full
// universe of dynamic candidates and the LLM should reason over all of it.
//
// The cached value is opaque to this tool — it is forwarded as raw JSON
// inside the response envelope so the LLM sees the full enriched row
// (symbol, venue, tags, price_change_pct_24h, quote_volume_24h, …).
func GetDiscoveryCandidates(cache port.Cache) port.Tool {
	return port.Tool{
		Handler: func(ctx context.Context, _ json.RawMessage) (json.RawMessage, error) {
			if cache == nil {
				return json.Marshal(map[string]any{
					"candidates": []any{},
					"count":      0,
					"message":    "Cache not configured; discovery candidates unavailable.",
				})
			}
			raw, err := cache.Get(ctx, discoveryCacheKey)
			if err != nil {
				return nil, fmt.Errorf("get_discovery_candidates: cache get: %w", err)
			}
			if raw == nil {
				return json.Marshal(map[string]any{
					"candidates": []any{},
					"count":      0,
					"message":    "No discovery candidates cached yet. Either Phase 0 is disabled, has not run, or all candidates were filtered out by the configured thresholds.",
				})
			}

			// Validate the cached blob is a JSON array; pass it through verbatim.
			var arr []json.RawMessage
			if err := json.Unmarshal(raw, &arr); err != nil {
				return nil, fmt.Errorf("get_discovery_candidates: malformed cache entry: %w", err)
			}
			return json.Marshal(map[string]any{
				"candidates": arr,
				"count":      len(arr),
			})
		},
		Definition: port.ToolDefinition{
			Name: "get_discovery_candidates",
			Description: "Get the dynamically discovered symbols for this cycle (top movers, high volume, new listings) " +
				"that are NOT in the configured markets.yaml watchlist. Each row includes price_change_pct_24h, " +
				"quote_volume_24h, is_new_listing, and tags (e.g. new_listing, top_mover_up). " +
				"Use this in cross-symbol screening alongside get_all_market_data to broaden the opportunity set " +
				"beyond the static watchlist. Discovered symbols cannot be executed automatically — surface them " +
				"in the summary and tag them as [unconfigured].",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		},
	}
}

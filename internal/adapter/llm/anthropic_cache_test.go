package llm

import (
	"strings"
	"testing"

	"github.com/azhar/cerebro/internal/port"
)

func TestBuildSystemBlocks(t *testing.T) {
	t.Run("empty prompt produces no blocks", func(t *testing.T) {
		if got := buildSystemBlocks(""); len(got) != 0 {
			t.Fatalf("expected 0 blocks, got %d", len(got))
		}
	})

	t.Run("short prompt is NOT marked cacheable", func(t *testing.T) {
		blocks := buildSystemBlocks("tiny prompt")
		if len(blocks) != 1 {
			t.Fatalf("expected 1 block, got %d", len(blocks))
		}
		if blocks[0].CacheControl != nil {
			t.Errorf("short prompt should not be marked cacheable — ephemeral-write overhead outweighs read savings")
		}
	})

	t.Run("large prompt is marked cacheable", func(t *testing.T) {
		big := strings.Repeat("You are the Cerebro Screening Agent. ", 200) // ~7400 chars
		blocks := buildSystemBlocks(big)
		if len(blocks) != 1 {
			t.Fatalf("expected 1 block, got %d", len(blocks))
		}
		if blocks[0].CacheControl == nil || blocks[0].CacheControl.Type != "ephemeral" {
			t.Errorf("large prompt must have ephemeral cache_control, got %+v", blocks[0].CacheControl)
		}
	})
}

func TestBuildAnthropicTools_CachingAndDeterminism(t *testing.T) {
	tools := map[string]port.Tool{
		"get_market_data":     {Definition: port.ToolDefinition{Name: "get_market_data", Description: "d1"}},
		"fetch_latest_news":   {Definition: port.ToolDefinition{Name: "fetch_latest_news", Description: "d2"}},
		"get_economic_events": {Definition: port.ToolDefinition{Name: "get_economic_events", Description: "d3"}},
	}

	t.Run("empty map returns nil", func(t *testing.T) {
		if got := buildAnthropicTools(nil); got != nil {
			t.Fatalf("expected nil, got %+v", got)
		}
	})

	t.Run("only the last tool carries cache_control", func(t *testing.T) {
		got := buildAnthropicTools(tools)
		if len(got) != 3 {
			t.Fatalf("expected 3 tools, got %d", len(got))
		}
		for i := 0; i < len(got)-1; i++ {
			if got[i].CacheControl != nil {
				t.Errorf("tool[%d] (%s) must NOT have cache_control; only the final tool does",
					i, got[i].Name)
			}
		}
		last := got[len(got)-1]
		if last.CacheControl == nil || last.CacheControl.Type != "ephemeral" {
			t.Errorf("final tool must have ephemeral cache_control, got %+v", last.CacheControl)
		}
	})

	t.Run("tool order is stable across calls — required for cache hits", func(t *testing.T) {
		a := buildAnthropicTools(tools)
		b := buildAnthropicTools(tools)
		if len(a) != len(b) {
			t.Fatalf("length mismatch %d vs %d", len(a), len(b))
		}
		for i := range a {
			if a[i].Name != b[i].Name {
				t.Fatalf("tool[%d] order diverges between calls: %s vs %s — cache would always miss",
					i, a[i].Name, b[i].Name)
			}
		}
	})

	t.Run("default input_schema is supplied when missing", func(t *testing.T) {
		one := map[string]port.Tool{
			"x": {Definition: port.ToolDefinition{Name: "x", Description: "d"}},
		}
		got := buildAnthropicTools(one)
		if got[0].InputSchema == nil {
			t.Fatalf("expected default empty-object schema, got nil")
		}
		if got[0].InputSchema["type"] != "object" {
			t.Fatalf("expected type=object, got %v", got[0].InputSchema["type"])
		}
	})
}

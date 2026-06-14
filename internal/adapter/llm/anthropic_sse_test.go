package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/azhar/cerebro/internal/port"
)

func TestLooksLikeSSE(t *testing.T) {
	tests := []struct {
		name string
		body string
		want bool
	}{
		{"event prefix", "event: message_start\ndata: {}", true},
		{"data prefix", "data: {\"type\":\"message_start\"}", true},
		{"leading whitespace then event", "\n\n  event: foo", true},
		{"plain json object", `{"content":[]}`, false},
		{"plain json array", `[{"x":1}]`, false},
		{"empty", "", false},
		{"json with data field is not sse", `{"data":"x"}`, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := looksLikeSSE([]byte(tt.body)); got != tt.want {
				t.Errorf("looksLikeSSE(%q) = %v, want %v", tt.body, got, tt.want)
			}
		})
	}
}

// textSSE is a minimal text-only Anthropic stream ending with end_turn.
const textSSE = `event: message_start
data: {"type":"message_start","message":{"type":"message","role":"assistant","usage":{"input_tokens":42}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello "}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"world"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":7}}

event: message_stop
data: {"type":"message_stop"}
`

// toolSSE is a stream where the model calls a tool, with the input arguments
// split across multiple input_json_delta fragments.
const toolSSE = `event: message_start
data: {"type":"message_start","message":{"type":"message","role":"assistant","usage":{"input_tokens":10}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_123","name":"get_price"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"sym"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"bol\":\"BTCUSDT\"}"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":15}}

event: message_stop
data: {"type":"message_stop"}
`

func TestParseAnthropicSSE_Text(t *testing.T) {
	resp, err := parseAnthropicSSE([]byte(textSSE))
	if err != nil {
		t.Fatalf("parseAnthropicSSE: %v", err)
	}
	if resp.StopReason != "end_turn" {
		t.Errorf("stop_reason = %q, want end_turn", resp.StopReason)
	}
	if len(resp.Content) != 1 || resp.Content[0].Type != "text" {
		t.Fatalf("content = %+v, want one text block", resp.Content)
	}
	if got := resp.Content[0].Text; got != "Hello world" {
		t.Errorf("text = %q, want %q", got, "Hello world")
	}
	if resp.Usage.InputTokens != 42 || resp.Usage.OutputTokens != 7 {
		t.Errorf("usage = %+v, want in=42 out=7", resp.Usage)
	}
}

func TestParseAnthropicSSE_ToolUse(t *testing.T) {
	resp, err := parseAnthropicSSE([]byte(toolSSE))
	if err != nil {
		t.Fatalf("parseAnthropicSSE: %v", err)
	}
	if resp.StopReason != "tool_use" {
		t.Errorf("stop_reason = %q, want tool_use", resp.StopReason)
	}
	if len(resp.Content) != 1 || resp.Content[0].Type != "tool_use" {
		t.Fatalf("content = %+v, want one tool_use block", resp.Content)
	}
	blk := resp.Content[0]
	if blk.ID != "toolu_123" || blk.Name != "get_price" {
		t.Errorf("tool block id/name = %q/%q, want toolu_123/get_price", blk.ID, blk.Name)
	}
	// The split input_json_delta fragments must reassemble into valid JSON.
	var input map[string]string
	if err := json.Unmarshal(blk.Input, &input); err != nil {
		t.Fatalf("reassembled tool input is not valid JSON: %v (raw=%s)", err, string(blk.Input))
	}
	if input["symbol"] != "BTCUSDT" {
		t.Errorf("tool input symbol = %q, want BTCUSDT", input["symbol"])
	}
}

func TestParseAnthropicSSE_NoEvents(t *testing.T) {
	// Only keep-alive comment frames, no data events.
	_, err := parseAnthropicSSE([]byte(": keep-alive\n\n: ping\n\n"))
	if err == nil {
		t.Fatal("expected error for stream with no data events, got nil")
	}
}

// stubTool implements a tool whose handler records its invocation.
type stubTool struct {
	called  bool
	gotArgs string
}

func (s *stubTool) def() port.ToolDefinition {
	return port.ToolDefinition{
		Name:        "get_price",
		Description: "Get the current price of a symbol",
		InputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{"symbol": map[string]any{"type": "string"}},
		},
	}
}

// TestComplete_SSE_ToolLoop drives the full Complete loop against an httptest
// server that streams SSE: first turn returns a tool_use stream, second turn
// (after the tool result is posted back) returns a final text stream. This
// proves the adapter dispatches the tool and consumes the streamed answer.
func TestComplete_SSE_ToolLoop(t *testing.T) {
	tool := &stubTool{}
	turn := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		body, _ := io.ReadAll(r.Body)
		if turn == 0 {
			// First call must NOT contain a tool_result yet.
			if strings.Contains(string(body), "tool_result") {
				t.Errorf("turn 0 unexpectedly contained tool_result")
			}
			turn++
			_, _ = w.Write([]byte(toolSSE))
			return
		}
		// Second call must echo the tool result back to the model.
		if !strings.Contains(string(body), "toolu_123") {
			t.Errorf("turn 1 missing tool_use id in messages: %s", body)
		}
		tool.called = true
		_, _ = w.Write([]byte(textSSE))
	}))
	defer srv.Close()

	a := NewAnthropic("test-key", srv.URL, "claude-haiku-4-5", 0.0, 256)
	tools := map[string]port.Tool{
		"get_price": {
			Definition: tool.def(),
			Handler: func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
				tool.gotArgs = string(args)
				return json.RawMessage(`{"price":"64000"}`), nil
			},
		},
	}

	out, err := a.Complete(context.Background(), "You are a test.", "Look up BTCUSDT.", tools)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if !tool.called {
		t.Error("expected second turn to fire (tool result echoed)")
	}
	if !strings.Contains(tool.gotArgs, "BTCUSDT") {
		t.Errorf("tool handler got args %q, want symbol BTCUSDT", tool.gotArgs)
	}
	if out != "Hello world" {
		t.Errorf("final output = %q, want %q", out, "Hello world")
	}
}

// TestComplete_NonStreamingJSON confirms the standard single-object JSON path
// still works (real api.anthropic.com and JSON-returning proxies).
func TestComplete_NonStreamingJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"stop_reason":"end_turn","content":[{"type":"text","text":"plain json"}],"usage":{"input_tokens":5,"output_tokens":2}}`))
	}))
	defer srv.Close()

	a := NewAnthropic("test-key", srv.URL, "claude-haiku-4-5", 0.0, 256)
	out, err := a.Complete(context.Background(), "sys", "hi", nil)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if out != "plain json" {
		t.Errorf("output = %q, want %q", out, "plain json")
	}
}

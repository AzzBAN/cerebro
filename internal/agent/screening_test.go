package agent

import (
	"testing"
)

func TestParseOpportunitiesOutput(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantLen int
		wantErr bool
	}{
		{
			name: "valid JSON",
			input: `{"opportunities": [
				{"symbol": "BTCUSDT", "venue": "binance_spot", "side": "BUY", "confidence": 0.8, "reasoning": "Strong bullish momentum", "correlations": [{"symbol": "ETHUSDT", "impact": "confirming", "note": "ETH also bullish"}], "avoided": false},
				{"symbol": "ETHUSDT", "venue": "binance_futures", "side": "SELL", "confidence": 0.6, "reasoning": "Diverging from BTC", "correlations": [], "avoided": true}
			]}`,
			wantLen: 2,
			wantErr: false,
		},
		{
			name: "valid JSON with markdown fences",
			input: "```json\n{\"opportunities\": [{\"symbol\": \"BTCUSDT\", \"venue\": \"binance_spot\", \"side\": \"BUY\", \"confidence\": 0.7, \"reasoning\": \"test\", \"correlations\": [], \"avoided\": false}]}\n```",
			wantLen: 1,
			wantErr: false,
		},
		{
			name:    "valid JSON embedded in text",
			input:   "Here are the opportunities:\n{\"opportunities\": [{\"symbol\": \"BTCUSDT\", \"venue\": \"binance_spot\", \"side\": \"BUY\", \"confidence\": 0.5, \"reasoning\": \"ok\", \"correlations\": [], \"avoided\": false}]}\nEnd of analysis.",
			wantLen: 1,
			wantErr: false,
		},
		{
			name:    "empty string",
			input:   "",
			wantErr: true,
		},
		{
			name:    "no JSON object",
			input:   "no json here just words",
			wantErr: true,
		},
		{
			name:    "malformed JSON",
			input:   `{"opportunities": [{"symbol": "BTCUSDT"`,
			wantErr: true,
		},
		{
			name:    "empty opportunities array",
			input:   `{"opportunities": []}`,
			wantLen: 0,
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseOpportunitiesOutput(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseOpportunitiesOutput() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && len(got) != tt.wantLen {
				t.Errorf("parseOpportunitiesOutput() returned %d opportunities, want %d", len(got), tt.wantLen)
			}
		})
	}
}

func TestParseOpportunitiesOutput_FieldsMapped(t *testing.T) {
	input := `{"opportunities": [{"symbol": "BTCUSDT", "venue": "binance_futures", "side": "SELL", "confidence": 0.85, "reasoning": "Breakdown below support", "correlations": [{"symbol": "ETHUSDT", "impact": "diverging", "note": "ETH holding support"}], "avoided": true}]}`

	results, err := parseOpportunitiesOutput(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 opportunity, got %d", len(results))
	}

	opp := results[0]
	if opp.Symbol != "BTCUSDT" {
		t.Errorf("Symbol = %q, want %q", opp.Symbol, "BTCUSDT")
	}
	if opp.Venue != "binance_futures" {
		t.Errorf("Venue = %q, want %q", opp.Venue, "binance_futures")
	}
	if opp.Side != "sell" {
		t.Errorf("Side = %q, want %q", opp.Side, "sell")
	}
	if opp.Confidence != 0.85 {
		t.Errorf("Confidence = %f, want %f", opp.Confidence, 0.85)
	}
	if opp.Reasoning != "Breakdown below support" {
		t.Errorf("Reasoning = %q, want %q", opp.Reasoning, "Breakdown below support")
	}
	if !opp.Avoided {
		t.Error("Avoided = false, want true")
	}
	if len(opp.Correlations) != 1 {
		t.Fatalf("Correlations length = %d, want 1", len(opp.Correlations))
	}
	if opp.Correlations[0].Symbol != "ETHUSDT" {
		t.Errorf("Correlation Symbol = %q, want %q", opp.Correlations[0].Symbol, "ETHUSDT")
	}
	if opp.Correlations[0].Impact != "diverging" {
		t.Errorf("Correlation Impact = %q, want %q", opp.Correlations[0].Impact, "diverging")
	}
}

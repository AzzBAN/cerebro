package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/azhar/cerebro/internal/port"
)

// PendingSymbol represents a symbol request waiting to be reviewed and merged.
type PendingSymbol struct {
	Symbol    string    `json:"symbol"`
	Venue     string    `json:"venue"`
	Reason    string    `json:"reason"`
	Timestamp time.Time `json:"timestamp"`
}

// RequestSymbolMonitor implements the request_symbol_monitor agent tool.
// It appends a symbol request to configs/pending_symbols.jsonl for operator review.
func RequestSymbolMonitor(configDir string) port.Tool {
	return port.Tool{
		Handler: func(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
			var args struct {
				Symbol string `json:"symbol"`
				Venue  string `json:"venue"`
				Reason string `json:"reason"`
			}
			if err := json.Unmarshal(input, &args); err != nil {
				return nil, fmt.Errorf("request_symbol_monitor: bad args: %w", err)
			}
			if args.Symbol == "" {
				return nil, fmt.Errorf("request_symbol_monitor: symbol is required")
			}
			if args.Venue == "" {
				args.Venue = "binance_futures"
			}
			if args.Reason == "" {
				args.Reason = "Interesting setup from copilot analysis"
			}

			sym := normalizeToolSymbol(args.Symbol)
			pending := PendingSymbol{
				Symbol:    string(sym),
				Venue:     args.Venue,
				Reason:    args.Reason,
				Timestamp: time.Now(),
			}

			// Append to pending_symbols.jsonl
			pendingFile := filepath.Join(configDir, "pending_symbols.jsonl")
			f, err := os.OpenFile(pendingFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
			if err != nil {
				return nil, fmt.Errorf("request_symbol_monitor: open file: %w", err)
			}
			defer f.Close()

			data, err := json.Marshal(pending)
			if err != nil {
				return nil, fmt.Errorf("request_symbol_monitor: marshal: %w", err)
			}
			if _, err := f.Write(append(data, '\n')); err != nil {
				return nil, fmt.Errorf("request_symbol_monitor: write: %w", err)
			}

			result := map[string]any{
				"status":  "pending",
				"symbol":  pending.Symbol,
				"venue":   pending.Venue,
				"reason":  pending.Reason,
				"message": fmt.Sprintf("Symbol %s added to %s. Run 'cerebro pending' to review and merge into markets.yaml.", pending.Symbol, pendingFile),
			}

			return json.Marshal(result)
		},
		Definition: port.ToolDefinition{
			Name:        "request_symbol_monitor",
			Description: "Request a symbol to be added to the monitoring watchlist. Appends to configs/pending_symbols.jsonl for operator review. Operator can run 'cerebro pending' CLI command to review and merge into markets.yaml.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"symbol": map[string]any{
						"type":        "string",
						"description": "Symbol to monitor: BTCUSDT, BTC/USDT, ETH/USDT-PERP",
					},
					"venue": map[string]any{
						"type":        "string",
						"description": "Venue: binance_spot or binance_futures (default: binance_futures)",
					},
					"reason": map[string]any{
						"type":        "string",
						"description": "Reason for adding this symbol (optional)",
					},
				},
				"required": []string{"symbol"},
			},
		},
	}
}

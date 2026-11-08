package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/azhar/cerebro/internal/port"
)

// ScreeningTrigger is a channel that sends screening requests to the screening agent.
type ScreeningTrigger chan string

// TriggerScreening implements the trigger_screening agent tool.
// It sends a trigger to the screening agent to run an immediate screening cycle.
func TriggerScreening(triggerCh ScreeningTrigger) port.Tool {
	return port.Tool{
		Handler: func(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
			var args struct {
				Symbol string `json:"symbol"`
			}
			if err := json.Unmarshal(input, &args); err != nil {
				return nil, fmt.Errorf("trigger_screening: bad args: %w", err)
			}

			// Normalize symbol if provided
			symbol := args.Symbol
			if symbol != "" {
				symbol = string(normalizeToolSymbol(symbol))
			}

			// Send trigger to screening agent (non-blocking)
			select {
			case triggerCh <- symbol:
				var message string
				if symbol == "" {
					message = "Screening triggered for all configured symbols"
				} else {
					message = fmt.Sprintf("Screening triggered for %s", symbol)
				}
				result := map[string]any{
					"status":  "triggered",
					"symbol":  symbol,
					"message": message,
				}
				return json.Marshal(result)
			default:
				return nil, fmt.Errorf("trigger_screening: screening agent busy or not running")
			}
		},
		Definition: port.ToolDefinition{
			Name:        "trigger_screening",
			Description: "Trigger an immediate screening run for a specific symbol or all configured symbols. The screening agent will analyze market conditions and generate trade setups on-demand instead of waiting for the scheduled cycle.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"symbol": map[string]any{
						"type":        "string",
						"description": "Symbol to screen (optional). If omitted, screens all configured symbols. Format: BTCUSDT, BTC/USDT, ETH/USDT-PERP",
					},
				},
			},
		},
	}
}

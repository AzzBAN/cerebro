package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/azhar/cerebro/internal/domain"
	"github.com/azhar/cerebro/internal/port"
	"github.com/azhar/cerebro/internal/risk"
	"github.com/google/uuid"
)

// ForceHaltTrading implements the force_halt_trading agent tool.
func ForceHaltTrading(gate *risk.Gate, audit port.AuditStore, notifiers []port.Notifier) port.Tool {
	return port.Tool{
		Handler: func(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
			var args struct {
				Mode string `json:"mode"`
			}
			if err := json.Unmarshal(input, &args); err != nil {
				return nil, fmt.Errorf("force_halt_trading: bad args: %w", err)
			}

			var mode domain.HaltMode
			switch args.Mode {
			case string(domain.HaltModePause):
				mode = domain.HaltModePause
			case string(domain.HaltModeFlatten):
				mode = domain.HaltModeFlatten
			case string(domain.HaltModePauseAndNotify):
				mode = domain.HaltModePauseAndNotify
			default:
				return nil, fmt.Errorf("force_halt_trading: unknown mode %q (valid: pause|flatten|pause_and_notify)", args.Mode)
			}

			gate.SetHalt(mode)
			slog.Warn("force_halt_trading called", "mode", mode)

			_ = audit.SaveEvent(ctx, domain.AuditEvent{
				ID:        uuid.New().String(),
				EventType: "halt",
				Actor:     "tool_call",
				Payload:   map[string]any{"mode": string(mode)},
				CreatedAt: time.Now().UTC(),
			})

			if mode == domain.HaltModePauseAndNotify || mode == domain.HaltModeFlatten {
				msg := fmt.Sprintf("[HALT] Trading halted via force_halt_trading: mode=%s", mode)
				for _, n := range notifiers {
					go func(notifier port.Notifier) {
						_ = notifier.Send(ctx, port.ChannelSystemAlerts, msg)
					}(n)
				}
			}

			return json.Marshal(map[string]any{"halted": true, "mode": string(mode)})
		},
		Definition: port.ToolDefinition{
			Name:        "force_halt_trading",
			Description: "Halt all trading immediately. Use with extreme caution.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"mode": map[string]any{
						"type":        "string",
						"description": "Halt mode: pause, flatten, or pause_and_notify",
						"enum":        []string{"pause", "flatten", "pause_and_notify"},
					},
				},
				"required": []string{"mode"},
			},
		},
	}
}

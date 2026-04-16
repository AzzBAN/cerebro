package domain

import "github.com/shopspring/decimal"

// RiskParams holds computed sizing output for a single trade.
type RiskParams struct {
	Quantity        decimal.Decimal
	StopLoss        decimal.Decimal
	TakeProfit1     decimal.Decimal
	RiskAmountQuote decimal.Decimal
}

// AuditEvent is a structured record of every operator command or system state change.
type AuditEvent struct {
	ID        string
	EventType string // command|halt|config_reload|reconcile|mismatch
	Actor     string // telegram user ID, CLI, system
	Payload   map[string]any
	CreatedAt interface{} // time.Time; using any to avoid import cycle with time in this file
}

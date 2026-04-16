package tools

import (
	"github.com/azhar/cerebro/internal/port"
	"github.com/azhar/cerebro/internal/risk"
)

// Deps holds all dependencies that tools need to operate.
// Passed once at wiring time; each tool function closes over what it needs.
type Deps struct {
	Cache          port.Cache
	TradeStore     port.TradeStore
	AgentLogStore  port.AgentLogStore
	AuditStore     port.AuditStore
	DerivativesFeed port.DerivativesFeed
	NewsFeed       port.NewsFeed
	CalendarFeed   port.CalendarFeed
	Brokers        []port.Broker
	RiskGate       *risk.Gate
	Router         interface{ Route(interface{}) } // late-bound to avoid cycle
	Notifiers      []port.Notifier
}

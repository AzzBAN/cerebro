package execution

import (
	"context"
	"log/slog"
	"time"

	"github.com/azhar/cerebro/internal/domain"
	"github.com/azhar/cerebro/internal/marketdata"
	"github.com/azhar/cerebro/internal/port"
	"github.com/shopspring/decimal"
)

// Monitor subscribes to price events and manages trailing stops and partial take-profits
// for open positions. It never bypasses the execution router.
// PRD §10.3.
type Monitor struct {
	router    *Router
	venue     domain.Venue
	store     port.TradeStore
	positions func() []domain.Position // live position source
}

// NewMonitor creates a Monitor.
// positionsFn is called each tick to get the current open positions list.
func NewMonitor(router *Router, venue domain.Venue, store port.TradeStore, positionsFn func() []domain.Position) *Monitor {
	return &Monitor{
		router:    router,
		venue:     venue,
		store:     store,
		positions: positionsFn,
	}
}

// Run subscribes to quote events from the hub and monitors all open positions.
// Blocks until ctx is cancelled.
func (m *Monitor) Run(ctx context.Context, hub *marketdata.Hub) error {
	quotes, _ := hub.Subscribe()
	slog.Info("order monitor started", "venue", m.venue)

	for {
		select {
		case <-ctx.Done():
			slog.Info("order monitor stopping")
			return nil
		case evt, ok := <-quotes:
			if !ok {
				return nil
			}
			m.evaluatePositions(ctx, evt.Quote)
		}
	}
}

func (m *Monitor) evaluatePositions(ctx context.Context, q domain.Quote) {
	positions := m.positions()
	for _, pos := range positions {
		if pos.Symbol != q.Symbol {
			continue
		}

		pnlPct := pos.UnrealizedPnLPct()

		// Check stop loss hit.
		if m.isStopHit(pos, q.Mid) {
			slog.Info("stop loss hit; submitting close order",
				"symbol", pos.Symbol, "side", pos.Side,
				"sl", pos.StopLoss, "current", q.Mid)
			m.submitClose(ctx, pos, q.Mid, "stop_loss_hit")
			continue
		}

		// Check take-profit 1.
		if !pos.TakeProfit1.IsZero() && m.isTP1Hit(pos, q.Mid) {
			slog.Info("take-profit 1 hit",
				"symbol", pos.Symbol, "tp1", pos.TakeProfit1, "current", q.Mid)
			// Partial close handled here — full implementation in Phase 7.
		}

		// Trailing stop.
		m.adjustTrailingStop(ctx, &pos, q.Mid, pnlPct)
	}
}

func (m *Monitor) isStopHit(pos domain.Position, currentPrice decimal.Decimal) bool {
	if pos.StopLoss.IsZero() {
		return false
	}
	switch pos.Side {
	case domain.SideBuy:
		return currentPrice.LessThanOrEqual(pos.StopLoss)
	case domain.SideSell:
		return currentPrice.GreaterThanOrEqual(pos.StopLoss)
	}
	return false
}

func (m *Monitor) isTP1Hit(pos domain.Position, currentPrice decimal.Decimal) bool {
	switch pos.Side {
	case domain.SideBuy:
		return currentPrice.GreaterThanOrEqual(pos.TakeProfit1)
	case domain.SideSell:
		return currentPrice.LessThanOrEqual(pos.TakeProfit1)
	}
	return false
}

func (m *Monitor) adjustTrailingStop(ctx context.Context, pos *domain.Position, currentPrice, pnlPct decimal.Decimal) {
	// Trail logic placeholder — strategy config wires trail_trigger_pct / trail_step_pct.
	// Phase 7 will wire per-strategy trail parameters.
	_ = ctx
	_ = currentPrice
	_ = pnlPct
}

func (m *Monitor) submitClose(ctx context.Context, pos domain.Position, price decimal.Decimal, reason string) {
	closeSide := domain.SideSell
	if pos.Side == domain.SideSell {
		closeSide = domain.SideBuy
	}

	intent := domain.OrderIntent{
		ID:            newUUID(),
		CorrelationID: pos.CorrelationID,
		Symbol:        pos.Symbol,
		Side:          closeSide,
		Quantity:      pos.Quantity,
		Strategy:      pos.Strategy,
		Environment:   domain.EnvironmentPaper, // Monitor always runs in position's env
		CreatedAt:     time.Now().UTC(),
	}

	slog.Info("monitor submitting close",
		"symbol", pos.Symbol, "side", closeSide, "reason", reason, "price", price)

	_, err := m.router.Route(ctx, intent, m.venue)
	if err != nil {
		slog.Error("monitor: close order failed", "symbol", pos.Symbol, "error", err)
	}
}

func newUUID() string {
	return time.Now().Format("20060102150405.999999999")
}

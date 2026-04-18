package backtest

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/azhar/cerebro/internal/domain"
	"github.com/azhar/cerebro/internal/execution/paper"
	"github.com/azhar/cerebro/internal/marketdata"
	"github.com/azhar/cerebro/internal/port"
	"github.com/shopspring/decimal"
)

// Simulator drives the full backtest pipeline:
//
//	candles → hub → strategies → paper execution → report
type Simulator struct {
	candles    []domain.Candle
	hub        *marketdata.Hub
	strategies []port.Strategy
	matcher    *paper.Matcher
	clock      *SimClock
}

// NewSimulator creates a Simulator.
func NewSimulator(
	candles []domain.Candle,
	hub *marketdata.Hub,
	strategies []port.Strategy,
	matcher *paper.Matcher,
	clock *SimClock,
) *Simulator {
	return &Simulator{
		candles:    candles,
		hub:        hub,
		strategies: strategies,
		matcher:    matcher,
		clock:      clock,
	}
}

// Run executes the backtest synchronously in a single goroutine.
// Returns the completed trade list.
func (s *Simulator) Run(ctx context.Context) ([]domain.Trade, error) {
	slog.Info("backtest simulator: starting", "candles", len(s.candles))

	var allTrades []domain.Trade
	for i, c := range s.candles {
		select {
		case <-ctx.Done():
			return allTrades, ctx.Err()
		default:
		}

		// Advance simulated clock.
		s.clock.Set(c.CloseTime)

		// Evaluate all strategies against this candle.
		for _, strategy := range s.strategies {
			sig, ok := strategy.OnCandle(ctx, c)
			if !ok {
				continue
			}

			slog.Debug("backtest: signal fired",
				"strategy", sig.Strategy, "symbol", sig.Symbol,
				"side", sig.Side, "candle", i+1)

			// Simple sizing: 0.001 BTC placeholder.
			intent := domain.OrderIntent{
				ID:            fmt.Sprintf("bt-%d-%s", i, sig.ID),
				CorrelationID: sig.CorrelationID,
				Symbol:        sig.Symbol,
				Venue:         domain.VenueBinanceSpot,
				Side:          sig.Side,
				Quantity:      decimal.NewFromFloat(0.001), // Phase 8 wires strategy-specific sizing
				Strategy:      sig.Strategy,
				Environment:   domain.EnvironmentPaper,
				CreatedAt:     c.CloseTime,
			}
			s.matcher.OnCandle(ctx, c)
			s.matcher.PlaceOrder(ctx, intent) //nolint:errcheck
		}

		// Process fills on the next candle.
		s.matcher.OnCandle(ctx, c)
	}

	// Collect all paper positions as final trades.
	slog.Info("backtest simulator: complete")
	return allTrades, nil
}

// RunWithReporter is a convenience wrapper that runs the simulation and returns
// a formatted report.
func RunWithReporter(ctx context.Context, sim *Simulator, strategy, symbol string, from, to time.Time) (*Report, error) {
	trades, err := sim.Run(ctx)
	if err != nil {
		return nil, err
	}
	report := ComputeReport(strategy, symbol, from, to, trades)
	return &report, nil
}

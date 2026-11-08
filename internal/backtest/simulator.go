package backtest

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	agentpkg "github.com/azhar/cerebro/internal/agent"
	"github.com/azhar/cerebro/internal/domain"
	"github.com/azhar/cerebro/internal/execution/paper"
	"github.com/azhar/cerebro/internal/marketdata"
	"github.com/azhar/cerebro/internal/port"
	"github.com/azhar/cerebro/internal/risk"
	"github.com/azhar/cerebro/internal/strategy"
	"github.com/shopspring/decimal"
)

// Simulator drives the full backtest pipeline:
//
//	candles → hub → strategies → dedup → risk gate → agent decision → paper execution → report
type Simulator struct {
	candles    []domain.Candle
	hub        *marketdata.Hub
	strategies []port.Strategy
	matcher    *paper.Matcher
	clock      *SimClock

	store       port.TradeStore
	gate        *risk.Gate
	dedup       *strategy.DedupWindow
	riskAgent   *agentpkg.RiskAgent
	defaultQty  decimal.Decimal
	dedupWindow time.Duration
	bracketFn   BracketResolver
	tracker     *DecisionTracker
}

// BracketResolver calculates stop loss and take profit for a signal.
type BracketResolver func(c domain.Candle, sig domain.Signal) (stopLoss, takeProfit decimal.Decimal)

// NewSimulator creates a Simulator.
func NewSimulator(
	candles []domain.Candle,
	hub *marketdata.Hub,
	strategies []port.Strategy,
	matcher *paper.Matcher,
	clock *SimClock,
) *Simulator {
	return &Simulator{
		candles:     candles,
		hub:         hub,
		strategies:  strategies,
		matcher:     matcher,
		clock:       clock,
		defaultQty:  decimal.NewFromFloat(0.001),
		dedupWindow: 5 * time.Minute,
	}
}

// SetTradeStore configures the TradeStore used to query trades at end of run.
func (s *Simulator) SetTradeStore(store port.TradeStore) *Simulator {
	s.store = store
	return s
}

// SetRiskGate attaches the risk gate for signal filtering.
func (s *Simulator) SetRiskGate(g *risk.Gate) *Simulator {
	s.gate = g
	return s
}

// SetDedup attaches the signal deduplication tracker.
func (s *Simulator) SetDedup(d *strategy.DedupWindow) *Simulator {
	s.dedup = d
	return s
}

// SetRiskAgent attaches the risk agent for AI decision-making.
func (s *Simulator) SetRiskAgent(a *agentpkg.RiskAgent) *Simulator {
	s.riskAgent = a
	return s
}
// HasRiskAgent reports whether a risk agent has been attached.
func (s *Simulator) HasRiskAgent() bool {
	return s.riskAgent != nil
}

// SetDecisionTracker records step-by-step agent decisions for post-test audit.
func (s *Simulator) SetDecisionTracker(t *DecisionTracker) *Simulator {
	s.tracker = t
	return s
}

// SetDefaultQuantity configures order quantity when sizing is not specified.
func (s *Simulator) SetDefaultQuantity(q decimal.Decimal) *Simulator {
	s.defaultQty = q
	return s
}

// SetDedupWindow configures the deduplication window duration.
func (s *Simulator) SetDedupWindow(w time.Duration) *Simulator {
	s.dedupWindow = w
	return s
}

// SetBracketResolver attaches a custom bracket price calculator.
func (s *Simulator) SetBracketResolver(fn BracketResolver) *Simulator {
	s.bracketFn = fn
	return s
}

// Run executes the backtest synchronously in a single goroutine.
// Returns the completed trade list.
func (s *Simulator) Run(ctx context.Context) ([]domain.Trade, error) {
	slog.Info("backtest simulator: starting", "candles", len(s.candles))

	for i, c := range s.candles {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		// Advance simulated clock.
		if s.clock != nil {
			s.clock.Set(c.CloseTime)
		}

		// 1. Process fills on open and evaluate brackets on high/low for this candle.
		s.matcher.OnCandle(ctx, c)

		// 2. Evaluate all strategies against this closed candle.
		for _, strat := range s.strategies {
			sig, ok := strat.OnCandle(ctx, c)
			if !ok {
				continue
			}

			slog.Debug("backtest: signal fired",
				"strategy", sig.Strategy, "symbol", sig.Symbol,
				"side", sig.Side, "candle", i+1)

			// 3. Deduplication check.
			if s.dedup != nil {
				win := s.dedupWindow
				if win <= 0 {
					win = 5 * time.Minute
				}
				if !s.dedup.Allow(sig, win) {
					slog.Debug("backtest: signal deduplicated", "signal", sig.ID)
					continue
				}
			}

			// 4. Risk Gate check.
			positions, _ := s.matcher.Positions(ctx)
			if s.gate != nil {
				if err := s.gate.Check(ctx, sig, positions); err != nil {
					slog.Debug("backtest: signal rejected by risk gate", "error", err)
					if s.tracker != nil {
						price, _ := c.Close.Float64()
						s.tracker.Record(AgentDecisionRecord{
							Timestamp:    c.CloseTime,
							Symbol:       string(sig.Symbol),
							Strategy:     string(sig.Strategy),
							SignalSide:   string(sig.Side),
							Price:        price,
							SignalReason: sig.Reason,
							Action:       "rejected",
							Rationale:    fmt.Sprintf("Risk gate rejected: %v", err),
							Verdict:      "REJECTED",
						})
					}
					continue
				}
			}

			// 5. Risk Agent evaluation (AI decision layer).
			ordersBefore := len(s.matcher.OpenOrders())
			var intentID string
	if s.riskAgent != nil {
		price, _ := c.Close.Float64()
		approved, err := s.riskAgent.Evaluate(ctx, sig, positions)
		isRouted := errors.Is(err, agentpkg.ErrOrderAlreadyRouted)
		action := "approved"
		verdict := "PENDING"
		rationale := "Agent approved signal execution"
		if !approved || (err != nil && !isRouted) {
			action = "rejected"
			verdict = "REJECTED"
			rationale = "Agent declined signal"
			if err != nil {
				rationale = fmt.Sprintf("Agent evaluation error: %v", err)
			}
		}

				if openOrders := s.matcher.OpenOrders(); len(openOrders) > ordersBefore {
					intentID = openOrders[len(openOrders)-1].Intent.ID
				}

				if s.tracker != nil {
					s.tracker.Record(AgentDecisionRecord{
						Timestamp:    c.CloseTime,
						Symbol:       string(sig.Symbol),
						Strategy:     string(sig.Strategy),
						SignalSide:   string(sig.Side),
						Price:        price,
						SignalReason: sig.Reason,
						Action:       action,
						Rationale:    rationale,
						Verdict:      verdict,
						IntentID:     intentID,
					})
				}
		if !approved || (err != nil && !isRouted) {
			slog.Debug("backtest: signal rejected by risk agent", "error", err)
			continue
		}
				// If the agent tool already routed the order (day-to-day Cerebro flow),
				// avoid duplicate order placement below.
				if intentID != "" {
					continue
				}
			}

			// 6. Sizing and paper order placement.
			qty := s.defaultQty
			if qty.IsZero() {
				// ponytail: fixed 0.001 qty fallback; add dynamic portfolio sizing when multi-asset allocator ready.
				qty = decimal.NewFromFloat(0.001)
			}

			intent := domain.OrderIntent{
				ID:            fmt.Sprintf("bt-%d-%s", i, sig.ID),
				CorrelationID: sig.CorrelationID,
				Symbol:        sig.Symbol,
				Venue:         domain.VenueBinanceSpot,
				Side:          sig.Side,
				Quantity:      qty,
				Strategy:      sig.Strategy,
				Environment:   domain.EnvironmentPaper,
				CreatedAt:     c.CloseTime,
			}
			if _, err := s.matcher.PlaceOrder(ctx, intent); err != nil {
				slog.Warn("backtest: place order failed", "error", err)
				continue
			}
			if s.tracker != nil && s.riskAgent == nil {
				price, _ := c.Close.Float64()
				s.tracker.Record(AgentDecisionRecord{
					Timestamp:    c.CloseTime,
					Symbol:       string(sig.Symbol),
					Strategy:     string(sig.Strategy),
					SignalSide:   string(sig.Side),
					Price:        price,
					SignalReason: sig.Reason,
					Action:       "approved",
					Rationale:    "Signal executed directly (agent off)",
					Verdict:      "PENDING",
					IntentID:     intent.ID,
				})
			}

			// Attach protective bracket if resolver is configured.
			if s.bracketFn != nil {
				sl, tp := s.bracketFn(c, sig)
				if !sl.IsZero() {
					bracketReq := domain.BracketRequest{
						ParentIntentID: intent.ID,
						CorrelationID:  intent.CorrelationID,
						Symbol:         intent.Symbol,
						Venue:          intent.Venue,
						Side:           intent.Side,
						Quantity:       intent.Quantity,
						StopLoss:       sl,
						TakeProfit:     tp,
					}
					_, _ = s.matcher.PlaceBracket(ctx, bracketReq)
				}
			}
		}
	}

	slog.Info("backtest simulator: complete")

	if s.store != nil {
		from := time.Time{}
		to := time.Now().Add(100 * 365 * 24 * time.Hour)
		if len(s.candles) > 0 {
			from = s.candles[0].OpenTime.Add(-time.Hour)
			to = s.candles[len(s.candles)-1].CloseTime.Add(time.Hour)
		}
		trades, err := s.store.TradesByWindow(ctx, from, to)
		if err == nil && s.tracker != nil {
			s.tracker.CorrelateWithTrades(trades)
		}
		return trades, err
	}
	return nil, nil
}

// RunWithReporter is a convenience wrapper that runs the simulation and returns
// a formatted report.
func RunWithReporter(ctx context.Context, sim *Simulator, strategy, symbol string, from, to time.Time) (*Report, error) {
	trades, err := sim.Run(ctx)
	if err != nil {
		return nil, err
	}
	if from.IsZero() && len(sim.candles) > 0 {
		from = sim.candles[0].OpenTime
	}
	if to.IsZero() && len(sim.candles) > 0 {
		to = sim.candles[len(sim.candles)-1].CloseTime
	}
	report := ComputeReport(strategy, symbol, from, to, trades, len(sim.candles))
	return &report, nil
}

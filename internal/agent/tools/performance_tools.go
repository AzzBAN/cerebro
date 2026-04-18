package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/azhar/cerebro/internal/domain"
	"github.com/azhar/cerebro/internal/port"
	"github.com/shopspring/decimal"
)

// StrategyPerformance holds aggregated performance metrics for a strategy+symbol pair.
type StrategyPerformance struct {
	Strategy            string          `json:"strategy"`
	Symbol              string          `json:"symbol"`
	TotalTrades         int             `json:"total_trades"`
	WinRate             float64         `json:"win_rate"`
	AvgPnL              string          `json:"avg_pnl"`
	ConsecutiveLosses   int             `json:"consecutive_losses"`
	NetPnL              string          `json:"net_pnl"`
	ConsecutiveLossesEnd *time.Time     `json:"consecutive_losses_end,omitempty"`
}

// GetStrategyPerformance returns a tool that queries recent trade performance
// aggregated by strategy and symbol.
func GetStrategyPerformance(trades port.TradeStore) port.Tool {
	return port.Tool{
		Handler: func(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
			var args struct {
				LookbackDays int `json:"lookback_days"`
			}
			if err := json.Unmarshal(input, &args); err != nil {
				return nil, fmt.Errorf("get_strategy_performance: bad args: %w", err)
			}
			if args.LookbackDays <= 0 {
				args.LookbackDays = 30
			}

			from := time.Now().UTC().AddDate(0, 0, -args.LookbackDays)
			to := time.Now().UTC()

			recentTrades, err := trades.TradesByWindow(ctx, from, to)
			if err != nil {
				return nil, fmt.Errorf("get_strategy_performance: query trades: %w", err)
			}

			perf := AggregatePerformance(recentTrades)
			return json.Marshal(perf)
		},
		Definition: port.ToolDefinition{
			Name:        "get_strategy_performance",
			Description: "Get per-strategy win rates, PnL, and consecutive loss streaks from recent trade history.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"lookback_days": map[string]any{
						"type":        "integer",
						"description": "Number of days to look back (default 30)",
					},
				},
				"required": []string{},
			},
		},
	}
}

type strategyKey struct {
	strategy string
	symbol   string
}

type perfAccum struct {
	wins     int
	losses   int
	totalPnL decimal.Decimal
	trades   []domain.Trade
}

func AggregatePerformance(trades []domain.Trade) []StrategyPerformance {
	accums := make(map[strategyKey]*perfAccum)

	for _, t := range trades {
		if t.PnL == nil {
			continue
		}
		key := strategyKey{strategy: string(t.Strategy), symbol: string(t.Symbol)}
		acc, ok := accums[key]
		if !ok {
			acc = &perfAccum{}
			accums[key] = acc
		}
		acc.totalPnL = acc.totalPnL.Add(*t.PnL)
		acc.trades = append(acc.trades, t)
		if t.PnL.IsPositive() {
			acc.wins++
		} else {
			acc.losses++
		}
	}

	result := make([]StrategyPerformance, 0, len(accums))
	for key, acc := range accums {
		total := acc.wins + acc.losses
		winRate := 0.0
		if total > 0 {
			winRate = float64(acc.wins) / float64(total) * 100
		}
		avgPnL := decimal.Zero
		if total > 0 {
			avgPnL = acc.totalPnL.Div(decimal.NewFromInt(int64(total)))
		}

		consec, consecEnd := countConsecutiveLosses(acc.trades)

		result = append(result, StrategyPerformance{
			Strategy:            key.strategy,
			Symbol:              key.symbol,
			TotalTrades:         total,
			WinRate:             winRate,
			AvgPnL:              avgPnL.StringFixed(4),
			NetPnL:              acc.totalPnL.StringFixed(4),
			ConsecutiveLosses:   consec,
			ConsecutiveLossesEnd: consecEnd,
		})
	}
	return result
}

func countConsecutiveLosses(trades []domain.Trade) (int, *time.Time) {
	maxStreak := 0
	currentStreak := 0
	var streakEnd *time.Time

	for i := len(trades) - 1; i >= 0; i-- {
		t := trades[i]
		if t.PnL == nil {
			continue
		}
		if t.PnL.IsNegative() {
			currentStreak++
			if currentStreak > maxStreak {
				maxStreak = currentStreak
				endTime := t.CreatedAt
				streakEnd = &endTime
			}
		} else {
			currentStreak = 0
		}
	}
	return maxStreak, streakEnd
}

// FormatPerformanceContext produces a human-readable summary of strategy performance
// suitable for prepending to agent prompts.
func FormatPerformanceContext(perf []StrategyPerformance) string {
	if len(perf) == 0 {
		return "No recent trade history available."
	}

	result := "Recent strategy performance:\n"
	for _, p := range perf {
		result += fmt.Sprintf("- %s %s: %d trades, %.0f%% win rate, net PnL %s USDT",
			p.Strategy, p.Symbol, p.TotalTrades, p.WinRate, p.NetPnL)
		if p.ConsecutiveLosses >= 3 {
			result += fmt.Sprintf(" [WARNING: %d consecutive losses]", p.ConsecutiveLosses)
		}
		result += "\n"
	}
	result += "\nUse this data to weight your bias. Strategies with declining win rates suggest weakening market conditions for that style."
	return result
}

package backtest

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"time"

	"github.com/azhar/cerebro/internal/domain"
	"github.com/shopspring/decimal"
)

// Report holds all computed backtest performance metrics.
type Report struct {
	Strategy    string    `json:"strategy"`
	Symbol      string    `json:"symbol"`
	From        time.Time `json:"from"`
	To          time.Time `json:"to"`
	TotalTrades int       `json:"total_trades"`
	WinRate     float64   `json:"win_rate_pct"`
	MaxDrawdown float64   `json:"max_drawdown_pct"`
	SharpeRatio float64   `json:"sharpe_ratio"`
	ProfitFactor float64  `json:"profit_factor"`
	NetPnL      float64   `json:"net_pnl_usdt"`
	AvgWinUSDT  float64   `json:"avg_win_usdt"`
	AvgLossUSDT float64   `json:"avg_loss_usdt"`
	GeneratedAt time.Time `json:"generated_at"`
}

// ComputeReport calculates all metrics from a slice of completed trades.
func ComputeReport(strategy, symbol string, from, to time.Time, trades []domain.Trade) Report {
	r := Report{
		Strategy:    strategy,
		Symbol:      symbol,
		From:        from,
		To:          to,
		TotalTrades: len(trades),
		GeneratedAt: time.Now().UTC(),
	}
	if len(trades) == 0 {
		return r
	}

	var wins, losses int
	var grossProfit, grossLoss float64
	var pnlSeries []float64
	var equity float64
	var peakEquity float64
	var maxDrawdown float64

	for _, t := range trades {
		if t.PnL == nil {
			continue
		}
		pnl, _ := t.PnL.Float64()
		r.NetPnL += pnl
		pnlSeries = append(pnlSeries, pnl)
		equity += pnl
		if equity > peakEquity {
			peakEquity = equity
		}
		drawdown := peakEquity - equity
		if drawdown > maxDrawdown {
			maxDrawdown = drawdown
		}

		if pnl > 0 {
			wins++
			grossProfit += pnl
			r.AvgWinUSDT += pnl
		} else {
			losses++
			grossLoss += math.Abs(pnl)
			r.AvgLossUSDT += math.Abs(pnl)
		}
	}

	total := wins + losses
	if total > 0 {
		r.WinRate = float64(wins) / float64(total) * 100
		r.AvgWinUSDT /= float64(wins + 1)
		r.AvgLossUSDT /= float64(losses + 1)
	}
	if grossLoss > 0 {
		r.ProfitFactor = grossProfit / grossLoss
	}
	if peakEquity > 0 {
		r.MaxDrawdown = maxDrawdown / peakEquity * 100
	}
	r.SharpeRatio = computeSharpe(pnlSeries)

	return r
}

// Save writes the report as JSON to the given path. Prints to stdout if path is empty.
func (r Report) Save(path string) error {
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return fmt.Errorf("report: marshal: %w", err)
	}
	if path == "" {
		fmt.Println(string(data))
		return nil
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("report: write %s: %w", path, err)
	}
	return nil
}

// Print renders a human-readable summary to stdout.
func (r Report) Print() {
	fmt.Printf("\n═══════════════════════════════════════\n")
	fmt.Printf("  CEREBRO BACKTEST REPORT\n")
	fmt.Printf("═══════════════════════════════════════\n")
	fmt.Printf("  Strategy    : %s\n", r.Strategy)
	fmt.Printf("  Symbol      : %s\n", r.Symbol)
	fmt.Printf("  Period      : %s → %s\n", r.From.Format("2006-01-02"), r.To.Format("2006-01-02"))
	fmt.Printf("───────────────────────────────────────\n")
	fmt.Printf("  Total Trades: %d\n", r.TotalTrades)
	fmt.Printf("  Win Rate    : %.1f%%\n", r.WinRate)
	fmt.Printf("  Net PnL     : %.2f USDT\n", r.NetPnL)
	fmt.Printf("  Max Drawdown: %.2f%%\n", r.MaxDrawdown)
	fmt.Printf("  Sharpe Ratio: %.2f\n", r.SharpeRatio)
	fmt.Printf("  Profit Factor: %.2f\n", r.ProfitFactor)
	fmt.Printf("  Avg Win     : %.2f USDT\n", r.AvgWinUSDT)
	fmt.Printf("  Avg Loss    : %.2f USDT\n", r.AvgLossUSDT)
	fmt.Printf("═══════════════════════════════════════\n\n")
}

// computeSharpe calculates the annualised Sharpe Ratio assuming daily returns.
// Uses risk-free rate = 0 (appropriate for crypto).
func computeSharpe(pnl []float64) float64 {
	if len(pnl) < 2 {
		return 0
	}
	mean := 0.0
	for _, p := range pnl {
		mean += p
	}
	mean /= float64(len(pnl))

	variance := 0.0
	for _, p := range pnl {
		diff := p - mean
		variance += diff * diff
	}
	variance /= float64(len(pnl) - 1)
	stdDev := math.Sqrt(variance)

	if stdDev == 0 {
		return 0
	}
	return mean / stdDev * math.Sqrt(252)
}

// ensure decimal import used
var _ = decimal.Zero

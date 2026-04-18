package port

import (
	"context"

	"github.com/azhar/cerebro/internal/domain"
)

// Strategy emits trade signals from market data events.
// Implementations must be non-blocking; heavy computation runs in the
// goroutine already provided by the market data hub.
type Strategy interface {
	Name() domain.StrategyName

	// OnCandle is called for each new closed candle on the strategy's subscribed symbols.
	// Returns (signal, true) when a signal fires, (zero, false) otherwise.
	OnCandle(ctx context.Context, c domain.Candle) (domain.Signal, bool)

	// Warmup feeds historical candles to prime internal indicators (RSI, EMA, etc.)
	// so the strategy can emit signals immediately once live data arrives.
	// Signals produced during warmup are discarded.
	Warmup(ctx context.Context, candles []domain.Candle)

	// Symbols returns the list of symbols this strategy monitors.
	Symbols() []domain.Symbol

	// Timeframes returns the candle timeframes this strategy subscribes to.
	Timeframes() []domain.Timeframe
}

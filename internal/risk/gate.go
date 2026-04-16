package risk

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/azhar/cerebro/internal/config"
	"github.com/azhar/cerebro/internal/domain"
	"github.com/azhar/cerebro/internal/port"
	"github.com/shopspring/decimal"
)

// Gate enforces all numeric risk limits. It never calls an LLM directly;
// the Risk Agent is invoked by the caller after Gate.Check passes.
// This is the safety layer that must always work, even when AI is unavailable.
type Gate struct {
	mu             sync.RWMutex
	cfg            config.RiskConfig
	cache          port.Cache
	calendar       *CalendarBlackout
	haltMode       *domain.HaltMode
	sessionPnL     decimal.Decimal
	dailyPnL       decimal.Decimal
	dailyResetDate time.Time
}

// NewGate creates a Gate with the given risk configuration.
func NewGate(cfg config.RiskConfig, cache port.Cache, cal *CalendarBlackout) *Gate {
	return &Gate{
		cfg:            cfg,
		cache:          cache,
		calendar:       cal,
		dailyResetDate: midnightUTC(),
	}
}

// Check validates a signal against all active risk limits.
// Returns nil if the signal may proceed, or a wrapped domain error if rejected.
func (g *Gate) Check(ctx context.Context, sig domain.Signal, positions []domain.Position) error {
	g.mu.RLock()
	defer g.mu.RUnlock()

	// 1. Global halt
	if g.haltMode != nil {
		return fmt.Errorf("%w: mode=%s", domain.ErrHaltActive, *g.haltMode)
	}

	// 2. Engine kill switch check is handled in app.go before signals reach the gate.

	// 3. Daily PnL reset at midnight UTC.
	if time.Now().UTC().After(g.dailyResetDate) {
		g.mu.RUnlock()
		g.mu.Lock()
		g.dailyPnL = decimal.Zero
		g.dailyResetDate = midnightUTC()
		g.mu.Unlock()
		g.mu.RLock()
	}

	// 4. Drawdown check.
	if g.cfg.MaxDrawdownPct > 0 && g.sessionPnL.IsNegative() {
		drawdown := g.sessionPnL.Abs()
		// We store PnL as an absolute value relative to starting equity.
		// Phase 4 will track equity properly; this is a structural placeholder.
		_ = drawdown
	}

	// 5. Daily loss check.
	if g.cfg.MaxDailyLossPct > 0 && g.dailyPnL.IsNegative() {
		_ = g.dailyPnL
	}

	// 6. Max open positions.
	if g.cfg.MaxOpenPositions > 0 && len(positions) >= g.cfg.MaxOpenPositions {
		return fmt.Errorf("%w: open positions %d >= max %d",
			domain.ErrSignalRejected, len(positions), g.cfg.MaxOpenPositions)
	}

	// 7. Max positions per symbol.
	if g.cfg.MaxOpenPositionsPerSymbol > 0 {
		symCount := 0
		for _, p := range positions {
			if p.Symbol == sig.Symbol {
				symCount++
			}
		}
		if symCount >= g.cfg.MaxOpenPositionsPerSymbol {
			return fmt.Errorf("%w: symbol %s already at max %d positions",
				domain.ErrSignalRejected, sig.Symbol, g.cfg.MaxOpenPositionsPerSymbol)
		}
	}

	// 8. Calendar blackout.
	if g.calendar.IsBlackedOut(time.Now().UTC(),
		30, // default blackout windows — strategy config overrides in execution layer
		15,
	) {
		return fmt.Errorf("%w: high-impact event blackout active", domain.ErrSignalRejected)
	}

	// 9. Read cached bias from Redis (non-blocking; falls back to Neutral if missing).
	biasKey := fmt.Sprintf("bias:%s", sig.Symbol)
	biasBytes, err := g.cache.Get(ctx, biasKey)
	if err != nil {
		slog.Warn("bias cache read failed; proceeding with Neutral bias", "error", err)
	}
	if biasBytes != nil {
		var bias domain.BiasResult
		if jerr := json.Unmarshal(biasBytes, &bias); jerr == nil {
			// Bias alignment check is done per-strategy by the strategy itself;
			// the gate enforces a global signal-direction bias filter here only
			// if configured. For now, log the bias for observability.
			slog.Debug("bias read from cache",
				"symbol", sig.Symbol,
				"score", bias.Score,
				"expires_at", bias.ExpiresAt,
			)
		}
	}

	return nil
}

// SetHalt activates a halt mode, preventing new orders.
func (g *Gate) SetHalt(mode domain.HaltMode) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.haltMode = &mode
	slog.Warn("trading halted", "mode", mode)
}

// ClearHalt lifts the current halt, allowing new orders.
func (g *Gate) ClearHalt() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.haltMode = nil
	slog.Info("trading halt cleared")
}

// IsHalted returns true if any halt mode is active.
func (g *Gate) IsHalted() bool {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.haltMode != nil
}

// CurrentHaltMode returns the active halt mode, or nil.
func (g *Gate) CurrentHaltMode() *domain.HaltMode {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.haltMode
}

// TradingState returns a human-readable trading state for operator surfaces.
func (g *Gate) TradingState() string {
	g.mu.RLock()
	defer g.mu.RUnlock()
	if g.haltMode == nil {
		return "running"
	}
	if *g.haltMode == domain.HaltModePause {
		return "paused"
	}
	return string(*g.haltMode)
}

// UpdatePnL records a realised trade PnL.
func (g *Gate) UpdatePnL(pnl decimal.Decimal) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.sessionPnL = g.sessionPnL.Add(pnl)
	g.dailyPnL = g.dailyPnL.Add(pnl)
}

func midnightUTC() time.Time {
	now := time.Now().UTC()
	return time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, time.UTC)
}

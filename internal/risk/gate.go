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
	startingEquity decimal.Decimal
	sessionPnL     decimal.Decimal
	dailyPnL       decimal.Decimal
	dailyResetDate time.Time

	// allowedSymbols is the closed universe of executable symbols (taken
	// from markets.yaml at startup). When non-empty, signals on symbols
	// outside this set are rejected. Empty = no allow-list (e.g. tests).
	allowedSymbols map[domain.Symbol]struct{}
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

// SetAllowedSymbols installs the executable-symbol allow-list. Signals on
// symbols not in this set will be rejected with ErrSignalRejected. Pass
// nil/empty to disable the check (used by tests and discovery-disabled
// deployments where the markets.yaml universe is the only source).
//
// Safe to call once at startup; not safe to call concurrently with Check.
func (g *Gate) SetAllowedSymbols(syms []domain.Symbol) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(syms) == 0 {
		g.allowedSymbols = nil
		return
	}
	set := make(map[domain.Symbol]struct{}, len(syms))
	for _, s := range syms {
		set[s] = struct{}{}
	}
	g.allowedSymbols = set
}

// SetStartingEquity records the account equity at session start so that
// drawdown and daily-loss percentages can be calculated.
func (g *Gate) SetStartingEquity(equity decimal.Decimal) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.startingEquity = equity
	slog.Info("risk gate: starting equity set", "equity", equity.StringFixed(2))
}

// Check validates a signal against all active risk limits.
// Returns nil if the signal may proceed, or a wrapped domain error if rejected.
func (g *Gate) Check(ctx context.Context, sig domain.Signal, positions []domain.Position) error {
	// 1. Daily PnL reset at midnight UTC — done under write lock *before*
	//    taking the read lock to avoid the RLock→Lock promotion race.
	g.maybeResetDailyPnL()

	g.mu.RLock()
	defer g.mu.RUnlock()

	// 2. Global halt
	if g.haltMode != nil {
		return fmt.Errorf("%w: mode=%s", domain.ErrHaltActive, *g.haltMode)
	}

	// 2a. Symbol allow-list. Rejects discovery-surfaced symbols that have
	// not been promoted into markets.yaml — execution is gated to the
	// configured universe, even when the LLM proposes something else.
	if len(g.allowedSymbols) > 0 {
		if _, ok := g.allowedSymbols[sig.Symbol]; !ok {
			return fmt.Errorf("%w: symbol %s is not in markets.yaml allow-list (discovery-only)",
				domain.ErrSignalRejected, sig.Symbol)
		}
	}

	// 3. Engine kill switch check is handled in app.go before signals reach the gate.

	// 4. Drawdown check — reject if session loss exceeds configured max.
	if g.cfg.MaxDrawdownPct > 0 && g.sessionPnL.IsNegative() && g.startingEquity.IsPositive() {
		drawdownPct := g.sessionPnL.Abs().Div(g.startingEquity).Mul(decimal.NewFromInt(100))
		limit := decimal.NewFromFloat(g.cfg.MaxDrawdownPct)
		if drawdownPct.GreaterThanOrEqual(limit) {
			return fmt.Errorf("%w: session drawdown %.2f%% >= max %.2f%%",
				domain.ErrSignalRejected, drawdownPct.InexactFloat64(), g.cfg.MaxDrawdownPct)
		}
	}

	// 5. Daily loss check — reject if today's realised loss exceeds configured max.
	if g.cfg.MaxDailyLossPct > 0 && g.dailyPnL.IsNegative() && g.startingEquity.IsPositive() {
		dailyLossPct := g.dailyPnL.Abs().Div(g.startingEquity).Mul(decimal.NewFromInt(100))
		limit := decimal.NewFromFloat(g.cfg.MaxDailyLossPct)
		if dailyLossPct.GreaterThanOrEqual(limit) {
			return fmt.Errorf("%w: daily loss %.2f%% >= max %.2f%%",
				domain.ErrSignalRejected, dailyLossPct.InexactFloat64(), g.cfg.MaxDailyLossPct)
		}
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

// maybeResetDailyPnL resets daily PnL at midnight UTC using a proper
// double-check pattern under a write lock (avoiding the RLock→Lock race).
func (g *Gate) maybeResetDailyPnL() {
	g.mu.RLock()
	needsReset := time.Now().UTC().After(g.dailyResetDate)
	g.mu.RUnlock()
	if !needsReset {
		return
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	// Double-check under write lock.
	if time.Now().UTC().After(g.dailyResetDate) {
		g.dailyPnL = decimal.Zero
		g.dailyResetDate = midnightUTC()
		slog.Info("risk gate: daily PnL reset at midnight UTC")
	}
}

func midnightUTC() time.Time {
	now := time.Now().UTC()
	return time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, time.UTC)
}

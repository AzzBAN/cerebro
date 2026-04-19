package chatops

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/azhar/cerebro/internal/domain"
	"github.com/azhar/cerebro/internal/port"
	"github.com/azhar/cerebro/internal/risk"
)

// Deps holds all external dependencies the dispatcher needs.
type Deps struct {
	RiskGate    *risk.Gate
	Cache       port.Cache
	Brokers     []port.Broker
	AuditStore  port.AuditStore
	CopilotFn   func(ctx context.Context, query string) (string, error)
	AllowlistFn func(actorID string) bool
}

// Dispatcher is the unified command router for CLI, Telegram, and Discord.
// It enforces operator allowlist checking, permission validation, and audit logging.
type Dispatcher struct {
	deps              Deps
	pendingFlattenAt  *time.Time // non-nil when awaiting /flatten confirmation
	confirmTimeoutS   int
}

// New creates a Dispatcher.
func New(deps Deps, flattenConfirmTimeoutS int) *Dispatcher {
	return &Dispatcher{deps: deps, confirmTimeoutS: flattenConfirmTimeoutS}
}

// Dispatch processes a raw command string from any operator surface.
// Returns the reply text to send back to the operator.
func (d *Dispatcher) Dispatch(ctx context.Context, actorID, raw string) string {
	// Operator allowlist check.
	if d.deps.AllowlistFn != nil && !d.deps.AllowlistFn(actorID) {
		slog.Warn("chatops: unauthorised actor", "actor", actorID)
		logCommand(ctx, d.deps.AuditStore, actorID, "DENIED", raw, "unauthorised")
		return "❌ Access denied."
	}

	cmd, arg := ParseCommand(raw)
	if cmd == "" {
		return "Unknown command. Try /status, /positions, /ask <query>, /pause, /resume, /flatten, /bias <symbol>, /summary."
	}

	result := d.handle(ctx, actorID, cmd, arg)
	logCommand(ctx, d.deps.AuditStore, actorID, cmd, arg, result)
	return result
}

func (d *Dispatcher) handle(ctx context.Context, actorID, cmd, arg string) string {
	switch cmd {
	case CmdStatus:
		return d.handleStatus(ctx)

	case CmdPositions:
		return d.handlePositions(ctx)

	case CmdBias:
		return d.handleBias(ctx, strings.TrimSpace(arg))

	case CmdPause:
		d.deps.RiskGate.SetHalt(domain.HaltModePause)
		slog.Info("trading paused via ChatOps", "actor", actorID)
		return "⏸ Trading paused. Open positions are retained."

	case CmdResume:
		d.deps.RiskGate.ClearHalt()
		slog.Info("trading resumed via ChatOps", "actor", actorID)
		return "▶️ Trading resumed."

	case CmdFlatten:
		return d.handleFlatten(ctx, actorID, arg)

	case CmdAsk:
		return d.handleAsk(ctx, arg)

	case CmdSummary:
		return d.handleSummary(ctx)

	default:
		return "Unknown command."
	}
}

func (d *Dispatcher) handleStatus(ctx context.Context) string {
	halted := d.deps.RiskGate.IsHalted()
	running := !halted
	state := d.deps.RiskGate.TradingState()
	mode := ""
	if m := d.deps.RiskGate.CurrentHaltMode(); m != nil {
		mode = fmt.Sprintf(" (mode: %s)", *m)
	}

	var posCount int
	for _, b := range d.deps.Brokers {
		if positions, err := b.Positions(ctx); err == nil {
			posCount += len(positions)
		}
	}

	return fmt.Sprintf(
		"📊 Status:\n  Running: %v\n  State: %s\n  Halted: %v%s\n  Open positions: %d",
		running, state, halted, mode, posCount,
	)
}

func (d *Dispatcher) handlePositions(ctx context.Context) string {
	var lines []string
	for _, b := range d.deps.Brokers {
		positions, err := b.Positions(ctx)
		if err != nil {
			lines = append(lines, fmt.Sprintf("  [%s] error: %v", b.Venue(), err))
			continue
		}
		for _, p := range positions {
			pnl := p.UnrealizedPnLPct().StringFixed(2)
			pnlStr := pnl + "%"
			if p.UnrealizedPnLPct().IsPositive() {
				pnlStr = "+" + pnlStr
			}
			lines = append(lines, fmt.Sprintf("  %s %s  Qty: %s  Entry: %s  PnL: %s  SL: %s",
				p.Symbol, p.Side, p.Quantity.String(), p.EntryPrice.String(), pnlStr, p.StopLoss.String()))
		}
	}
	if len(lines) == 0 {
		return "📋 No open positions."
	}
	return "📋 Open Positions:\n" + strings.Join(lines, "\n")
}

func (d *Dispatcher) handleBias(ctx context.Context, symbol string) string {
	if symbol == "" {
		return "Usage: /bias <symbol>  (e.g. /bias BTCUSDT)"
	}
	key := fmt.Sprintf("bias:%s", symbol)
	b, err := d.deps.Cache.Get(ctx, key)
	if err != nil || b == nil {
		return fmt.Sprintf("⚠️ No bias cached for %s. Screening agent may not have run yet.", symbol)
	}
	var bias domain.BiasResult
	if err := json.Unmarshal(b, &bias); err != nil {
		return "⚠️ Error reading bias cache."
	}
	age := time.Since(bias.CachedAt).Round(time.Minute)
	return fmt.Sprintf("📈 Bias for %s: %s\n  Cached: %s ago\n  Expires: %s\n  Reasoning: %s",
		symbol, bias.Score, age, bias.ExpiresAt.Format("15:04 UTC"), bias.Reasoning)
}

func (d *Dispatcher) handleFlatten(ctx context.Context, actorID, arg string) string {
	now := time.Now()
	if d.pendingFlattenAt != nil && now.Sub(*d.pendingFlattenAt) < time.Duration(d.confirmTimeoutS)*time.Second {
		// Confirmation received.
		d.pendingFlattenAt = nil
		d.deps.RiskGate.SetHalt(domain.HaltModeFlatten)
		slog.Warn("flatten confirmed via ChatOps", "actor", actorID)
		return "⚠️ FLATTEN confirmed. All positions will be closed."
	}
	// First call — set confirmation timer.
	d.pendingFlattenAt = &now
	return fmt.Sprintf("⚠️ Are you sure? Send /flatten again within %ds to confirm. This will close ALL positions.", d.confirmTimeoutS)
}

func (d *Dispatcher) handleAsk(ctx context.Context, query string) string {
	if query == "" {
		return "Usage: /ask <question>"
	}
	if d.deps.CopilotFn == nil {
		return "Copilot not available (LLM not configured)."
	}
	result, err := d.deps.CopilotFn(ctx, query)
	if err != nil {
		return fmt.Sprintf("⚠️ Copilot error: %v", err)
	}
	return "🤖 " + result
}

func (d *Dispatcher) handleSummary(ctx context.Context) string {
	var sections []string

	// Trading state.
	halted := d.deps.RiskGate.IsHalted()
	state := d.deps.RiskGate.TradingState()
	statusEmoji := "🟢"
	if halted {
		statusEmoji = "🔴"
	}
	sections = append(sections, fmt.Sprintf("%s State: %s", statusEmoji, state))

	// Positions + balance across all venues.
	var totalPos int
	var posLines []string
	for _, b := range d.deps.Brokers {
		positions, err := b.Positions(ctx)
		if err != nil {
			continue
		}
		totalPos += len(positions)
		for _, p := range positions {
			pnl := p.UnrealizedPnLPct().StringFixed(2) + "%"
			if p.UnrealizedPnLPct().IsPositive() {
				pnl = "+" + pnl
			}
			posLines = append(posLines, fmt.Sprintf("  %s %s  Qty: %s  Entry: %s  PnL: %s",
				p.Symbol, p.Side, p.Quantity.String(), p.EntryPrice.String(), pnl))
		}

		bal, err := b.Balance(ctx)
		if err != nil {
			continue
		}
		sections = append(sections, fmt.Sprintf("💰 %s: %s USDT (free: %s)",
			b.Venue(), bal.TotalUSDT.StringFixed(2), bal.FreeUSDT.StringFixed(2)))
	}
	if totalPos > 0 {
		sections = append(sections, fmt.Sprintf("📊 Positions (%d):", totalPos))
		sections = append(sections, posLines...)
	} else {
		sections = append(sections, "📊 No open positions.")
	}

	// Screening summary (best-effort — may not have run yet).
	if raw, err := d.deps.Cache.Get(ctx, "screening:summary"); err == nil && raw != nil {
		sections = append(sections, "📋 Screening:\n"+string(raw))
	}

	return strings.Join(sections, "\n")
}

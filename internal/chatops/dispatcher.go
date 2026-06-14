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

// ClosePositionFn closes a single open position via the execution Router.
// Implementations MUST submit a reduce-only market order through the
// per-venue worker (preserving the single-writer invariant) and return the
// broker-assigned close order ID on success.
//
// Risk-gate exemption (intentional): reduce-only EXITS are deliberately NOT
// run through the risk gate. A position exit must never be blocked by
// position-count, notional, drawdown, or daily-loss limits — those limits
// exist to cap how much NEW risk is opened, and refusing to let an operator
// out of a position is the opposite of safety.
//
// Kill-switch exemption (intentional): closes are also allowed while the gate
// is halted (kill_switch / pause / flatten). /flatten itself sets
// HaltModeFlatten *before* issuing closes, so a kill-switch-aware close path
// would deadlock flatten-to-safety against its own halt. Keeping closes
// always-allowed is what makes emergency flattening possible. New entries
// remain blocked by the gate on both the strategy and agent paths.
type ClosePositionFn func(ctx context.Context, pos domain.Position) (string, error)

// Deps holds all external dependencies the dispatcher needs.
type Deps struct {
	RiskGate    *risk.Gate
	Cache       port.Cache
	Brokers     []port.Broker
	AuditStore  port.AuditStore
	CopilotFn   func(ctx context.Context, query string) (string, error)
	AllowlistFn func(actorID string) bool
	// CloseFn is invoked by /close and /flatten to submit reduce-only market
	// orders through the execution router. When nil, the dispatcher refuses
	// close requests with an explicit error — so a mis-wired build can't
	// silently pretend to close positions.
	CloseFn ClosePositionFn
	// ConfirmActionFn confirms a queued position-manager action by ID, executing
	// it synchronously. RejectActionFn discards a queued action by ID. Both
	// return an error when no queue owns the ID. When nil, /confirm and /reject
	// report that the position manager is not wired.
	ConfirmActionFn func(ctx context.Context, id string) error
	RejectActionFn  func(id string) error
}

// Dispatcher is the unified command router for CLI, Telegram, and Discord.
// It enforces operator allowlist checking, permission validation, and audit logging.
type Dispatcher struct {
	deps             Deps
	pendingFlattenAt *time.Time           // non-nil when awaiting /flatten confirmation
	pendingCloseAt   map[string]time.Time // symbol → timestamp for /close confirmation
	confirmTimeoutS  int
}

// New creates a Dispatcher.
func New(deps Deps, flattenConfirmTimeoutS int) *Dispatcher {
	return &Dispatcher{
		deps:            deps,
		pendingCloseAt:  make(map[string]time.Time),
		confirmTimeoutS: flattenConfirmTimeoutS,
	}
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
		return helpText()
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

	case CmdClose:
		return d.handleClose(ctx, actorID, strings.TrimSpace(arg))

	case CmdConfirm:
		return d.handleConfirm(ctx, actorID, strings.TrimSpace(arg))

	case CmdReject:
		return d.handleReject(actorID, strings.TrimSpace(arg))

	case CmdAsk:
		return d.handleAsk(ctx, arg)

	case CmdSummary:
		return d.handleSummary(ctx)

	case CmdHelp:
		return helpText()

	default:
		return "Unknown command."
	}
}

func helpText() string {
	return strings.Join([]string{
		"📖 Commands:",
		"  /status              — trading state + open-position count",
		"  /positions           — list open positions with PnL",
		"  /summary             — state + positions + balances + screening",
		"  /close <symbol>      — close a single position (requires confirm)",
		"  /flatten             — close ALL positions (requires confirm)",
		"  /confirm <action-id> — confirm a pending position-manager action",
		"  /reject <action-id>  — reject a pending position-manager action",
		"  /pause  | /resume    — halt / resume new entries",
		"  /bias <symbol>       — last cached LLM bias",
		"  /ask <question>      — ask the copilot",
		"  /help                — this message",
	}, "\n")
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

// handleFlatten implements the two-step /flatten flow:
//  1. First call arms a confirmation timer and asks the operator to confirm.
//  2. Confirmation within confirmTimeoutS submits reduce-only MARKET close
//     orders for every open position across every broker via CloseFn.
//
// The risk gate is halted in `flatten` mode so new entries are blocked
// while the closes are in flight.
func (d *Dispatcher) handleFlatten(ctx context.Context, actorID, _ string) string {
	now := time.Now()
	window := time.Duration(d.confirmTimeoutS) * time.Second

	if d.pendingFlattenAt == nil || now.Sub(*d.pendingFlattenAt) > window {
		// First call (or expired) — arm confirmation.
		d.pendingFlattenAt = &now
		return fmt.Sprintf("⚠️ Are you sure? Send /flatten again within %ds to confirm. This will close ALL open positions.", d.confirmTimeoutS)
	}

	// Confirmation received.
	d.pendingFlattenAt = nil

	if d.deps.CloseFn == nil {
		slog.Error("flatten requested but CloseFn not wired")
		return "❌ Flatten unavailable: execution router not wired."
	}

	// Halt first so no new entries slip in between the positions snapshot
	// and the close submissions.
	d.deps.RiskGate.SetHalt(domain.HaltModeFlatten)
	slog.Warn("flatten confirmed via ChatOps", "actor", actorID)

	positions := d.collectAllPositions(ctx)
	if len(positions) == 0 {
		return "ℹ️ No open positions to close. Halt mode: flatten."
	}

	closed, failures := d.closeAll(ctx, actorID, positions, "chatops_flatten")

	var msg strings.Builder
	fmt.Fprintf(&msg, "⚠️ FLATTEN submitted %d/%d close orders.\n", closed, len(positions))
	for _, f := range failures {
		fmt.Fprintf(&msg, "  ✗ %s: %s\n", f.symbol, f.err)
	}
	return strings.TrimRight(msg.String(), "\n")
}

// handleClose closes a single position matching the supplied symbol.
// Supports two-step confirmation keyed per-symbol so a pending /close BTC
// doesn't affect a subsequent /close ETH.
func (d *Dispatcher) handleClose(ctx context.Context, actorID, arg string) string {
	if arg == "" {
		return "Usage: /close <symbol>  (e.g. /close BTC/USDT)"
	}
	// Allow /close all as a friendly alias for /flatten.
	if strings.EqualFold(arg, "all") {
		return d.handleFlatten(ctx, actorID, "")
	}
	if d.deps.CloseFn == nil {
		return "❌ Close unavailable: execution router not wired."
	}

	positions := d.collectAllPositions(ctx)
	match, err := resolvePositionBySymbol(arg, positions)
	if err != nil {
		return fmt.Sprintf("❌ %s", err)
	}

	key := strings.ToUpper(string(match.Symbol))
	now := time.Now()
	window := time.Duration(d.confirmTimeoutS) * time.Second
	if at, pending := d.pendingCloseAt[key]; !pending || now.Sub(at) > window {
		d.pendingCloseAt[key] = now
		return fmt.Sprintf(
			"⚠️ Close %s %s %s? Send `/close %s` again within %ds to confirm.",
			match.Symbol, match.Side, match.Quantity.String(),
			match.Symbol, d.confirmTimeoutS,
		)
	}
	delete(d.pendingCloseAt, key)

	orderID, cerr := d.deps.CloseFn(ctx, match)
	if cerr != nil {
		slog.Error("chatops: close failed", "symbol", match.Symbol, "error", cerr)
		return fmt.Sprintf("❌ Close failed for %s: %v", match.Symbol, cerr)
	}

	slog.Warn("position closed via ChatOps",
		"actor", actorID, "symbol", match.Symbol,
		"side", match.Side, "qty", match.Quantity, "order_id", orderID)

	return fmt.Sprintf("✅ Close order submitted for %s %s %s (order_id=%s).",
		match.Symbol, match.Side, match.Quantity.String(), orderID)
}

// handleConfirm confirms a queued position-manager action by ID. The action
// queues live per-venue and the ID is a bare UUID with no venue prefix, so the
// wiring closure tries each queue and executes the one that owns the ID.
func (d *Dispatcher) handleConfirm(ctx context.Context, actorID, id string) string {
	if id == "" {
		return "Usage: /confirm <action-id>"
	}
	if d.deps.ConfirmActionFn == nil {
		return "❌ Confirm unavailable: position manager not wired."
	}
	if err := d.deps.ConfirmActionFn(ctx, id); err != nil {
		slog.Warn("chatops: confirm failed", "actor", actorID, "id", id, "error", err)
		return fmt.Sprintf("❌ Confirm failed for %s: %v", id, err)
	}
	slog.Warn("position action confirmed via ChatOps", "actor", actorID, "id", id)
	return fmt.Sprintf("✅ Action %s confirmed and executed.", id)
}

// handleReject discards a queued position-manager action by ID without
// executing it. Like /confirm, the ID is venue-agnostic so the wiring closure
// tries each queue.
func (d *Dispatcher) handleReject(actorID, id string) string {
	if id == "" {
		return "Usage: /reject <action-id>"
	}
	if d.deps.RejectActionFn == nil {
		return "❌ Reject unavailable: position manager not wired."
	}
	if err := d.deps.RejectActionFn(id); err != nil {
		slog.Warn("chatops: reject failed", "actor", actorID, "id", id, "error", err)
		return fmt.Sprintf("❌ Reject failed for %s: %v", id, err)
	}
	slog.Warn("position action rejected via ChatOps", "actor", actorID, "id", id)
	return fmt.Sprintf("🚫 Action %s rejected.", id)
}

// collectAllPositions walks every configured broker and returns a merged
// slice of live positions. Errors per broker are logged and skipped — we
// never want /close to silently refuse work because one venue hiccupped.
func (d *Dispatcher) collectAllPositions(ctx context.Context) []domain.Position {
	var out []domain.Position
	for _, b := range d.deps.Brokers {
		positions, err := b.Positions(ctx)
		if err != nil {
			slog.Warn("chatops: fetch positions failed", "venue", b.Venue(), "error", err)
			continue
		}
		out = append(out, positions...)
	}
	return out
}

// closeFailure records one leg of a /flatten that did not submit.
type closeFailure struct {
	symbol domain.Symbol
	err    string
}

// closeAll submits CloseFn for every position and returns (successes, failures).
// Failures are aggregated so the operator sees exactly which positions are
// still open after the flatten.
func (d *Dispatcher) closeAll(ctx context.Context, actorID string, positions []domain.Position, reason string) (int, []closeFailure) {
	var closed int
	var failures []closeFailure
	for _, p := range positions {
		orderID, err := d.deps.CloseFn(ctx, p)
		if err != nil {
			slog.Error("chatops: close leg failed",
				"actor", actorID, "symbol", p.Symbol, "error", err, "reason", reason)
			failures = append(failures, closeFailure{symbol: p.Symbol, err: err.Error()})
			continue
		}
		slog.Warn("chatops: close submitted",
			"actor", actorID, "symbol", p.Symbol, "side", p.Side,
			"qty", p.Quantity, "order_id", orderID, "reason", reason)
		closed++
	}
	return closed, failures
}

// resolvePositionBySymbol locates the live position that matches the
// operator's free-form symbol input. Accepts canonical forms like
// "BTC/USDT", exchange forms like "BTCUSDT", and case-insensitive input.
// Fails when no match is found OR when the input is ambiguous across venues.
func resolvePositionBySymbol(input string, positions []domain.Position) (domain.Position, error) {
	q := strings.ToUpper(strings.TrimSpace(input))
	// Strip common separators so "BTC/USDT", "BTC-USDT", "BTCUSDT" all match.
	normalised := func(s string) string {
		s = strings.ToUpper(s)
		s = strings.ReplaceAll(s, "/", "")
		s = strings.ReplaceAll(s, "-", "")
		return s
	}
	qn := normalised(q)

	var matches []domain.Position
	for _, p := range positions {
		if normalised(string(p.Symbol)) == qn {
			matches = append(matches, p)
		}
	}
	switch len(matches) {
	case 0:
		return domain.Position{}, fmt.Errorf("no open position for %q", input)
	case 1:
		return matches[0], nil
	default:
		venues := make([]string, 0, len(matches))
		for _, m := range matches {
			venues = append(venues, string(m.Venue))
		}
		return domain.Position{}, fmt.Errorf("ambiguous symbol %q (found on %s); use the canonical symbol",
			input, strings.Join(venues, ", "))
	}
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

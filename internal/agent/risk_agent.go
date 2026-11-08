package agent

import (
	"errors"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "embed"

	"github.com/azhar/cerebro/internal/agent/tools"
	"github.com/azhar/cerebro/internal/domain"
	"github.com/azhar/cerebro/internal/port"
	"github.com/shopspring/decimal"
)

// QuoteSource exposes the latest merged market quote for a symbol. The
// marketdata.Hub satisfies this implicitly via LatestQuote; tests pass a stub.
type QuoteSource interface {
	LatestQuote(sym domain.Symbol) (domain.Quote, bool)
}

// BalanceSource provides current account equity and session drawdown to the agent.
type BalanceSource interface {
	AccountEquity(ctx context.Context) (equity decimal.Decimal, drawdownPct float64, ok bool)
}
// ErrOrderAlreadyRouted indicates the order was routed by an agent tool call,
// even though the overall agent run ended with an error (e.g. timeout on final turn).
var ErrOrderAlreadyRouted = errors.New("risk agent: order already routed before invocation error")

//go:embed prompts/risk.tmpl
var riskPrompt string

// discoveryTagPattern matches the contamination tags injected by the screening
// agent's discovery pipeline (e.g. "[unconfigured — requires operator promotion]",
// "[unconfigured]"). These tags must be stripped before the risk agent sees the
// signal reason, otherwise the LLM synthesises "unconfigured routing" rejections
// even for fully configured symbols.
var discoveryTagPattern = regexp.MustCompile(`\[unconfigured[^\]]*\]`)

// RiskAgent is invoked by the risk gate on the signal path.
// It can approve, reject, or resize a signal.
// On any failure, the caller's risk gate fails closed (signal rejected).
type RiskAgent struct {
	mu             sync.RWMutex
	runtime        *Runtime
	tools          map[string]port.Tool
	trades         port.TradeStore
	cache          port.Cache
	configuredSyms []domain.Symbol // routable symbols from markets.yaml
	// quotes is an optional live-quote source (typically *marketdata.Hub).
	// When non-nil, the agent's user message is enriched with the latest
	// Last/Mid price for the signal symbol so the LLM can compute
	// stop_loss in absolute terms instead of guessing.
	quotes           QuoteSource
	balance          BalanceSource
	lastRejectReason string
}

// LastRejectReason returns the reason supplied to reject_signal on the latest evaluation.
func (r *RiskAgent) LastRejectReason() string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.lastRejectReason
}
// SetBalanceSource attaches an account balance source so the user message includes equity and drawdown.
func (r *RiskAgent) SetBalanceSource(b BalanceSource) { r.balance = b }

// SetCache attaches a cache store so the agent can read cached market bias.
func (r *RiskAgent) SetCache(c port.Cache) { r.cache = c }

// NewRiskAgent creates a RiskAgent.
func NewRiskAgent(runtime *Runtime, agentTools map[string]port.Tool) *RiskAgent {
	return &RiskAgent{runtime: runtime, tools: agentTools}
}

// NewRiskAgentWithPerf creates a RiskAgent with trade performance data access.
func NewRiskAgentWithPerf(runtime *Runtime, agentTools map[string]port.Tool, trades port.TradeStore) *RiskAgent {
	return &RiskAgent{runtime: runtime, tools: agentTools, trades: trades}
}

// SetQuoteSource attaches a live-quote source so the agent's user message
// can carry the latest mark price for the signal symbol. Optional — when nil
// the agent falls back to "no live quote available" and the LLM must decide
// without one.
func (r *RiskAgent) SetQuoteSource(q QuoteSource) { r.quotes = q }

// SetConfiguredSymbols tells the risk agent which symbols are routable
// (i.e. present in markets.yaml). When non-empty, the agent pre-filters
// signals for unconfigured symbols before invoking the LLM, preventing
// spurious "unconfigured routing" rejections caused by discovery-tag
// contamination in the signal reason field.
func (r *RiskAgent) SetConfiguredSymbols(syms []domain.Symbol) {
	r.configuredSyms = syms
}

// Evaluate asks the LLM to approve or reject the signal.
// Returns (approved, nil) on approval, (false, reason) on rejection.
// Any LLM failure returns (false, ErrAgentTimeout) — fail closed.
//
// The user message is kept small and signal-specific so that the long,
// stable system prompt (+ tool schemas + optional performance context)
// forms a cacheable prefix across successive Risk Agent invocations.
func (r *RiskAgent) Evaluate(ctx context.Context, sig domain.Signal, positions []domain.Position) (bool, error) {
	// Strip discovery-agent contamination tags from the signal reason.
	// The screening agent tags discovery-only symbols with "[unconfigured ...]",
	// which pollutes the LLM context and causes spurious rejections even for
	// fully configured symbols when the tag leaks via shared context.
	cleanReason := discoveryTagPattern.ReplaceAllString(sig.Reason, "")
	cleanReason = strings.TrimSpace(cleanReason)
	if cleanReason == "" {
		cleanReason = string(sig.Strategy)
	}

	userMsg := r.buildUserMessage(ctx, sig, cleanReason, positions)

	// Inject recent strategy performance into the system prompt (cacheable)
	// rather than the user message.
	systemPrompt := riskPrompt
	if r.trades != nil {
		systemPrompt = r.injectPerformanceContextSystem(ctx, 7, systemPrompt)
	}

	// Append configured-symbol list to the system prompt so the LLM knows
	// which symbols are routable and doesn't reject them as unknown.
	if len(r.configuredSyms) > 0 {
		symList := make([]string, len(r.configuredSyms))
		for i, s := range r.configuredSyms {
			symList[i] = string(s)
		}
		systemPrompt += fmt.Sprintf(
			"\n\n## Routable Symbols\n\nThe following symbols are fully configured and routable. "+
				"Never reject a signal solely because a symbol appears 'unconfigured' — "+
				"these symbols have valid market entries:\n%s",
			strings.Join(symList, ", "),
		)
	}

	// The agent communicates its decision purely through tool side-effects:
	// approve_and_route_order / resize_and_route_order route an order, while
	// reject_signal declines. LLM.Complete only returns the final assistant
	// text, so we wrap the decision tools to record which one actually fired
	// and derive the verdict from that — never from the mere fact that the
	// invocation succeeded.
	rec := &decisionRecorder{}
	wrapped := rec.wrap(r.tools)

	result := r.runtime.Invoke(ctx, domain.AgentRisk, systemPrompt, userMsg, wrapped, "risk_evaluation",
		"Evaluating risk for signal")
	routed, rejected, rejectReason := rec.outcome()
	r.mu.Lock()
	r.lastRejectReason = rejectReason
	r.mu.Unlock()
	if result.Err != nil {
		if routed {
			slog.Warn("risk agent: invocation error after order routed; treating as already routed",
				"signal_id", sig.ID, "error", result.Err)
			return true, ErrOrderAlreadyRouted
		}
		slog.Error("risk agent: invocation failed; failing closed",
			"signal_id", sig.ID, "error", result.Err)
		return false, result.Err
	}
	slog.Info("risk agent: evaluation complete",
		"signal_id", sig.ID, "routed", routed, "rejected", rejected,
		"output_preview", truncate(result.Output, 100))

	switch {
	case routed:
		// An order was placed by the approve/resize tool — report approval so
		// the caller knows routing was handled and does not re-process it.
		return true, nil
	case rejected:
		return false, nil
	default:
		// The model produced text but never called a decision tool. Nothing
		// was routed; fail closed and treat the signal as not approved.
		slog.Warn("risk agent: no decision tool called; failing closed",
			"signal_id", sig.ID, "output_preview", truncate(result.Output, 100))
		return false, nil
	}
}

// decisionRecorder observes the risk agent's decision tools as they fire
// inside the tool-calling loop. Only a handler that returns nil counts as a
// decision — a tool that errored (bad args, route failure) is surfaced to the
// model as an observation and does not represent a committed outcome.
type decisionRecorder struct {
	mu           sync.Mutex
	routed       bool   // approve_and_route_order or resize_and_route_order succeeded
	rejected     bool   // reject_signal succeeded
	rejectReason string // reason argument passed to reject_signal
}

// wrap returns a copy of tools with the three decision tools instrumented to
// record their outcome. Non-decision tools pass through unchanged.
func (d *decisionRecorder) wrap(in map[string]port.Tool) map[string]port.Tool {
	if len(in) == 0 {
		return in
	}
	out := make(map[string]port.Tool, len(in))
	for name, t := range in {
		switch name {
		case "approve_and_route_order", "resize_and_route_order":
			out[name] = d.record(t, &d.routed, nil)
		case "reject_signal":
			out[name] = d.record(t, &d.rejected, &d.rejectReason)
		default:
			out[name] = t
		}
	}
	return out
}

// record wraps a single tool so that a successful handler call sets *flag.
func (d *decisionRecorder) record(t port.Tool, flag *bool, reasonTarget *string) port.Tool {
	orig := t.Handler
	t.Handler = func(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
		res, err := orig(ctx, input)
		if err == nil {
			d.mu.Lock()
			*flag = true
			if reasonTarget != nil {
				var args struct {
					Reason string `json:"reason"`
				}
				if jErr := json.Unmarshal(input, &args); jErr == nil && args.Reason != "" {
					*reasonTarget = args.Reason
				}
			}
			d.mu.Unlock()
		}
		return res, err
	}
	return t
}

// outcome reports whether an order was routed and/or the signal was rejected.
func (d *decisionRecorder) outcome() (routed, rejected bool, rejectReason string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.routed, d.rejected, d.rejectReason
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// atrPattern extracts the trailing ATR value from signal reasons emitted by
// strategies that include it, e.g. "...ATR=2.7197". Strategy-agnostic — any
// reason containing "ATR=<float>" yields that float. Strategies that don't
// embed an ATR simply produce no match.
var atrPattern = regexp.MustCompile(`ATR=([0-9]+(?:\.[0-9]+)?)`)

// buildUserMessage assembles the per-signal user message. It enriches the
// base signal description with the live mark price (when a QuoteSource is
// wired) and any ATR hint embedded in the strategy's reason string. These
// numbers give the LLM enough information to compute stop_loss / take_profit
// in absolute terms — without them the model has no entry-price anchor and
// routinely emits an unprotected order (the root cause of the
// "no SL/TP levels to attach" flatten seen on BTC/USDT-PERP and SOL/USDT-PERP).
func (r *RiskAgent) buildUserMessage(ctx context.Context, sig domain.Signal, cleanReason string, positions []domain.Position) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf(
		"Signal received: symbol=%s side=%s strategy=%s reason=%q. "+
			"Current open positions: %d. "+
			"Evaluate and call approve_and_route_order (with stop_loss) or reject_signal.",
		sig.Symbol, sig.Side, sig.Strategy, cleanReason, len(positions),
	))

	// Cached market bias from screening agent, when available.
	if r.cache != nil {
		if bBytes, err := r.cache.Get(ctx, fmt.Sprintf("bias:%s", sig.Symbol)); err == nil && len(bBytes) > 0 {
			var bias domain.BiasResult
			if err := json.Unmarshal(bBytes, &bias); err == nil {
				fmt.Fprintf(&b, " market_bias=%s bias_score=%d bias_reason=%q", bias.Score.String(), bias.Score, bias.Reasoning)
			}
		}
	}

	// Live equity and drawdown context upfront to minimize tool round-trips.
	if r.balance != nil {
		if eq, dd, ok := r.balance.AccountEquity(ctx); ok && !eq.IsZero() {
			fmt.Fprintf(&b, " account_equity=%s drawdown_pct=%.2f%%", eq.StringFixed(2), dd)
		}
	}
	// Live mark price from the market hub, when available.
	if r.quotes != nil {
		if q, ok := r.quotes.LatestQuote(sig.Symbol); ok {
			price := q.Last
			if price.IsZero() {
				price = q.Mid
			}
			if !price.IsZero() {
				fmt.Fprintf(&b, " entry_price=%s", price.StringFixed(8))
			}
		}
	}

	// ATR hint parsed from the strategy's reason, when present.
	if m := atrPattern.FindStringSubmatch(cleanReason); len(m) == 2 {
		if _, err := strconv.ParseFloat(m[1], 64); err == nil {
			fmt.Fprintf(&b, " atr=%s", m[1])
		}
	}

	return b.String()
}

// injectPerformanceContextSystem appends recent performance data to the
// SYSTEM prompt rather than the user message. See the identically-named
// helper in screening.go — putting the context in the system prefix is
// what allows Anthropic's prompt cache to kick in across successive risk
// evaluations within the 5-minute cache window.
func (r *RiskAgent) injectPerformanceContextSystem(ctx context.Context, lookbackDays int, systemPrompt string) string {
	from := time.Now().UTC().AddDate(0, 0, -lookbackDays)
	to := time.Now().UTC()

	recentTrades, err := r.trades.TradesByWindow(ctx, from, to)
	if err != nil || len(recentTrades) == 0 {
		return systemPrompt
	}

	perf := tools.AggregatePerformance(recentTrades)
	context := tools.FormatPerformanceContext(perf)

	var losingLessons []string
	for i := len(recentTrades) - 1; i >= 0 && len(losingLessons) < 3; i-- {
		t := recentTrades[i]
		if t.PnL != nil && t.PnL.IsNegative() {
			losingLessons = append(losingLessons, fmt.Sprintf(
				"- %s %s (%s): loss %s USDT at price %s (%s)",
				t.Strategy, t.Symbol, t.Side, t.PnL.String(), t.FillPrice.String(),
				t.CreatedAt.Format("2006-01-02 15:04"),
			))
		}
	}

	lessonsSection := ""
	if len(losingLessons) > 0 {
		lessonsSection = "\n\n### Lessons from Recent Losing Trades\n" +
			"The following trades resulted in losses. Exercise increased caution and require stronger confirmation before approving signals matching these setups:\n" +
			strings.Join(losingLessons, "\n") + "\n"
	}

	return systemPrompt + "\n\n## Recent Strategy Performance\n\n" + context + lessonsSection
}

package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	_ "embed"

	"github.com/azhar/cerebro/internal/agent/tools"
	"github.com/azhar/cerebro/internal/config"
	"github.com/azhar/cerebro/internal/domain"
	"github.com/azhar/cerebro/internal/port"
	"github.com/google/uuid"
)

// symbolScreenResult tracks the outcome of a single per-symbol screening run.
type symbolScreenResult struct {
	Symbol  domain.Symbol
	Bias    string // "Bullish", "Bearish", "Neutral", or "" on failure
	Success bool
	Error   string // non-empty on failure
}

//go:embed prompts/screening.tmpl
var screeningPrompt string

//go:embed prompts/screening_opportunities.tmpl
var screeningOpportunitiesPrompt string

// opportunitiesMaxTokens is the per-call max_tokens override applied to the
// Phase 2 cross-symbol screening invocation. The bias-score-calibrated
// default (~512) cannot fit a multi-entry JSON array with multi-sentence
// reasoning fields; 2048 leaves comfortable headroom for the configured
// `screening_max_opportunities` (typically 3–5) without inflating costs on
// the much-shorter Phase 1 calls.
const opportunitiesMaxTokens = 2048

// screening_summary.tmpl is no longer used — Phase 3 renders the operator
// summary from cached Phase 1 + Phase 2 results via a pure-Go template
// (see renderScreeningSummary). This saves one full LLM invocation per
// screening cycle (~96/day at the default 15-minute interval).
//
// The file is kept on disk for reference but no longer embedded.

// BiasPublisher is the optional sink for bias updates produced by the screening
// agent. The TUI's runner satisfies this interface (SendBias).
//
// We define the interface at the consumption site to avoid importing the TUI
// package from the agent package — this preserves the hexagonal architecture
// rule that domain/agent code never depends on UI adapters.
type BiasPublisher interface {
	SendBias(b domain.BiasResult)
}

// ScreeningAgent runs on a configurable schedule (every 1–4 h) and writes
// a BiasResult to Redis for each monitored symbol.
// It NEVER runs on the hot signal path.
type ScreeningAgent struct {
	runtime          *Runtime
	cache            port.Cache
	tools            map[string]port.Tool
	trades           port.TradeStore
	cfg              config.AgentConfig
	source           port.SymbolSource
	biasTTL          time.Duration
	maxOpportunities int
	notifiers        []port.Notifier

	// discovery is optional. When non-nil, runCycle calls it before Phase 1
	// so the SymbolSource union can pick up the freshly discovered symbols.
	discovery *Discovery

	// planner is optional. When non-nil, runCycle invokes it after the
	// discovery phase: the candidate list flows into a deterministic
	// regime tagger + strategy matcher + trade-plan builder, and the
	// resulting TradePlans are cached and dispatched to ChatOps.
	// Independent of LLM availability.
	planner *DiscoveryPlanner

	// biasPub is an optional sink that receives every BiasResult after it has
	// been cached. May be nil; methods must guard before calling.
	biasPub BiasPublisher
}

// NewScreeningAgent creates a ScreeningAgent.
func NewScreeningAgent(
	runtime *Runtime,
	cache port.Cache,
	tools map[string]port.Tool,
	trades port.TradeStore,
	cfg config.AgentConfig,
	source port.SymbolSource,
	biasTTL time.Duration,
	notifiers []port.Notifier,
) *ScreeningAgent {
	maxOpp := cfg.ScreeningMaxOpportunities
	if maxOpp <= 0 {
		maxOpp = 3
	}
	return &ScreeningAgent{
		runtime:          runtime,
		cache:            cache,
		tools:            tools,
		trades:           trades,
		cfg:              cfg,
		source:           source,
		biasTTL:          biasTTL,
		maxOpportunities: maxOpp,
		notifiers:        notifiers,
	}
}

// SetDiscovery attaches an optional Discovery service that runs at the top
// of each cycle. Its candidates are written to Redis and the SymbolSource
// union picks them up. Pass nil to disable.
//
// Safe to call once during runtime composition; not safe after Run().
func (s *ScreeningAgent) SetDiscovery(d *Discovery) {
	s.discovery = d
}

// SetPlanner attaches an optional DiscoveryPlanner that turns the
// candidate list into TradePlans. Pass nil to disable.
//
// Safe to call once during runtime composition; not safe after Run().
func (s *ScreeningAgent) SetPlanner(p *DiscoveryPlanner) {
	s.planner = p
}

// SetBiasPublisher injects an optional sink that receives every BiasResult
// after it has been cached to Redis. Pass nil to disable.
//
// Safe to call once during runtime composition; not safe to call after Run().
func (s *ScreeningAgent) SetBiasPublisher(p BiasPublisher) {
	s.biasPub = p
}

// Run starts the scheduling loop. Blocks until ctx is cancelled.
func (s *ScreeningAgent) Run(ctx context.Context) error {
	interval := time.Duration(s.cfg.ScreeningIntervalMinutes) * time.Minute
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	slog.Info("screening agent started",
		"interval_minutes", s.cfg.ScreeningIntervalMinutes,
		"symbols", len(s.source.Symbols(ctx)))

	// Run immediately on startup.
	s.runCycle(ctx)

	for {
		select {
		case <-ctx.Done():
			slog.Info("screening agent stopping")
			return nil
		case <-ticker.C:
			s.runCycle(ctx)
		}
	}
}

// runCycle executes all phases and sends a detailed report to Telegram
// covering per-symbol results, partial failures, and the summary.
//
// Phase 0 (discovery, optional): refresh the dynamic candidate cache so the
// SymbolSource union picks up new movers before Phase 1 iterates.
// Phase 1: per-symbol bias.
// Phase 2: cross-symbol opportunities.
// Phase 3: consolidated operator summary.
func (s *ScreeningAgent) runCycle(ctx context.Context) {
	var cands []DiscoveryCandidate
	if s.discovery != nil {
		var err error
		cands, err = s.discovery.Candidates(ctx)
		if err != nil {
			slog.Warn("screening: discovery phase failed; continuing with static symbols", "error", err)
		}
	}
	results := s.runAll(ctx)

	// After Phase 1 (so the planner can read the freshly cached bias
	// scores), turn the candidate list into TradePlans and ship them to
	// ChatOps. Independent of LLM Phase 2; runs even when the LLM is
	// unavailable.
	if s.planner != nil && len(cands) > 0 {
		plans := s.planner.Run(ctx, cands, s.biasTTL)
		slog.Info("screening: trade plans generated",
			"plans", len(plans), "candidates", len(cands))
	}

	s.runOpportunities(ctx)
	summaryOk := s.runSummary(ctx)

	// Build and send a cycle report to Telegram.
	s.sendCycleReport(ctx, results, summaryOk)
}

// notifyAll pushes a message to all configured notifiers on the given channel.
func (s *ScreeningAgent) notifyAll(ctx context.Context, ch port.NotifyChannel, msg string) {
	for _, n := range s.notifiers {
		if err := n.Send(ctx, ch, msg); err != nil {
			slog.Warn("screening: failed to send notification", "channel", ch, "error", err)
		}
	}
}

// sendCycleReport builds a structured report from per-symbol results and sends
// it via Telegram. Differentiates full success, partial failure, and total failure.
func (s *ScreeningAgent) sendCycleReport(ctx context.Context, results []symbolScreenResult, summaryOk bool) {
	var succeeded, failed []symbolScreenResult
	for _, r := range results {
		if r.Success {
			succeeded = append(succeeded, r)
		} else {
			failed = append(failed, r)
		}
	}

	total := len(results)
	if total == 0 {
		return
	}

	var b strings.Builder

	switch {
	case len(failed) == 0:
		// All succeeded — no extra alert needed; the Phase 3 summary
		// already gets sent to ChannelAIReasoning.
		return

	case len(succeeded) == 0:
		// Total failure.
		fmt.Fprintf(&b, "Screening Cycle FAILED: 0/%d symbols produced bias data\n\n", total)
		b.WriteString("Failed:\n")
		for _, r := range failed {
			fmt.Fprintf(&b, "  - %s: %s\n", r.Symbol, r.Error)
		}
		b.WriteString("\nAll LLM providers may be down or misconfigured. Check API keys and provider config in app.yaml.")
		slog.Error("screening cycle: all symbols failed", "total", total)

	default:
		// Partial failure.
		fmt.Fprintf(&b, "Screening Cycle: %d/%d symbols succeeded\n\n", len(succeeded), total)
		b.WriteString("Succeeded:\n")
		for _, r := range succeeded {
			fmt.Fprintf(&b, "  - %s: %s\n", r.Symbol, r.Bias)
		}
		b.WriteString("\nFailed:\n")
		for _, r := range failed {
			fmt.Fprintf(&b, "  - %s: %s\n", r.Symbol, r.Error)
		}
		if summaryOk {
			b.WriteString("\nSummary was produced from partial data.")
		} else {
			b.WriteString("\nSummary could not be produced.")
		}
		slog.Warn("screening cycle: partial failure",
			"succeeded", len(succeeded), "failed", len(failed), "total", total)
	}

	s.notifyAll(ctx, port.ChannelSystemAlerts, b.String())
}

func (s *ScreeningAgent) runAll(ctx context.Context) []symbolScreenResult {
	var (
		mu      sync.Mutex
		results []symbolScreenResult
	)
	g, gctx := errgroup.WithContext(ctx)
	syms := s.source.Symbols(ctx)
	for _, sym := range syms {
		g.Go(func() error {
			bias, err := s.runForSymbol(gctx, sym)
			mu.Lock()
			if err != nil {
				slog.Error("screening agent: symbol run failed",
					"symbol", sym, "error", err)
				results = append(results, symbolScreenResult{
					Symbol: sym, Success: false, Error: err.Error(),
				})
			} else {
				results = append(results, symbolScreenResult{
					Symbol: sym, Bias: bias, Success: true,
				})
			}
			mu.Unlock()
			return nil
		})
	}
	_ = g.Wait()
	return results
}

// runForSymbol returns the bias string on success, or an error if the LLM
// invocation failed entirely (so the caller can report it).
//
// We intentionally keep the user message tiny and stable across symbols
// ("Analyse <SYMBOL> …") so that the system prompt + tool schemas + perf
// context form a long stable prefix that Anthropic's prompt cache can
// serve at ~10% of list price. Moving the performance blob out of the
// user message into the system prompt is what makes that prefix stable.
func (s *ScreeningAgent) runForSymbol(ctx context.Context, sym domain.Symbol) (string, error) {
	userMsg := fmt.Sprintf("Analyse current market conditions for %s and produce a bias score.", sym)

	// Inject recent strategy performance into the SYSTEM prompt (cacheable
	// across all per-symbol calls in this cycle) rather than the user
	// message (which would defeat caching).
	systemPrompt := screeningPrompt
	if s.trades != nil {
		systemPrompt = injectPerformanceContextSystem(ctx, s.trades, 7, systemPrompt)
	}

	// Only pass tools relevant to screening; skip irrelevant ones (position sizing, routing, etc.)
	// to avoid confusing the model.
	screeningTools := make(map[string]port.Tool)
	for name, tool := range s.tools {
		switch name {
		case "get_market_data", "get_derivatives_data", "fetch_latest_news", "get_economic_events":
			screeningTools[name] = tool
		}
	}

	result := s.runtime.Invoke(ctx, domain.AgentScreening, systemPrompt, userMsg, screeningTools, "bias_score",
		fmt.Sprintf("Analyzing %s market conditions", sym))
	if result.Err != nil {
		// Fail closed: retain previous cached bias, do NOT clear the key.
		slog.Warn("screening: LLM failed; retaining previous bias",
			"symbol", sym, "error", result.Err)
		return "", fmt.Errorf("LLM invocation failed: %w", result.Err)
	}

	if strings.TrimSpace(result.Output) == "" {
		slog.Warn("screening: empty model output; defaulting to Neutral",
			"symbol", sym)
		result.Output = `{"bias":"Neutral","reasoning":"Model returned empty response; defaulting to Neutral per fail-closed policy."}`
	}

	parsed, err := parseScreeningOutput(result.Output)
	if err != nil {
		slog.Warn("screening: parse output failed; defaulting to Neutral",
			"symbol", sym, "error", err, "output", truncateOutput(result.Output, 400))
		parsed = screeningParsedOutput{
			Bias:      "Neutral",
			Reasoning: "Model response was not valid JSON; defaulting to Neutral per fail-closed policy.",
		}
	}

	if err := s.cacheBias(ctx, sym, parsed.Bias, parsed.Reasoning); err != nil {
		return "", err
	}
	return parsed.Bias, nil
}

type screeningParsedOutput struct {
	Bias      string `json:"bias"`
	Reasoning string `json:"reasoning"`
}

func parseScreeningOutput(raw string) (screeningParsedOutput, error) {
	// Trim whitespace and strip markdown code fences if present.
	cleaned := strings.TrimSpace(raw)
	cleaned = strings.TrimPrefix(cleaned, "```json")
	cleaned = strings.TrimPrefix(cleaned, "```")
	cleaned = strings.TrimSuffix(cleaned, "```")
	cleaned = strings.TrimSpace(cleaned)

	var parsed screeningParsedOutput
	if err := json.Unmarshal([]byte(cleaned), &parsed); err == nil {
		return parsed, nil
	}

	obj := extractFirstJSONObject(raw)
	if obj == "" {
		return screeningParsedOutput{}, fmt.Errorf("no JSON object found in model output")
	}
	if err := json.Unmarshal([]byte(obj), &parsed); err != nil {
		return screeningParsedOutput{}, fmt.Errorf("parse extracted JSON: %w", err)
	}
	return parsed, nil
}

func extractFirstJSONObject(s string) string {
	start := strings.IndexByte(s, '{')
	if start == -1 {
		return ""
	}
	depth := 0
	inString := false
	escaped := false
	for i := start; i < len(s); i++ {
		ch := s[i]
		if inString {
			if escaped {
				escaped = false
				continue
			}
			if ch == '\\' {
				escaped = true
				continue
			}
			if ch == '"' {
				inString = false
			}
			continue
		}
		switch ch {
		case '"':
			inString = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return s[start : i+1]
			}
		}
	}
	return ""
}

func truncateOutput(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	return s[:max] + "...(truncated)"
}

func (s *ScreeningAgent) cacheBias(ctx context.Context, sym domain.Symbol, biasStr, reasoning string) error {
	score := domain.BiasNeutral
	switch strings.TrimSpace(strings.ToLower(biasStr)) {
	case "bullish":
		score = domain.BiasBullish
	case "bearish":
		score = domain.BiasBearish
	}

	bias := domain.BiasResult{
		Symbol:    sym,
		Score:     score,
		Reasoning: reasoning,
		CachedAt:  time.Now().UTC(),
		ExpiresAt: time.Now().UTC().Add(s.biasTTL),
	}

	b, err := json.Marshal(bias)
	if err != nil {
		return fmt.Errorf("marshal bias: %w", err)
	}
	key := fmt.Sprintf("bias:%s", sym)
	if err := s.cache.Set(ctx, key, b, s.biasTTL); err != nil {
		return fmt.Errorf("cache bias: %w", err)
	}

	slog.Info("screening: bias updated",
		"symbol", sym, "score", bias.Score, "expires_at", bias.ExpiresAt)

	// Push to optional sinks (e.g. TUI Bias / Signals panel). Best-effort:
	// the publisher is responsible for non-blocking delivery.
	if s.biasPub != nil {
		s.biasPub.SendBias(bias)
	}
	return nil
}

// injectPerformanceContext prepends a performance summary to the user message.
//
// DEPRECATED: prefer injectPerformanceContextSystem when the caller can
// place the context in the system prompt — moving it there lets Anthropic's
// prompt cache hit the ~10%-of-list price across sibling calls in the same
// cycle. This helper remains for one-off flows where a cacheable system
// prefix is not available.
func injectPerformanceContext(ctx context.Context, trades port.TradeStore, lookbackDays int, userMsg string) string {
	from := time.Now().UTC().AddDate(0, 0, -lookbackDays)
	to := time.Now().UTC()

	recentTrades, err := trades.TradesByWindow(ctx, from, to)
	if err != nil || len(recentTrades) == 0 {
		return userMsg
	}

	perf := tools.AggregatePerformance(recentTrades)
	context := tools.FormatPerformanceContext(perf)
	return context + "\n\n" + userMsg
}

// injectPerformanceContextSystem appends the performance summary to the
// system prompt. The system prompt is cacheable (Anthropic 5-min ephemeral
// cache) whereas the user message is not; sharing the same system prefix
// across every per-symbol call in the screening cycle is the difference
// between paying full price N times and paying full price once plus the
// cached rate on the remaining N-1 calls.
func injectPerformanceContextSystem(ctx context.Context, trades port.TradeStore, lookbackDays int, systemPrompt string) string {
	from := time.Now().UTC().AddDate(0, 0, -lookbackDays)
	to := time.Now().UTC()

	recentTrades, err := trades.TradesByWindow(ctx, from, to)
	if err != nil || len(recentTrades) == 0 {
		return systemPrompt
	}

	perf := tools.AggregatePerformance(recentTrades)
	context := tools.FormatPerformanceContext(perf)
	return systemPrompt + "\n\n## Recent Strategy Performance\n\n" + context
}

// runOpportunities runs Phase 2: cross-symbol analysis producing ranked opportunities.
func (s *ScreeningAgent) runOpportunities(ctx context.Context) {
	// Collect all cached bias scores from Phase 1.
	biasContext := s.collectBiasContext(ctx)
	if biasContext == "" {
		slog.Warn("screening opportunities: no bias data available; skipping")
		return
	}

	userMsg := fmt.Sprintf(
		"Here are the current bias scores for all monitored symbols:\n\n%s\n\n"+
			"Analyse these scores and identify the top %d entry opportunities. "+
			"Use get_all_market_data to compare relative strength before deciding.",
		biasContext, s.maxOpportunities,
	)

	// Build tool set: screening tools + cross-symbol comparators + Phase 0 candidates.
	oppTools := make(map[string]port.Tool)
	for name, tool := range s.tools {
		switch name {
		case "get_market_data", "get_derivatives_data", "fetch_latest_news",
			"get_economic_events", "get_all_market_data", "get_discovery_candidates":
			oppTools[name] = tool
		}
	}

	// Phase 2 emits a multi-entry JSON array (one object per opportunity, each
	// with a multi-sentence reasoning field plus correlations). The provider
	// `max_output_tokens` default in app.yaml is calibrated for the Phase 1
	// bias-score JSON (~100 tokens) and is far too small here — at 512 the
	// response gets cut off mid-string, leaving the parser with unterminated
	// JSON. Override the cap for this call so the array can be emitted
	// completely. The parser is also tolerant of truncation as a last line of
	// defence; see parseOpportunitiesOutput.
	oppCtx := WithMaxTokens(ctx, opportunitiesMaxTokens)

	result := s.runtime.Invoke(oppCtx, domain.AgentScreening, screeningOpportunitiesPrompt, userMsg, oppTools, "screening_opportunities",
		"Identifying top entry opportunities")
	if result.Err != nil {
		slog.Warn("screening opportunities: LLM failed; retaining previous cache", "error", result.Err)
		return
	}

	if strings.TrimSpace(result.Output) == "" {
		slog.Warn("screening opportunities: empty model output; skipping")
		return
	}

	opportunities, err := parseOpportunitiesOutput(result.Output)
	if err != nil {
		slog.Warn("screening opportunities: parse failed",
			"error", err, "output", truncateOutput(result.Output, 400))
		return
	}

	// Cap to max opportunities.
	if len(opportunities) > s.maxOpportunities {
		opportunities = opportunities[:s.maxOpportunities]
	}

	now := time.Now().UTC()
	for i := range opportunities {
		if opportunities[i].ID == "" {
			opportunities[i].ID = uuid.New().String()
		}
		opportunities[i].CachedAt = now
		opportunities[i].ExpiresAt = now.Add(s.biasTTL)
	}

	b, err := json.Marshal(opportunities)
	if err != nil {
		slog.Error("screening opportunities: marshal failed", "error", err)
		return
	}
	if err := s.cache.Set(ctx, "screening:opportunities", b, s.biasTTL); err != nil {
		slog.Error("screening opportunities: cache write failed", "error", err)
		return
	}

	slog.Info("screening: opportunities updated",
		"count", len(opportunities), "expires_at", now.Add(s.biasTTL))
}

func (s *ScreeningAgent) collectBiasContext(ctx context.Context) string {
	keys, err := s.cache.Keys(ctx, "bias:*")
	if err != nil || len(keys) == 0 {
		return ""
	}

	var lines []string
	for _, key := range keys {
		raw, err := s.cache.Get(ctx, key)
		if err != nil || raw == nil {
			continue
		}
		var bias domain.BiasResult
		if err := json.Unmarshal(raw, &bias); err != nil {
			continue
		}
		lines = append(lines, fmt.Sprintf("- %s: %s (reasoning: %s)",
			bias.Symbol, bias.Score, bias.Reasoning))
	}
	return strings.Join(lines, "\n")
}

// runSummary runs Phase 3: synthesizes all Phase 1 + Phase 2 results into a
// consolidated summary for the operator. This phase used to invoke the LLM
// for pure text synthesis; that wastes ~1 call per cycle (~96/day at 15-min
// interval) because the output is entirely determined by the Phase 1 bias
// map and the Phase 2 opportunities list. We now render it from a Go
// template — zero LLM cost, deterministic formatting.
//
// Returns true if a summary was produced and sent to notifiers.
func (s *ScreeningAgent) runSummary(ctx context.Context) bool {
	biases := s.loadBiases(ctx)
	if len(biases) == 0 {
		slog.Warn("screening summary: no bias data available; skipping")
		return false
	}
	opps := s.loadOpportunities(ctx)

	output := renderScreeningSummary(biases, opps)
	if strings.TrimSpace(output) == "" {
		return false
	}

	if err := s.cache.Set(ctx, "screening:summary", []byte(output), s.biasTTL); err != nil {
		slog.Error("screening summary: cache write failed", "error", err)
		return false
	}

	slog.Info("screening: summary rendered (no LLM)",
		"biases", len(biases), "opportunities", len(opps),
		"expires_at", time.Now().UTC().Add(s.biasTTL))

	s.notifyAll(ctx, port.ChannelAIReasoning, output)
	return true
}

// loadBiases returns all cached Phase 1 biases keyed by symbol.
func (s *ScreeningAgent) loadBiases(ctx context.Context) []domain.BiasResult {
	keys, err := s.cache.Keys(ctx, "bias:*")
	if err != nil || len(keys) == 0 {
		return nil
	}
	out := make([]domain.BiasResult, 0, len(keys))
	for _, key := range keys {
		raw, err := s.cache.Get(ctx, key)
		if err != nil || raw == nil {
			continue
		}
		var b domain.BiasResult
		if err := json.Unmarshal(raw, &b); err != nil {
			continue
		}
		out = append(out, b)
	}
	sort.Slice(out, func(i, j int) bool {
		return string(out[i].Symbol) < string(out[j].Symbol)
	})
	return out
}

// loadOpportunities returns the cached Phase 2 opportunities.
func (s *ScreeningAgent) loadOpportunities(ctx context.Context) []domain.ScreeningOpportunity {
	raw, err := s.cache.Get(ctx, "screening:opportunities")
	if err != nil || raw == nil {
		return nil
	}
	var opps []domain.ScreeningOpportunity
	if err := json.Unmarshal(raw, &opps); err != nil {
		return nil
	}
	return opps
}

// renderScreeningSummary formats a human-readable markdown summary from
// cached Phase 1 + Phase 2 state. Pure function (no I/O, no LLM) so we
// can unit-test it cheaply.
func renderScreeningSummary(biases []domain.BiasResult, opps []domain.ScreeningOpportunity) string {
	var b strings.Builder

	// --- Market Overview ------------------------------------------------
	bullish, bearish, neutral := 0, 0, 0
	for _, bi := range biases {
		switch bi.Score {
		case domain.BiasBullish:
			bullish++
		case domain.BiasBearish:
			bearish++
		default:
			neutral++
		}
	}
	b.WriteString("### Market Overview\n")
	switch {
	case bullish > bearish && bullish > neutral:
		fmt.Fprintf(&b, "Broadly bullish across monitored assets (%d bullish / %d bearish / %d neutral).\n",
			bullish, bearish, neutral)
	case bearish > bullish && bearish > neutral:
		fmt.Fprintf(&b, "Broadly bearish across monitored assets (%d bullish / %d bearish / %d neutral).\n",
			bullish, bearish, neutral)
	default:
		fmt.Fprintf(&b, "Mixed / neutral market conditions (%d bullish / %d bearish / %d neutral).\n",
			bullish, bearish, neutral)
	}

	// --- Per-Symbol Bias ------------------------------------------------
	b.WriteString("\n### Per-Symbol Bias\n")
	for _, bi := range biases {
		reason := truncateOutput(strings.ReplaceAll(bi.Reasoning, "\n", " "), 160)
		fmt.Fprintf(&b, "- **%s**: %s — %s\n", bi.Symbol, bi.Score, reason)
	}

	// --- Top Opportunities ---------------------------------------------
	b.WriteString("\n### Top Opportunities\n")
	if len(opps) == 0 {
		b.WriteString("_No actionable opportunities identified this cycle._\n")
	} else {
		for _, o := range opps {
			reason := truncateOutput(strings.ReplaceAll(o.Reasoning, "\n", " "), 160)
			marker := ""
			if o.Avoided {
				marker = " [AVOIDED]"
			}
			fmt.Fprintf(&b, "- **%s** (%s) %s — conf=%.2f%s: %s\n",
				o.Symbol, o.Venue, strings.ToUpper(string(o.Side)),
				o.Confidence, marker, reason)
		}
	}

	// --- Watchlist ------------------------------------------------------
	// Flag divergences and avoided opportunities as items to watch.
	var watch []string
	if bullish > 0 && bearish > 0 {
		watch = append(watch, fmt.Sprintf("Divergence: %d bullish vs %d bearish — correlated assets disagree.", bullish, bearish))
	}
	for _, o := range opps {
		if o.Avoided {
			watch = append(watch, fmt.Sprintf("Avoided %s: %s", o.Symbol, truncateOutput(o.Reasoning, 120)))
		}
	}
	if len(watch) > 3 {
		watch = watch[:3]
	}
	if len(watch) > 0 {
		b.WriteString("\n### Watchlist\n")
		for _, w := range watch {
			fmt.Fprintf(&b, "- %s\n", w)
		}
	}

	return b.String()
}

type opportunitiesOutput struct {
	Opportunities []opportunityEntry `json:"opportunities"`
}

type opportunityEntry struct {
	Symbol       string               `json:"symbol"`
	Venue        string               `json:"venue"`
	Side         string               `json:"side"`
	Confidence   float64              `json:"confidence"`
	Reasoning    string               `json:"reasoning"`
	Correlations []correlationEntry   `json:"correlations"`
	Avoided      bool                 `json:"avoided"`
}

type correlationEntry struct {
	Symbol string `json:"symbol"`
	Impact string `json:"impact"`
	Note   string `json:"note"`
}

func parseOpportunitiesOutput(raw string) ([]domain.ScreeningOpportunity, error) {
	cleaned := strings.TrimSpace(raw)
	cleaned = strings.TrimPrefix(cleaned, "```json")
	cleaned = strings.TrimPrefix(cleaned, "```")
	cleaned = strings.TrimSuffix(cleaned, "```")
	cleaned = strings.TrimSpace(cleaned)

	var parsed opportunitiesOutput
	if err := json.Unmarshal([]byte(cleaned), &parsed); err == nil {
		return convertOpportunities(parsed), nil
	}

	if obj := extractFirstJSONObject(raw); obj != "" {
		if err := json.Unmarshal([]byte(obj), &parsed); err == nil {
			return convertOpportunities(parsed), nil
		}
	}

	// Last-resort recovery: the response was truncated mid-JSON (typically
	// because max_tokens cut the model off in the middle of a string in the
	// `opportunities` array). Walk the array and salvage any fully-emitted
	// objects that precede the truncation point. This degrades the result
	// gracefully — we may lose the final 1-2 entries but still surface the
	// rest to the operator instead of failing the entire phase.
	if recovered := recoverPartialOpportunities(raw); len(recovered.Opportunities) > 0 {
		return convertOpportunities(recovered), nil
	}

	return nil, fmt.Errorf("no JSON object found in model output")
}

// recoverPartialOpportunities salvages fully-emitted entries from a truncated
// `{"opportunities": [...]}` response. Returns an empty struct when nothing
// is recoverable (no array start, or no complete object inside the array).
func recoverPartialOpportunities(raw string) opportunitiesOutput {
	var out opportunitiesOutput

	// Locate the "opportunities" key, then the array's opening bracket.
	keyIdx := strings.Index(raw, `"opportunities"`)
	if keyIdx == -1 {
		return out
	}
	rel := strings.IndexByte(raw[keyIdx:], '[')
	if rel == -1 {
		return out
	}
	pos := keyIdx + rel + 1

	for pos < len(raw) {
		// Skip whitespace and inter-element commas.
		for pos < len(raw) {
			ch := raw[pos]
			if ch == ' ' || ch == '\t' || ch == '\n' || ch == '\r' || ch == ',' {
				pos++
				continue
			}
			break
		}
		if pos >= len(raw) || raw[pos] == ']' {
			break
		}
		if raw[pos] != '{' {
			break // unexpected token; bail out — what we have is what we keep
		}

		// Walk a balanced object, respecting JSON string escapes.
		objStart := pos
		depth := 0
		inString := false
		escaped := false
		objEnd := -1
		for ; pos < len(raw); pos++ {
			ch := raw[pos]
			if inString {
				switch {
				case escaped:
					escaped = false
				case ch == '\\':
					escaped = true
				case ch == '"':
					inString = false
				}
				continue
			}
			switch ch {
			case '"':
				inString = true
			case '{':
				depth++
			case '}':
				depth--
				if depth == 0 {
					objEnd = pos + 1
				}
			}
			if objEnd != -1 {
				break
			}
		}
		if objEnd == -1 {
			break // truncation hit mid-object — discard this partial entry
		}

		var entry opportunityEntry
		if err := json.Unmarshal([]byte(raw[objStart:objEnd]), &entry); err == nil {
			out.Opportunities = append(out.Opportunities, entry)
		}
		pos = objEnd
	}
	return out
}

func convertOpportunities(parsed opportunitiesOutput) []domain.ScreeningOpportunity {
	out := make([]domain.ScreeningOpportunity, 0, len(parsed.Opportunities))
	for _, e := range parsed.Opportunities {
		side := domain.Side(strings.ToLower(e.Side))

		var corrs []domain.SymbolCorrelation
		for _, c := range e.Correlations {
			corrs = append(corrs, domain.SymbolCorrelation{
				Symbol: domain.Symbol(c.Symbol),
				Impact: c.Impact,
				Note:   c.Note,
			})
		}

		out = append(out, domain.ScreeningOpportunity{
			Symbol:       domain.Symbol(e.Symbol),
			Venue:        domain.Venue(e.Venue),
			Side:         side,
			Confidence:   e.Confidence,
			Reasoning:    e.Reasoning,
			Correlations: corrs,
			Avoided:      e.Avoided,
		})
	}
	return out
}

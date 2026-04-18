package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	_ "embed"

	"github.com/azhar/cerebro/internal/agent/tools"
	"github.com/azhar/cerebro/internal/config"
	"github.com/azhar/cerebro/internal/domain"
	"github.com/azhar/cerebro/internal/port"
	"github.com/google/uuid"
)

//go:embed prompts/screening.tmpl
var screeningPrompt string

//go:embed prompts/screening_opportunities.tmpl
var screeningOpportunitiesPrompt string

// ScreeningAgent runs on a configurable schedule (every 1–4 h) and writes
// a BiasResult to Redis for each monitored symbol.
// It NEVER runs on the hot signal path.
type ScreeningAgent struct {
	runtime         *Runtime
	cache           port.Cache
	tools           map[string]port.Tool
	trades          port.TradeStore
	cfg             config.AgentConfig
	symbols         []domain.Symbol
	biasTTL         time.Duration
	maxOpportunities int
}

// NewScreeningAgent creates a ScreeningAgent.
func NewScreeningAgent(
	runtime *Runtime,
	cache port.Cache,
	tools map[string]port.Tool,
	trades port.TradeStore,
	cfg config.AgentConfig,
	symbols []domain.Symbol,
	biasTTL time.Duration,
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
		symbols:          symbols,
		biasTTL:          biasTTL,
		maxOpportunities: maxOpp,
	}
}

// Run starts the scheduling loop. Blocks until ctx is cancelled.
func (s *ScreeningAgent) Run(ctx context.Context) error {
	interval := time.Duration(s.cfg.ScreeningIntervalMinutes) * time.Minute
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	slog.Info("screening agent started",
		"interval_minutes", s.cfg.ScreeningIntervalMinutes,
		"symbols", len(s.symbols))

	// Run immediately on startup.
	s.runAll(ctx)
	s.runOpportunities(ctx)

	for {
		select {
		case <-ctx.Done():
			slog.Info("screening agent stopping")
			return nil
		case <-ticker.C:
			s.runAll(ctx)
			s.runOpportunities(ctx)
		}
	}
}

func (s *ScreeningAgent) runAll(ctx context.Context) {
	for _, sym := range s.symbols {
		if err := s.runForSymbol(ctx, sym); err != nil {
			slog.Error("screening agent: symbol run failed",
				"symbol", sym, "error", err)
		}
	}
}

func (s *ScreeningAgent) runForSymbol(ctx context.Context, sym domain.Symbol) error {
	userMsg := fmt.Sprintf("Analyse current market conditions for %s and produce a bias score.", sym)

	// Inject recent strategy performance context when available.
	if s.trades != nil {
		userMsg = injectPerformanceContext(ctx, s.trades, 7, userMsg)
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

	result := s.runtime.Invoke(ctx, domain.AgentScreening, screeningPrompt, userMsg, screeningTools, "bias_score")
	if result.Err != nil {
		// Fail closed: retain previous cached bias, do NOT clear the key.
		slog.Warn("screening: LLM failed; retaining previous bias",
			"symbol", sym, "error", result.Err)
		return nil
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
		return err
	}
	return nil
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
	return nil
}

// injectPerformanceContext prepends recent strategy performance data to the user message.
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

	// Build tool set: screening tools + get_all_market_data.
	oppTools := make(map[string]port.Tool)
	for name, tool := range s.tools {
		switch name {
		case "get_market_data", "get_derivatives_data", "fetch_latest_news", "get_economic_events":
			oppTools[name] = tool
		}
	}
	if t, ok := s.tools["get_all_market_data"]; ok {
		oppTools["get_all_market_data"] = t
	}

	result := s.runtime.Invoke(ctx, domain.AgentScreening, screeningOpportunitiesPrompt, userMsg, oppTools, "screening_opportunities")
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

	obj := extractFirstJSONObject(raw)
	if obj == "" {
		return nil, fmt.Errorf("no JSON object found in model output")
	}
	if err := json.Unmarshal([]byte(obj), &parsed); err != nil {
		return nil, fmt.Errorf("parse extracted JSON: %w", err)
	}
	return convertOpportunities(parsed), nil
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

package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	_ "embed"

	"github.com/azhar/cerebro/internal/config"
	"github.com/azhar/cerebro/internal/domain"
	"github.com/azhar/cerebro/internal/port"
)

//go:embed prompts/screening.tmpl
var screeningPrompt string

// ScreeningAgent runs on a configurable schedule (every 1–4 h) and writes
// a BiasResult to Redis for each monitored symbol.
// It NEVER runs on the hot signal path.
type ScreeningAgent struct {
	runtime  *Runtime
	cache    port.Cache
	tools    map[string]port.ToolHandler
	cfg      config.AgentConfig
	symbols  []domain.Symbol
	biasTTL  time.Duration
}

// NewScreeningAgent creates a ScreeningAgent.
func NewScreeningAgent(
	runtime *Runtime,
	cache port.Cache,
	tools map[string]port.ToolHandler,
	cfg config.AgentConfig,
	symbols []domain.Symbol,
	biasTTL time.Duration,
) *ScreeningAgent {
	return &ScreeningAgent{
		runtime: runtime,
		cache:   cache,
		tools:   tools,
		cfg:     cfg,
		symbols: symbols,
		biasTTL: biasTTL,
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

	for {
		select {
		case <-ctx.Done():
			slog.Info("screening agent stopping")
			return nil
		case <-ticker.C:
			s.runAll(ctx)
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

	result := s.runtime.Invoke(ctx, domain.AgentScreening, screeningPrompt, userMsg, s.tools, "bias_score")
	if result.Err != nil {
		// Fail closed: retain previous cached bias, do NOT clear the key.
		slog.Warn("screening: LLM failed; retaining previous bias",
			"symbol", sym, "error", result.Err)
		return nil
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
	var parsed screeningParsedOutput
	if err := json.Unmarshal([]byte(raw), &parsed); err == nil {
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

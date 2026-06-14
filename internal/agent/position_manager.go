package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"text/template"
	"time"

	_ "embed"

	"github.com/azhar/cerebro/internal/config"
	"github.com/azhar/cerebro/internal/domain"
	"github.com/azhar/cerebro/internal/port"
	"github.com/shopspring/decimal"
)

//go:embed prompts/position_manager.tmpl
var positionManagerPrompt string

//go:embed prompts/position_manager_user.tmpl
var positionManagerUserTmplRaw string

var pmUserTemplate = template.Must(template.New("pm_user").Parse(positionManagerUserTmplRaw))

// positionManagerInput is the data injected into the user prompt template.
type positionManagerInput struct {
	Symbol             domain.Symbol
	Venue              domain.Venue
	Side               domain.Side
	Strategy           domain.StrategyName
	CorrelationID      string
	EntryPrice         string
	CurrentPrice       string
	UnrealizedPnLPct   string
	StopLoss           string
	TakeProfit1        string
	Quantity           string
	Leverage           int
	OpenHours          float64
	MaxHoldHours       int
	ScaleOutPct        float64
	TrailingEnabled    bool
	TrailDistance      string
	MaxStopDistancePct float64
	IsFutures          bool
	Environment        domain.Environment
}

// pmRawResponse is the JSON shape the LLM is asked to return.
type pmRawResponse struct {
	Action               string  `json:"action"`
	Symbol               string  `json:"symbol"`
	CorrelationID        string  `json:"correlation_id"`
	Reasoning            string  `json:"reasoning"`
	NewStop              float64 `json:"new_stop"`
	CloseQuantity        string  `json:"close_quantity"`
	Confidence           float64 `json:"confidence"`
	RequiresConfirmation bool    `json:"requires_confirmation"`
}

// PositionManagerAgent reviews open positions and returns a ManagedAction.
// In live mode, FLATTEN and PARTIAL_CLOSE decisions are advisory — the caller
// must queue them for operator confirmation. In paper/demo mode they execute
// autonomously. On any LLM failure the configured LLMFailureAction is applied;
// the default fallback is ActionHold (never automatically flatten on error).
type PositionManagerAgent struct {
	runtime *Runtime
	tools   map[string]port.Tool
	cfg     config.PositionManagerConfig
}

// NewPositionManagerAgent creates a PositionManagerAgent.
func NewPositionManagerAgent(
	runtime *Runtime,
	agentTools map[string]port.Tool,
	cfg config.PositionManagerConfig,
) *PositionManagerAgent {
	return &PositionManagerAgent{runtime: runtime, tools: agentTools, cfg: cfg}
}

// Review asks the LLM to evaluate the position and returns a ManagedAction.
// Fails safe: any error returns the configured LLMFailureAction (default HOLD).
func (p *PositionManagerAgent) Review(ctx context.Context, review domain.PositionReview) (domain.ManagedAction, error) {
	userMsg, err := renderPMUserMsg(review)
	if err != nil {
		slog.Error("position_manager: template render failed; using fallback",
			"symbol", review.Position.Symbol, "error", err)
		return p.llmFallback(review.Position, "template render failed"), nil
	}

	invokeCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	result := p.runtime.Invoke(
		invokeCtx,
		domain.AgentPositionManager,
		positionManagerPrompt,
		userMsg,
		p.tools,
		"position_review",
		"Reviewing position "+string(review.Position.Symbol),
	)
	if result.Err != nil {
		slog.Error("position_manager: invocation failed; using fallback",
			"symbol", review.Position.Symbol, "error", result.Err)
		return p.llmFallback(review.Position, "agent invocation failed"), nil
	}

	action, err := parsePMResponse(result.Output)
	if err != nil {
		slog.Error("position_manager: response parse failed; using fallback",
			"symbol", review.Position.Symbol, "error", err,
			"raw", truncate(result.Output, 200))
		return p.llmFallback(review.Position, "response parse failed"), nil
	}

	if err := action.Validate(); err != nil {
		slog.Error("position_manager: action validation failed; using fallback",
			"symbol", review.Position.Symbol, "error", err)
		return p.llmFallback(review.Position, "action validation failed"), nil
	}

	slog.Info("position_manager: decision",
		"symbol", review.Position.Symbol,
		"action", action.Decision,
		"confidence", action.Confidence,
		"reason", truncate(action.Reason, 100))
	return action, nil
}

// llmFallback returns the configured failure action. "tighten_breakeven" moves
// the stop to entry price so a winner cannot turn into a loser. Everything else
// — including the default — returns ActionHold.
func (p *PositionManagerAgent) llmFallback(pos domain.Position, reason string) domain.ManagedAction {
	if p.cfg.LLMFailureAction == "tighten_breakeven" && !pos.EntryPrice.IsZero() {
		return domain.ManagedAction{
			Decision:    domain.ActionTightenStop,
			NewStopLoss: pos.EntryPrice,
			Reason:      reason + " (tighten_breakeven fallback)",
			Confidence:  0,
		}
	}
	return domain.ManagedAction{
		Decision:   domain.ActionHold,
		Reason:     reason + " (hold fallback)",
		Confidence: 0,
	}
}

// renderPMUserMsg renders the user prompt template for the given review.
func renderPMUserMsg(review domain.PositionReview) (string, error) {
	pos := review.Position
	openHours := 0.0
	if !pos.OpenedAt.IsZero() {
		openHours = time.Since(pos.OpenedAt).Hours()
	}

	data := positionManagerInput{
		Symbol:           pos.Symbol,
		Venue:            pos.Venue,
		Side:             pos.Side,
		Strategy:         pos.Strategy,
		CorrelationID:    pos.CorrelationID,
		EntryPrice:       pos.EntryPrice.StringFixed(8),
		CurrentPrice:     pos.CurrentPrice.StringFixed(8),
		UnrealizedPnLPct: pos.UnrealizedPnLPct().StringFixed(2),
		StopLoss:         pos.StopLoss.StringFixed(8),
		TakeProfit1:      pos.TakeProfit1.StringFixed(8),
		Quantity:         pos.Quantity.StringFixed(8),
		Leverage:         pos.Leverage,
		OpenHours:        openHours,
		IsFutures:        pos.Venue == domain.VenueBinanceFutures,
	}

	var buf bytes.Buffer
	if err := pmUserTemplate.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("pm user template: %w", err)
	}
	return buf.String(), nil
}

// parsePMResponse converts the LLM's raw JSON output into a domain.ManagedAction.
// LLM action names (HOLD / MOVE_STOP / PARTIAL_CLOSE / FLATTEN) are mapped to
// the domain's ActionDecision constants.
func parsePMResponse(raw string) (domain.ManagedAction, error) {
	var resp pmRawResponse
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		return domain.ManagedAction{}, fmt.Errorf("unmarshal pm response: %w", err)
	}

	action := domain.ManagedAction{
		Reason:     resp.Reasoning,
		Confidence: resp.Confidence,
	}

	switch resp.Action {
	case "HOLD":
		action.Decision = domain.ActionHold
	case "MOVE_STOP":
		action.Decision = domain.ActionTightenStop
		action.NewStopLoss = decimal.NewFromFloat(resp.NewStop)
	case "PARTIAL_CLOSE", "FLATTEN":
		action.Decision = domain.ActionClose
	default:
		return domain.ManagedAction{}, fmt.Errorf("unknown pm action %q", resp.Action)
	}

	return action, nil
}

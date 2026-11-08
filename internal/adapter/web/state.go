// Package web serves the high-density terminal dashboard over HTTP with live
// WebSocket updates. It mirrors the event surface of the Bubble Tea TUI
// (internal/tui): the composition root fans every engine event to both, so the
// web dashboard shows exactly what the terminal shows.
//
// This file defines the JSON transport DTOs and the thread-safe State snapshot
// that new clients receive on connect. Monetary values are encoded as strings
// (via decimal.Decimal.String) so precision is never lost to float64.
package web

import (
	"context"

	"github.com/azhar/cerebro/internal/config"
	"github.com/azhar/cerebro/internal/domain"
	"github.com/azhar/cerebro/internal/positionproposal"
	"github.com/azhar/cerebro/internal/uistate"
)

// ─── JSON DTOs ────────────────────────────────────────────────────────────────

// QuoteDTO is a market-watch row.
type QuoteDTO struct {
	Symbol             string `json:"symbol"`
	Bid                string `json:"bid"`
	Ask                string `json:"ask"`
	Mid                string `json:"mid"`
	Last               string `json:"last"`
	PriceChange        string `json:"priceChange"`
	PriceChangePercent string `json:"priceChangePercent"`
	Volume24h          string `json:"volume24h"`
	Timestamp          int64  `json:"timestamp"` // unix millis
}

func quoteToDTO(q domain.Quote) QuoteDTO {
	return QuoteDTO{
		Symbol:             string(q.Symbol),
		Bid:                q.Bid.String(),
		Ask:                q.Ask.String(),
		Mid:                q.Mid.String(),
		Last:               q.Last.String(),
		PriceChange:        q.PriceChange.String(),
		PriceChangePercent: q.PriceChangePercent.String(),
		Volume24h:          q.Volume24h.String(),
		Timestamp:          q.Timestamp.UnixMilli(),
	}
}

// PositionDTO is one open position with its derived PnL fields.
type PositionDTO struct {
	Symbol        string `json:"symbol"`
	Venue         string `json:"venue"`
	Side          string `json:"side"`
	Quantity      string `json:"quantity"`
	EntryPrice    string `json:"entryPrice"`
	CurrentPrice  string `json:"currentPrice"`
	StopLoss      string `json:"stopLoss"`
	TakeProfit1   string `json:"takeProfit1"`
	Strategy      string `json:"strategy"`
	Leverage      int    `json:"leverage"`
	Margin        string `json:"margin"`
	Isolated      bool   `json:"isolated"`
	UnrealizedPnL string `json:"unrealizedPnl"`
	PnLPct        string `json:"pnlPct"`
	PnLROI        string `json:"pnlRoi"`
	OpenedAt      int64  `json:"openedAt"` // unix millis
}

func positionToDTO(p domain.Position) PositionDTO {
	return PositionDTO{
		Symbol:        string(p.Symbol),
		Venue:         string(p.Venue),
		Side:          string(p.Side),
		Quantity:      p.Quantity.String(),
		EntryPrice:    p.EntryPrice.String(),
		CurrentPrice:  p.CurrentPrice.String(),
		StopLoss:      p.StopLoss.String(),
		TakeProfit1:   p.TakeProfit1.String(),
		Strategy:      string(p.Strategy),
		Leverage:      p.Leverage,
		Margin:        p.EffectiveMargin().String(),
		Isolated:      p.Isolated,
		UnrealizedPnL: p.UnrealizedPnL().String(),
		PnLPct:        p.UnrealizedPnLPct().String(),
		PnLROI:        p.UnrealizedPnLROI().String(),
		OpenedAt:      p.OpenedAt.UnixMilli(),
	}
}

// LogDTO is one line in the combined system/agent log.
type LogDTO struct {
	Level string `json:"level"`
	Text  string `json:"text"`
	At    int64  `json:"at"` // unix millis
}

// BiasDTO is one Screening-agent directional read.
type BiasDTO struct {
	Symbol    string `json:"symbol"`
	Score     int    `json:"score"` // -1 bearish, 0 neutral, 1 bullish
	Label     string `json:"label"`
	Reasoning string `json:"reasoning"`
	CachedAt  int64  `json:"cachedAt"`
}

func biasToDTO(b domain.BiasResult) BiasDTO {
	return BiasDTO{
		Symbol:    string(b.Symbol),
		Score:     int(b.Score),
		Label:     b.Score.String(),
		Reasoning: b.Reasoning,
		CachedAt:  b.CachedAt.UnixMilli(),
	}
}

// OpportunityDTO is one ranked cross-symbol entry opportunity (screening Phase 2).
type OpportunityDTO struct {
	ID         string  `json:"id"`
	Symbol     string  `json:"symbol"`
	Venue      string  `json:"venue"`
	Side       string  `json:"side"`       // "buy" | "sell" (lowercased domain.Side)
	Confidence float64 `json:"confidence"` // 0.0–1.0
	Reasoning  string  `json:"reasoning"`
	Avoided    bool    `json:"avoided"` // skipped (e.g. event risk)
	CachedAt   int64   `json:"cachedAt"`
}

func opportunityToDTO(o domain.ScreeningOpportunity) OpportunityDTO {
	return OpportunityDTO{
		ID:         o.ID,
		Symbol:     string(o.Symbol),
		Venue:      string(o.Venue),
		Side:       string(o.Side),
		Confidence: o.Confidence,
		Reasoning:  o.Reasoning,
		Avoided:    o.Avoided,
		CachedAt:   o.CachedAt.UnixMilli(),
	}
}

// AgentRunDTO is one live or completed agent invocation.
type AgentRunDTO struct {
	RunID       string `json:"runId"`
	Agent       string `json:"agent"`
	Step        string `json:"step"`
	ToolName    string `json:"toolName"`
	Provider    string `json:"provider"`
	Model       string `json:"model"`
	Symbol      string `json:"symbol"`
	Description string `json:"description"`
	Content     string `json:"content"`
	StepNum     int    `json:"stepNum"`
	MaxSteps    int    `json:"maxSteps"`
	At          int64  `json:"at"`
}

func agentStateToDTO(s uistate.AgentState) AgentRunDTO {
	return AgentRunDTO{
		RunID:       s.RunID,
		Agent:       s.Agent,
		Step:        string(s.Step),
		ToolName:    s.ToolName,
		Provider:    s.Provider,
		Model:       s.Model,
		Symbol:      s.Symbol,
		Description: s.Description,
		Content:     s.Content,
		StepNum:     s.StepNum,
		MaxSteps:    s.MaxSteps,
		At:          s.At.UnixMilli(),
	}
}

// MacroDTO bundles the macro indicators.
type MacroDTO struct {
	FearGreedValue    int     `json:"fearGreedValue"`
	FearGreedCategory string  `json:"fearGreedCategory"`
	FundingRate       float64 `json:"fundingRate"`
	OpenInterestUSD   string  `json:"openInterestUsd"`
	LongShortRatio    float64 `json:"longShortRatio"`
	UpdatedAt         int64   `json:"updatedAt"`
}

func macroToDTO(s uistate.MacroSnapshot) MacroDTO {
	return MacroDTO{
		FearGreedValue:    s.FearGreed.Value,
		FearGreedCategory: s.FearGreed.Category,
		FundingRate:       s.BTCFundingRate.Rate,
		OpenInterestUSD:   s.BTCOpenInterest.TotalUSD.String(),
		LongShortRatio:    s.BTCLongShort.GlobalRatio,
		UpdatedAt:         s.UpdatedAt.UnixMilli(),
	}
}

// NewsItemDTO is one headline.
type NewsItemDTO struct {
	Title     string `json:"title"`
	Source    string `json:"source"`
	Domain    string `json:"domain"`
	URL       string `json:"url"`
	Sentiment string `json:"sentiment"`
}

// BudgetDTO is the LLM daily-spend snapshot.
type BudgetDTO struct {
	Date          string  `json:"date"`
	TokensUsed    int64   `json:"tokensUsed"`
	CostUSD       float64 `json:"costUsd"`
	TokenBudget   int     `json:"tokenBudget"`
	CostBudgetUSD float64 `json:"costBudgetUsd"`
}

func budgetToDTO(s uistate.BudgetSnapshot) BudgetDTO {
	return BudgetDTO{
		Date:          s.Date,
		TokensUsed:    s.TokensUsed,
		CostUSD:       s.CostUSD,
		TokenBudget:   s.TokenBudget,
		CostBudgetUSD: s.CostBudgetUSD,
	}
}

// ProposalDTO is one pending agent SL/TP adjustment awaiting operator action.
type ProposalDTO struct {
	ID           string `json:"id"`
	Symbol       string `json:"symbol"`
	Venue        string `json:"venue"`
	Side         string `json:"side"`
	CurrentStop  string `json:"currentStop"`
	CurrentTP    string `json:"currentTp"`
	ProposedStop string `json:"proposedStop"`
	ProposedTP   string `json:"proposedTp"`
	Reasoning    string `json:"reasoning"`
	CreatedAt    int64  `json:"createdAt"` // unix millis
}

func proposalToDTO(p positionproposal.Proposal) ProposalDTO {
	return ProposalDTO{
		ID:           p.ID,
		Symbol:       string(p.Symbol),
		Venue:        string(p.Venue),
		Side:         string(p.Side),
		CurrentStop:  p.CurrentStop.String(),
		CurrentTP:    p.CurrentTP.String(),
		ProposedStop: p.ProposedStop.String(),
		ProposedTP:   p.ProposedTP.String(),
		Reasoning:    p.Reasoning,
		CreatedAt:    p.CreatedAt.UnixMilli(),
	}
}

// ─── Config DTOs ────────────────────────────────────────────────────────────

// StrategySummaryDTO is the read-only view of one enabled strategy's effective
// (post-aggressiveness) entry parameters. It mirrors the values the engine
// actually uses, so the dashboard shows the real behaviour, not the raw yaml.
type StrategySummaryDTO struct {
	Name                 string  `json:"name"`
	PrimaryTimeframe     string  `json:"primaryTimeframe"`
	TrendTimeframe       string  `json:"trendTimeframe"`
	RiskPctPerTrade      float64 `json:"riskPctPerTrade"`
	MaxPositionSizePct   float64 `json:"maxPositionSizePct"`
	DedupWindowSeconds   int     `json:"dedupWindowSeconds"`
	ConfirmationCandles  int     `json:"confirmationCandles"`
	RequireBiasAlignment bool    `json:"requireBiasAlignment"`
	OrderType            string  `json:"orderType"`
	Markets              int     `json:"markets"` // count of symbols this strategy fires on
}

// RiskSummaryDTO is the read-only view of the global risk caps. These are hard
// limits the aggressiveness dial never relaxes — shown so operators can see the
// guardrails alongside the per-strategy tuning.
type RiskSummaryDTO struct {
	MaxDrawdownPct         float64 `json:"maxDrawdownPct"`
	MaxDailyLossPct        float64 `json:"maxDailyLossPct"`
	MaxExposurePct         float64 `json:"maxExposurePct"`
	MaxOpenPositions       int     `json:"maxOpenPositions"`
	MaxOpenPositionsVenue  int     `json:"maxOpenPositionsPerVenue"`
	MaxOpenPositionsSymbol int     `json:"maxOpenPositionsPerSymbol"`
	MinEquityToTrade       float64 `json:"minEquityToTrade"`
}

// ConfigDTO is the dashboard's read-only configuration snapshot: the global
// aggressiveness level, the resulting enabled-strategy parameters, and the
// global risk caps. It is static for the lifetime of the process (config is
// loaded once at startup), so it is sent in the initial snapshot only.
type ConfigDTO struct {
	Environment    string               `json:"environment"`
	Aggressiveness string               `json:"aggressiveness"`
	Strategies     []StrategySummaryDTO `json:"strategies"`
	Risk           RiskSummaryDTO       `json:"risk"`
}

// ConfigToDTO projects the loaded config into the dashboard snapshot. Only
// enabled strategies are included, since disabled ones never fire.
func ConfigToDTO(cfg *config.Config) ConfigDTO {
	level := cfg.Aggressiveness
	if level == "" {
		level = domain.AggressivenessBalanced
	}

	strategies := make([]StrategySummaryDTO, 0, len(cfg.Strategies.Strategies))
	for _, s := range cfg.Strategies.Strategies {
		if !s.Enabled {
			continue
		}
		strategies = append(strategies, StrategySummaryDTO{
			Name:                 string(s.Name),
			PrimaryTimeframe:     string(s.PrimaryTimeframe),
			TrendTimeframe:       string(s.TrendTimeframe),
			RiskPctPerTrade:      s.RiskPctPerTrade,
			MaxPositionSizePct:   s.MaxPositionSizePct,
			DedupWindowSeconds:   s.SignalDedupWindowSeconds,
			ConfirmationCandles:  s.ConfirmationCandles,
			RequireBiasAlignment: s.RequireBiasAlignment,
			OrderType:            string(s.OrderType),
			Markets:              len(s.Markets),
		})
	}

	return ConfigDTO{
		Environment:    string(cfg.Environment),
		Aggressiveness: string(level),
		Strategies:     strategies,
		Risk: RiskSummaryDTO{
			MaxDrawdownPct:         cfg.Risk.MaxDrawdownPct,
			MaxDailyLossPct:        cfg.Risk.MaxDailyLossPct,
			MaxExposurePct:         cfg.Risk.MaxExposurePct,
			MaxOpenPositions:       cfg.Risk.MaxOpenPositions,
			MaxOpenPositionsVenue:  cfg.Risk.MaxOpenPositionsPerVenue,
			MaxOpenPositionsSymbol: cfg.Risk.MaxOpenPositionsPerSymbol,
			MinEquityToTrade:       cfg.Risk.MinEquityToTrade,
		},
	}
}

// ─── Editable config DTOs ─────────────────────────────────────────────────────

// RawStrategyDTO is one strategy's RAW (pre-aggressiveness-scaling) editable
// values, sourced straight from strategies.yaml. The edit form pre-fills from
// these so a save round-trips raw values and never double-scales.
type RawStrategyDTO struct {
	Name                 string  `json:"name"`
	Enabled              bool    `json:"enabled"`
	RiskPctPerTrade      float64 `json:"riskPctPerTrade"`
	MaxPositionSizePct   float64 `json:"maxPositionSizePct"`
	DedupWindowSeconds   int     `json:"dedupWindowSeconds"`
	ConfirmationCandles  int     `json:"confirmationCandles"`
	RequireBiasAlignment bool    `json:"requireBiasAlignment"`
	StopLossFixedPct     float64 `json:"stopLossFixedPct"`
	TP1RRRatio           float64 `json:"tp1RrRatio"`
}

// RawStrategiesDTO is the editable view returned by GET /api/strategies/raw:
// the global aggressiveness plus every strategy's raw values.
type RawStrategiesDTO struct {
	Aggressiveness string           `json:"aggressiveness"`
	Strategies     []RawStrategyDTO `json:"strategies"`
}

// StrategyEditDTO is one strategy's edited fields in a POST /api/strategies
// request. All fields are pointers so the client can send only what changed;
// a nil field is left untouched on disk.
type StrategyEditDTO struct {
	Name                 string   `json:"name"`
	Enabled              *bool    `json:"enabled,omitempty"`
	RiskPctPerTrade      *float64 `json:"riskPctPerTrade,omitempty"`
	MaxPositionSizePct   *float64 `json:"maxPositionSizePct,omitempty"`
	DedupWindowSeconds   *int     `json:"dedupWindowSeconds,omitempty"`
	ConfirmationCandles  *int     `json:"confirmationCandles,omitempty"`
	RequireBiasAlignment *bool    `json:"requireBiasAlignment,omitempty"`
	StopLossFixedPct     *float64 `json:"stopLossFixedPct,omitempty"`
	TP1RRRatio           *float64 `json:"tp1RrRatio,omitempty"`
}

// EditRequestDTO is the POST /api/strategies body: an optional aggressiveness
// change plus zero or more per-strategy edits.
type EditRequestDTO struct {
	Aggressiveness string            `json:"aggressiveness,omitempty"`
	Strategies     []StrategyEditDTO `json:"strategies,omitempty"`
}

// StrategyEditor is the runtime hook the web layer calls to read raw editable
// config and apply edits. The app layer implements it (internal/app/reload.go);
// declared here as an interface so the web package stays adapter-only and tests
// can stub it.
type StrategyEditor interface {
	// RawView returns the current pre-scaling editable config.
	RawView() (RawStrategiesDTO, error)
	// ApplyEdits validates and persists the edits, then hot-reloads the engine.
	// On any failure it must leave the on-disk files and live engine unchanged.
	// It returns the post-edit config snapshot for broadcasting to clients.
	ApplyEdits(ctx context.Context, req EditRequestDTO) (ConfigDTO, error)
}

// ─── Config file editing ────────────────────────────────────────────────────

// ConfigFileDTO is one config file's name and raw content, returned by
// GET /api/configs and GET /api/configs/{name}.
type ConfigFileDTO struct {
	Name    string `json:"name"`
	Content string `json:"content"`
}

// ConfigFileSaveRequest is the PUT /api/configs/{name} body.
type ConfigFileSaveRequest struct {
	Content string `json:"content"`
}

// ConfigFileEditor is the runtime hook the web layer calls to read and write
// raw config files with hot-reload. The app layer implements it; declared here
// as an interface so the web package stays adapter-only.
type ConfigFileEditor interface {
	// List returns the names of all editable config files.
	List() []string
	// Read returns one config file's raw content by name.
	Read(name string) (ConfigFileDTO, error)
	// Save writes one config file's raw content, then hot-reloads the engine.
	// On any failure the on-disk file is restored and the live engine is
	// left unchanged. It returns the post-save config snapshot for
	// broadcasting to clients.
	Save(ctx context.Context, name string, content string) (ConfigDTO, error)
	// FormSchema returns the form schema for a config file (for form-based editing).
	FormSchema(name string) (ConfigFormSchemaDTO, error)
	// FormRead returns the current field values as a JSON object.
	FormRead(name string) (map[string]interface{}, error)
	// FormWrite applies field changes from a JSON object, then hot-reloads.
	FormWrite(ctx context.Context, name string, fields map[string]interface{}) (ConfigDTO, error)
}

// ConfigFormSchemaDTO is the schema describing how to render a form for a config file.
type ConfigFormSchemaDTO struct {
	File     string                 `json:"file"`
	Sections []ConfigFormSectionDTO `json:"sections"`
}

// ConfigFormSectionDTO groups related fields.
type ConfigFormSectionDTO struct {
	Title       string                 `json:"title"`
	Description string                 `json:"description,omitempty"`
	Key         string                 `json:"key"`
	Fields      []ConfigFormFieldDTO   `json:"fields"`
	Sections    []ConfigFormSectionDTO `json:"sections"`
}

// ConfigFormFieldDTO describes a single editable field.
type ConfigFormFieldDTO struct {
	Name        string      `json:"name"`  // yaml key path (e.g., "position_manager.enabled")
	Label       string      `json:"label"` // human-readable label
	Type        string      `json:"type"`  // string, number, bool, select, object
	Value       interface{} `json:"value"` // current value
	Options     []string    `json:"options"`
	Description string      `json:"description,omitempty"`
	Required    bool        `json:"required,omitempty"`
	Min         interface{} `json:"min,omitempty"`
	Max         interface{} `json:"max,omitempty"`
	Step        interface{} `json:"step,omitempty"`
}

// OrderIntentDTO is the JSON transport for the order-history view's intent
// rows. The DTO exposes every field the dashboard renders: identity, entry
// contract (type, prices, TIF), and the protective bracket request. Status
// is included so a single row can show the full lifecycle (pending →
// submitted → filled/rejected/cancelled).
type OrderIntentDTO struct {
	ID            string `json:"id"`
	CorrelationID string `json:"correlationId"`
	Symbol        string `json:"symbol"`
	Venue         string `json:"venue"`
	Side          string `json:"side"`
	OrderType     string `json:"orderType"`
	Quantity      string `json:"quantity"`
	LimitPrice    string `json:"limitPrice,omitempty"`
	StopPrice     string `json:"stopPrice,omitempty"`
	StopLoss      string `json:"stopLoss,omitempty"`
	TakeProfit1   string `json:"takeProfit1,omitempty"`
	TIF           string `json:"timeInForce,omitempty"`
	ReduceOnly    bool   `json:"reduceOnly"`
	PositionSide  string `json:"positionSide,omitempty"`
	Leverage      int    `json:"leverage"`
	Strategy      string `json:"strategy"`
	Environment   string `json:"environment"`
	Status        string `json:"status"`
	CreatedAt     int64  `json:"createdAt"`
}

func orderIntentToDTO(i domain.OrderIntent) OrderIntentDTO {
	dto := OrderIntentDTO{
		ID:            i.ID,
		CorrelationID: i.CorrelationID,
		Symbol:        string(i.Symbol),
		Venue:         string(i.Venue),
		Side:          string(i.Side),
		OrderType:     string(i.OrderTypeOrDefault()),
		Quantity:      i.Quantity.String(),
		TIF:           string(i.TIF),
		ReduceOnly:    i.ReduceOnly,
		PositionSide:  string(i.PositionSide),
		Leverage:      i.Leverage,
		Strategy:      string(i.Strategy),
		Environment:   string(i.Environment),
		Status:        string(i.Status),
		CreatedAt:     i.CreatedAt.UnixMilli(),
	}
	if !i.LimitPrice.IsZero() {
		dto.LimitPrice = i.LimitPrice.String()
	}
	if !i.StopPrice.IsZero() {
		dto.StopPrice = i.StopPrice.String()
	}
	if !i.StopLoss.IsZero() {
		dto.StopLoss = i.StopLoss.String()
	}
	if !i.TakeProfit1.IsZero() {
		dto.TakeProfit1 = i.TakeProfit1.String()
	}
	return dto
}

// TradeDTO is the JSON transport for a closed trade row. PnL is included
// even when nil so the order-history view can render a placeholder for
// open legs (PnL only finalises when the position closes).
type TradeDTO struct {
	ID            string  `json:"id"`
	IntentID      string  `json:"intentId"`
	CorrelationID string  `json:"correlationId"`
	Symbol        string  `json:"symbol"`
	Side          string  `json:"side"`
	Quantity      string  `json:"quantity"`
	FillPrice     string  `json:"fillPrice"`
	Fees          string  `json:"fees"`
	PnL           *string `json:"pnl,omitempty"`
	Strategy      string  `json:"strategy"`
	Venue         string  `json:"venue"`
	ClosedAt      *int64  `json:"closedAt,omitempty"`
	CreatedAt     int64   `json:"createdAt"`
}

func tradeToDTO(t domain.Trade) TradeDTO {
	dto := TradeDTO{
		ID:            t.ID,
		IntentID:      t.IntentID,
		CorrelationID: t.CorrelationID,
		Symbol:        string(t.Symbol),
		Side:          string(t.Side),
		Quantity:      t.Quantity.String(),
		FillPrice:     t.FillPrice.String(),
		Fees:          t.Fees.String(),
		Strategy:      string(t.Strategy),
		Venue:         string(t.Venue),
		CreatedAt:     t.CreatedAt.UnixMilli(),
	}
	if t.PnL != nil {
		s := t.PnL.String()
		dto.PnL = &s
	}
	if t.ClosedAt != nil {
		ms := t.ClosedAt.UnixMilli()
		dto.ClosedAt = &ms
	}
	return dto
}

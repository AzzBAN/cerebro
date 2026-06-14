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

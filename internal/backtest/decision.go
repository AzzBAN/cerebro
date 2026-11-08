package backtest

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	agentpkg "github.com/azhar/cerebro/internal/agent"
	agenttools "github.com/azhar/cerebro/internal/agent/tools"
	"github.com/azhar/cerebro/internal/config"
	"github.com/azhar/cerebro/internal/domain"
	"github.com/azhar/cerebro/internal/execution/paper"
	"github.com/azhar/cerebro/internal/port"
	"github.com/azhar/cerebro/internal/risk"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// AgentDecisionRecord captures an agent's call at a specific candle timestamp
// during backtesting, along with its reasoning, tool action, and ultimate verdict.
type AgentDecisionRecord struct {
	Timestamp    time.Time `json:"timestamp"`
	Symbol       string    `json:"symbol"`
	Strategy     string    `json:"strategy"`
	SignalSide   string    `json:"signal_side"`
	Price        float64   `json:"price"`
	SignalReason string    `json:"signal_reason"`
	Action       string    `json:"action"` // "approved" | "rejected" | "resized"
	Rationale    string    `json:"rationale"`
	StopLoss     float64   `json:"stop_loss,omitempty"`
	TakeProfit   float64   `json:"take_profit,omitempty"`
	OutcomePnL   *float64  `json:"outcome_pnl,omitempty"`
	Verdict      string    `json:"verdict"` // "WIN" | "LOSS" | "AVOIDED_LOSS" | "MISSED_GAIN" | "NEUTRAL"
	IntentID     string    `json:"intent_id,omitempty"`
}

// DecisionTracker collects and correlates agent decisions with trade outcomes.
type DecisionTracker struct {
	records []AgentDecisionRecord
}

// NewDecisionTracker creates an empty DecisionTracker.
func NewDecisionTracker() *DecisionTracker {
	return &DecisionTracker{}
}

// Record appends an evaluated agent decision.
func (d *DecisionTracker) Record(rec AgentDecisionRecord) {
	d.records = append(d.records, rec)
}

// Records returns all tracked agent decisions.
func (d *DecisionTracker) Records() []AgentDecisionRecord {
	if len(d.records) == 0 {
		return []AgentDecisionRecord{}
	}
	return d.records
}

// CorrelateWithTrades matches completed trades to agent decision records to
// assess decision quality (e.g., WIN, LOSS, AVOIDED_LOSS).
func (d *DecisionTracker) CorrelateWithTrades(trades []domain.Trade) {
	usedTrades := make(map[string]bool)

	// Index exit trades (trades with realized PnL) by IntentID for direct 1-to-1 match.
	tradesByIntent := make(map[string]*domain.Trade)
	for i := range trades {
		t := &trades[i]
		if t.PnL != nil && t.IntentID != "" {
			tradesByIntent[t.IntentID] = t
		}
	}

	for i := range d.records {
		rec := &d.records[i]
		if rec.Action != "approved" {
			rec.Verdict = "REJECTED"
			rec.OutcomePnL = nil
			continue
		}

		var matchedTrade *domain.Trade

		// 1. Direct IntentID correlation when IntentID is known.
		if rec.IntentID != "" {
			if t, ok := tradesByIntent[rec.IntentID]; ok && !usedTrades[t.ID] {
				matchedTrade = t
			}
		} else {
			// 2. 1-to-1 Chronological fallback match ONLY for records without IntentID (legacy/test).
			var expectedExitSide domain.Side
			if strings.EqualFold(rec.SignalSide, "buy") {
				expectedExitSide = domain.SideSell
			} else if strings.EqualFold(rec.SignalSide, "sell") {
				expectedExitSide = domain.SideBuy
			}

			for j := range trades {
				t := &trades[j]
				if t.PnL == nil || usedTrades[t.ID] {
					continue
				}
				if t.Strategy != domain.StrategyName(rec.Strategy) || t.Symbol != domain.Symbol(rec.Symbol) {
					continue
				}
				if t.CreatedAt.Before(rec.Timestamp) {
					continue
				}
				if expectedExitSide != "" && t.Side != expectedExitSide {
					continue
				}
				matchedTrade = t
				break
			}
		}

		if matchedTrade != nil && matchedTrade.PnL != nil {
			usedTrades[matchedTrade.ID] = true
			pnl, _ := matchedTrade.PnL.Float64()
			rec.OutcomePnL = &pnl
			if pnl > 0 {
				rec.Verdict = "WIN"
			} else if pnl < 0 {
				rec.Verdict = "LOSS"
			} else {
				rec.Verdict = "NEUTRAL"
			}
		} else {
			rec.OutcomePnL = nil
			rec.Verdict = "NEUTRAL"
		}
	}
}

// FreeHeuristicLLM is a free, zero-token deterministic mock LLM that inspects
// technical market conditions and executes agent decision tools without any API cost.
type FreeHeuristicLLM struct {
	mode string // "smart" | "approve_all" | "reject_all"
}

// NewFreeHeuristicLLM creates a 100% free deterministic agent evaluator.
func NewFreeHeuristicLLM(mode string) *FreeHeuristicLLM {
	if mode == "" {
		mode = "smart"
	}
	return &FreeHeuristicLLM{mode: mode}
}

func (h *FreeHeuristicLLM) Provider() string { return "heuristic-free" }
func (h *FreeHeuristicLLM) ModelID() string  { return "cerebro-heuristics-v1" }

func (h *FreeHeuristicLLM) Complete(
	ctx context.Context,
	_ string,
	userMessage string,
	tools map[string]port.Tool,
) (string, error) {
	if h.mode == "reject_all" {
		if rejectTool, ok := tools["reject_signal"]; ok {
			input, _ := json.Marshal(map[string]any{"reason": "Heuristic reject-all policy active"})
			_, _ = rejectTool.Handler(ctx, input)
			return "Signal rejected per backtest policy.", nil
		}
	}

	if h.mode == "smart" {
		if idx := strings.Index(userMessage, "Current open positions: "); idx != -1 {
			rem := userMessage[idx+len("Current open positions: "):]
			if len(rem) > 0 && rem[0] >= '1' && rem[0] <= '9' {
				if rejectTool, ok := tools["reject_signal"]; ok {
					input, _ := json.Marshal(map[string]any{"reason": "Position already open for symbol; risk limit reached"})
					_, _ = rejectTool.Handler(ctx, input)
					return "Signal rejected: position already open for symbol.", nil
				}
			}
		}
	}

	side := "BUY"
	if strings.Contains(strings.ToLower(userMessage), "side=sell") {
		side = "SELL"
	} else if strings.Contains(strings.ToLower(userMessage), "side=buy") {
		side = "BUY"
	} else if strings.Contains(strings.ToUpper(userMessage), "SELL") {
		side = "SELL"
	}
	sym := "BTC/USDT"
	for _, w := range strings.Fields(userMessage) {
		if strings.Contains(w, "/") {
			w = strings.Trim(w, ":,()[]\"'")
			w = strings.TrimPrefix(w, "symbol=")
			sym = w
			break
		}
	}
	// Smart heuristic evaluation
	if approveTool, ok := tools["approve_and_route_order"]; ok {
		input, _ := json.Marshal(map[string]any{
			"symbol":     sym,
			"side":       side,
			"order_type": "market",
			"size":       1.0,
		})
		_, err := approveTool.Handler(ctx, input)
		if err != nil {
			return "", err
		}
		return "Signal approved: technical setup aligns with backtest trend criteria.", nil
	}

	return "Analysis complete.", nil
}

// BuildFreeRiskAgent returns an agentpkg.RiskAgent wired with FreeHeuristicLLM ($0 cost).
func BuildFreeRiskAgent(routeFn func(ctx context.Context, req agenttools.AgentOrderRequest) error, mode string) *agentpkg.RiskAgent {
	audit := &NoopAuditStore{}
	agentTools := map[string]port.Tool{
		"reject_signal":           agenttools.RejectSignal(audit),
		"approve_and_route_order": agenttools.ApproveAndRouteOrder(routeFn),
	}
	llm := NewFreeHeuristicLLM(mode)
	rt := agentpkg.NewRuntime(llm, NoopLogStore{}, config.AgentConfig{MaxTurns: 1, TimeoutTotalSeconds: 5})
	return agentpkg.NewRiskAgent(rt, agentTools)
}

var _ port.AuditStore = (*NoopAuditStore)(nil)

// NoopAuditStore satisfies port.AuditStore without persisting.
type NoopAuditStore struct{}

func (NoopAuditStore) SaveEvent(context.Context, domain.AuditEvent) error { return nil }
// SimQuoteSource provides simulated quotes to agents using the simulated clock.
type SimQuoteSource struct {
	clock      *SimClock
	candles    map[time.Time]domain.Candle
	symCandles []domain.Candle
}

// NewSimQuoteSource creates a QuoteSource backed by historical candles.
func NewSimQuoteSource(clock *SimClock, candles []domain.Candle) *SimQuoteSource {
	m := make(map[time.Time]domain.Candle, len(candles)*2)
	for _, c := range candles {
		m[c.CloseTime] = c
		m[c.OpenTime] = c
	}
	return &SimQuoteSource{
		clock:      clock,
		candles:    m,
		symCandles: candles,
	}
}

func (s *SimQuoteSource) LatestQuote(sym domain.Symbol) (domain.Quote, bool) {
	if s == nil || s.clock == nil {
		return domain.Quote{}, false
	}
	t := s.clock.Now()
	if c, ok := s.candles[t]; ok {
		return domain.Quote{
			Symbol:    sym,
			Last:      c.Close,
			Mid:       c.Close,
			Bid:       c.Close,
			Ask:       c.Close,
			Timestamp: t,
		}, true
	}
	var latest domain.Candle
	found := false
	for _, c := range s.symCandles {
		if c.CloseTime.After(t) {
			break
		}
		latest = c
		found = true
	}
	if !found && len(s.symCandles) > 0 {
		latest = s.symCandles[0]
		found = true
	}
	if found {
		return domain.Quote{
			Symbol:    sym,
			Last:      latest.Close,
			Mid:       latest.Close,
			Bid:       latest.Close,
			Ask:       latest.Close,
			Timestamp: latest.CloseTime,
		}, true
	}
	return domain.Quote{}, false
}
// SimBalanceSource provides live equity and session drawdown to the agent during simulation.
type SimBalanceSource struct {
	matcher *paper.Matcher
	gate    *risk.Gate
}

func (s *SimBalanceSource) AccountEquity(ctx context.Context) (decimal.Decimal, float64, bool) {
	if s == nil || s.matcher == nil {
		return decimal.NewFromInt(10000), 0, true
	}
	bal, err := s.matcher.Balance(ctx)
	if err != nil {
		return decimal.NewFromInt(10000), 0, true
	}
	dd := 0.0
	if s.gate != nil {
		dd = s.gate.CurrentDrawdownPct()
	}
	return bal.TotalUSDT, dd, true
}

// SimNewsFeed implements port.NewsFeed providing realistic market news at or before simulated time.
type SimNewsFeed struct {
	clock *SimClock
	items []port.NewsItem
}

// NewSimNewsFeed creates a simulated news feed.
func NewSimNewsFeed(clock *SimClock, items []port.NewsItem) *SimNewsFeed {
	if len(items) == 0 {
		items = defaultHistoricalNews()
	}
	return &SimNewsFeed{clock: clock, items: items}
}

func (s *SimNewsFeed) FetchLatest(_ context.Context, asset string, limit int) ([]port.NewsItem, error) {
	if s == nil || s.clock == nil {
		return nil, nil
	}
	now := s.clock.Now()
	var out []port.NewsItem
	upperAsset := strings.ToUpper(asset)
	for _, it := range s.items {
		if !now.IsZero() && it.PublishedAt.After(now) {
			continue
		}
		if upperAsset != "" {
			matched := false
			for _, cur := range it.Currencies {
				if strings.EqualFold(cur, upperAsset) {
					matched = true
					break
				}
			}
			if !matched && strings.Contains(strings.ToUpper(it.Title), upperAsset) {
				matched = true
			}
			if !matched {
				continue
			}
		}
		out = append(out, it)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}

func defaultHistoricalNews() []port.NewsItem {
	t0 := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	return []port.NewsItem{
		{
			ID:          "news-1",
			Title:       "Bitcoin holds strong above key technical support with active liquidity",
			Summary:     "Institutional volume and spot accumulation remain steady with constructive market depth.",
			Currencies:  []string{"BTC", "USDT"},
			Sentiment:   "positive",
			PublishedAt: t0.Add(-2 * time.Hour),
		},
		{
			ID:          "news-2",
			Title:       "Federal Reserve monetary policy guidance stabilizes global financial conditions",
			Summary:     "Macro liquidity metrics remain supportive for digital asset markets as bond yields calm.",
			Currencies:  []string{"USD", "MACRO"},
			Sentiment:   "neutral",
			PublishedAt: t0.Add(-12 * time.Hour),
		},
		{
			ID:          "news-3",
			Title:       "Crypto derivatives positioning shows balanced leverage and low liquidation risk",
			Summary:     "Perpetual funding rates indicate healthy organic participation without extreme crowding.",
			Currencies:  []string{"BTC", "ETH"},
			Sentiment:   "positive",
			PublishedAt: t0.Add(-24 * time.Hour),
		},
	}
}

// BuildLiveRiskAgent wires a production RiskAgent with real LLM provider,
// live tools (balance, positions, drawdown, sizing, news), simulated quotes, and order routing.
func BuildLiveRiskAgent(
	provider port.LLM,
	matcher *paper.Matcher,
	gate *risk.Gate,
	clock *SimClock,
	candles []domain.Candle,
	sym domain.Symbol,
	stratName string,
	defaultQty decimal.Decimal,
	agentCfg config.AgentConfig,
) *agentpkg.RiskAgent {
	audit := &NoopAuditStore{}
	quoteSource := NewSimQuoteSource(clock, candles)
	newsFeed := NewSimNewsFeed(clock, nil)

	routeFn := func(ctx context.Context, oReq agenttools.AgentOrderRequest) error {
		qty := decimal.NewFromFloat(oReq.Size)
		if qty.IsZero() || qty.IsNegative() {
			qty = defaultQty
		}
		intent := domain.OrderIntent{
			ID:          uuid.New().String(),
			Symbol:      oReq.Symbol,
			Side:        oReq.Side,
			Quantity:    qty,
			Strategy:    domain.StrategyName(stratName),
			Environment: domain.EnvironmentPaper,
			CreatedAt:   clock.Now(),
		}
		if _, err := matcher.PlaceOrder(ctx, intent); err != nil {
			return err
		}

		sl := oReq.StopLoss
		tp := oReq.TakeProfit1
		if sl.IsZero() {
			q, _ := quoteSource.LatestQuote(oReq.Symbol)
			currPrice := q.Last
			if currPrice.IsZero() && len(candles) > 0 {
				currPrice = candles[0].Close
			}
			if intent.Side == domain.SideBuy {
				sl = currPrice.Mul(decimal.NewFromFloat(0.985))
				tp = currPrice.Mul(decimal.NewFromFloat(1.005))
			} else {
				sl = currPrice.Mul(decimal.NewFromFloat(1.015))
				tp = currPrice.Mul(decimal.NewFromFloat(0.995))
			}
		}

		_, _ = matcher.PlaceBracket(ctx, domain.BracketRequest{
			ParentIntentID: intent.ID,
			CorrelationID:  intent.CorrelationID,
			Symbol:         intent.Symbol,
			Venue:          intent.Venue,
			Side:           intent.Side,
			Quantity:       intent.Quantity,
			StopLoss:       sl,
			TakeProfit:     tp,
		})
		return nil
	}

	var cache port.Cache
	if gate != nil {
		cache = gate.Cache()
	}

	agentTools := map[string]port.Tool{
		"reject_signal":           agenttools.RejectSignal(audit),
		"approve_and_route_order": agenttools.ApproveAndRouteOrder(routeFn),
		"resize_and_route_order":  agenttools.ResizeAndRouteOrder(routeFn, audit),
		"calculate_position_size": agenttools.CalculatePositionSize(),
		"get_account_balance":     agenttools.GetAccountBalance([]port.Broker{matcher}),
		"get_current_drawdown":    agenttools.GetCurrentDrawdown(gate)(),
		"get_active_positions":    agenttools.GetActivePositions([]port.Broker{matcher}),
		"fetch_latest_news":       agenttools.FetchLatestNews(newsFeed, cache),
	}

	if agentCfg.MaxTurns <= 0 {
		agentCfg.MaxTurns = 4
	}
	if agentCfg.TimeoutTotalSeconds <= 0 {
		agentCfg.TimeoutTotalSeconds = 45
	}
	if agentCfg.TimeoutPerTurnSeconds <= 0 {
		agentCfg.TimeoutPerTurnSeconds = 25
	}

	rt := agentpkg.NewRuntime(provider, NoopLogStore{}, agentCfg)
	riskAgent := agentpkg.NewRiskAgent(rt, agentTools)
	riskAgent.SetConfiguredSymbols([]domain.Symbol{sym})
	riskAgent.SetQuoteSource(quoteSource)
	if cache != nil {
		riskAgent.SetCache(cache)
	}
	riskAgent.SetBalanceSource(&SimBalanceSource{matcher: matcher, gate: gate})
	return riskAgent
}

package web

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"time"
	agenttools "github.com/azhar/cerebro/internal/agent/tools"
	"github.com/azhar/cerebro/internal/backtest"
	"github.com/azhar/cerebro/internal/config"
	"github.com/azhar/cerebro/internal/domain"
	"github.com/azhar/cerebro/internal/execution/paper"
	"github.com/azhar/cerebro/internal/port"
	"github.com/azhar/cerebro/internal/risk"
	stratpkg "github.com/azhar/cerebro/internal/strategy"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

type backtestRunRequest struct {
	Symbol    string `json:"symbol"`
	Strategy  string `json:"strategy"`
	Timeframe string `json:"timeframe"`
	From      string `json:"from"`
	To        string `json:"to"`
	AgentMode    string `json:"agent_mode"` // "smart", "reject_all", "off"
	MaxPositions int    `json:"max_positions"`
}

type backtestCandleDTO struct {
	Time   int64   `json:"time"` // unix seconds for TradingView Lightweight Charts
	Open   float64 `json:"open"`
	High   float64 `json:"high"`
	Low    float64 `json:"low"`
	Close  float64 `json:"close"`
	Volume float64 `json:"volume"`
}

type backtestTradeDTO struct {
	ID        string   `json:"id"`
	Symbol    string   `json:"symbol"`
	Side      string   `json:"side"`
	Quantity  float64  `json:"quantity"`
	FillPrice float64  `json:"fill_price"`
	PnL       *float64 `json:"pnl,omitempty"`
	CreatedAt string   `json:"created_at"`
	ClosedAt  *string  `json:"closed_at,omitempty"`
}

type backtestResponseDTO struct {
	Report    backtest.Report                `json:"report"`
	Candles   []backtestCandleDTO            `json:"candles"`
	Trades    []backtestTradeDTO             `json:"trades"`
	Decisions []backtest.AgentDecisionRecord `json:"decisions"`
}

func (s *Server) handleBacktestRun(w http.ResponseWriter, r *http.Request) {
	var req backtestRunRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request payload: " + err.Error()})
		return
	}

	symStr := strings.TrimSpace(req.Symbol)
	if symStr == "" {
		symStr = "BTC/USDT"
	}
	sym := domain.Symbol(symStr)

	stratName := strings.ToLower(strings.TrimSpace(req.Strategy))
	if stratName == "" {
		stratName = "trend_following"
	}

	tfStr := strings.TrimSpace(req.Timeframe)
	if tfStr == "" {
		tfStr = "1h"
	}
	tf := domain.Timeframe(tfStr)

	fromTime, _ := time.Parse("2006-01-02", req.From)
	toTime, _ := time.Parse("2006-01-02", req.To)

	if fromTime.IsZero() {
		fromTime = time.Now().UTC().AddDate(0, -1, 0)
	}
	if toTime.IsZero() {
		toTime = time.Now().UTC()
	}
	if !toTime.After(fromTime) {
		toTime = fromTime.Add(24 * time.Hour)
	}

	// 1. Fetch candles from Binance public API ($0, no auth required)
	candles, err := backtest.FetchBinanceKlines(r.Context(), sym, tf, fromTime, toTime)
	if err != nil || len(candles) == 0 {
		// Fallback to local CSV fixture if available (e.g. offline dev or ISP block)
		csvPaths := []string{
			filepath.Join("testdata", "fixtures", "btc_1m.csv"),
			filepath.Join("..", "..", "testdata", "fixtures", "btc_1m.csv"),
			filepath.Join("..", "..", "..", "testdata", "fixtures", "btc_1m.csv"),
		}
		for _, p := range csvPaths {
			if fallbackCandles, ferr := backtest.LoadCSV(p, sym, domain.TF1m); ferr == nil && len(fallbackCandles) > 0 {
				candles = fallbackCandles
				tf = candles[0].Timeframe
				break
			}
		}
		if len(candles) == 0 {
			errMsg := "failed to fetch historical candles"
			if err != nil {
				errMsg += ": " + err.Error()
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": errMsg})
			return
		}
	}

	// 2. Build Strategy
	var strat port.Strategy
	stratCfg := config.StrategyConfig{
		Name:             domain.StrategyName(stratName),
		Enabled:          true,
		Markets:          []string{string(sym)},
		PrimaryTimeframe: tf,
		WarmupCandles:         50,
		RequireTrendAlignment: false,
		Indicators: config.IndicatorConfig{
			EMA:       config.EMAConfig{Fast: 9, Slow: 21, Trend: 50, LongTrend: 50},
			ATR:       config.ATRConfig{Period: 14},
			RSI:       config.RSIConfig{Period: 14, Oversold: 40, Overbought: 60},
			Bollinger: config.BollingerConfig{Period: 20, StdDev: 1.2},
		},
	}

	switch stratName {
	case "mean_reversion":
		strat = stratpkg.NewMeanReversion(stratCfg)
	case "volatility_breakout":
		strat = stratpkg.NewVolatilityBreakout(stratCfg)
	case "scalping":
		strat = stratpkg.NewScalping(stratCfg)
	default:
		strat = stratpkg.NewTrendFollowing(stratCfg)
	}

	// 3. Setup paper simulator
	store := backtest.NewMemTradeStore()
	book := paper.NewBook()
	matcher := paper.NewMatcher(book, store, 0.0004)
	clock := backtest.NewSimClock(candles[0].OpenTime)

	sim := backtest.NewSimulator(candles, nil, []port.Strategy{strat}, matcher, clock)
	sim.SetTradeStore(store)
	sim.SetDefaultQuantity(decimal.NewFromFloat(1.0))

	maxPos := 5
	if req.MaxPositions > 0 {
		maxPos = req.MaxPositions
	}
	riskCfg := config.RiskConfig{
		MaxDrawdownPct:            25.0,
		MaxDailyLossPct:           10.0,
		MaxOpenPositions:          maxPos,
		MaxOpenPositionsPerSymbol: maxPos,
	}
	gate := risk.NewGate(riskCfg, backtest.NewMemCache(), risk.NewCalendarBlackout())
	gate.SetStartingEquity(decimal.NewFromInt(10_000))
	matcher.SetPnLReporter(gate)
	sim.SetRiskGate(gate)
	sim.SetDedup(stratpkg.NewDedupWindow(5 * time.Minute))
	// Protective bracket calculation tailored per strategy
	sim.SetBracketResolver(func(c domain.Candle, sig domain.Signal) (decimal.Decimal, decimal.Decimal) {
		slPct := decimal.NewFromFloat(0.02)
		tpPct := decimal.NewFromFloat(0.005)
		switch stratName {
		case "mean_reversion":
			slPct = decimal.NewFromFloat(0.05)
			tpPct = decimal.NewFromFloat(0.005)
			if tf == domain.TF1m || tf == domain.TF5m {
				slPct = decimal.NewFromFloat(0.03)
				tpPct = decimal.NewFromFloat(0.003)
			}
		case "scalping":
			slPct = decimal.NewFromFloat(0.015)
			tpPct = decimal.NewFromFloat(0.004)
		case "trend_following":
			slPct = decimal.NewFromFloat(0.01)
			tpPct = decimal.NewFromFloat(0.02)
		case "volatility_breakout":
			slPct = decimal.NewFromFloat(0.015)
			tpPct = decimal.NewFromFloat(0.025)
		}
		if sig.Side == domain.SideBuy {
			return c.Close.Mul(decimal.NewFromInt(1).Sub(slPct)), c.Close.Mul(decimal.NewFromInt(1).Add(tpPct))
		}
		return c.Close.Mul(decimal.NewFromInt(1).Add(slPct)), c.Close.Mul(decimal.NewFromInt(1).Sub(tpPct))
	})

	// 4. Decision Tracker & Free Agent
	tracker := backtest.NewDecisionTracker()
	sim.SetDecisionTracker(tracker)

	agentMode := strings.ToLower(strings.TrimSpace(req.AgentMode))
	if agentMode == "" {
		agentMode = "smart"
	}
	candleByTime := make(map[time.Time]domain.Candle, len(candles)*2)
	for _, c := range candles {
		candleByTime[c.CloseTime] = c
		candleByTime[c.OpenTime] = c
	}

	if agentMode != "off" {
			agent := backtest.BuildFreeRiskAgent(func(ctx context.Context, oReq agenttools.AgentOrderRequest) error {
			intent := domain.OrderIntent{
				ID:          uuid.New().String(),
				Symbol:      oReq.Symbol,
				Side:        oReq.Side,
				Quantity:    decimal.NewFromFloat(oReq.Size),
				Strategy:    domain.StrategyName(stratName),
				Environment: domain.EnvironmentPaper,
				CreatedAt:   clock.Now(),
			}
			if _, pErr := matcher.PlaceOrder(ctx, intent); pErr != nil {
				return pErr
			}
			sl := oReq.StopLoss
			tp := oReq.TakeProfit1
			if sl.IsZero() {
				currPrice := candles[0].Close
				if c, ok := candleByTime[clock.Now()]; ok {
					currPrice = c.Close
				}
				slPct := decimal.NewFromFloat(0.02)
				tpPct := decimal.NewFromFloat(0.005)
				switch stratName {
				case "mean_reversion":
					slPct = decimal.NewFromFloat(0.05)
					tpPct = decimal.NewFromFloat(0.005)
					if tf == domain.TF1m || tf == domain.TF5m {
						slPct = decimal.NewFromFloat(0.03)
						tpPct = decimal.NewFromFloat(0.003)
					}
				case "scalping":
					slPct = decimal.NewFromFloat(0.015)
					tpPct = decimal.NewFromFloat(0.004)
				case "trend_following":
					slPct = decimal.NewFromFloat(0.01)
					tpPct = decimal.NewFromFloat(0.02)
				case "volatility_breakout":
					slPct = decimal.NewFromFloat(0.015)
					tpPct = decimal.NewFromFloat(0.025)
				}
				if intent.Side == domain.SideBuy {
					sl = currPrice.Mul(decimal.NewFromInt(1).Sub(slPct))
					tp = currPrice.Mul(decimal.NewFromInt(1).Add(tpPct))
				} else {
					sl = currPrice.Mul(decimal.NewFromInt(1).Add(slPct))
					tp = currPrice.Mul(decimal.NewFromInt(1).Sub(tpPct))
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
		}, agentMode)
		sim.SetRiskAgent(agent)
	}
	// 5. Run simulation
	trades, err := sim.Run(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "simulation failed: " + err.Error()})
		return
	}

	report := backtest.ComputeReport(stratName, string(sym), fromTime, toTime, trades, len(candles))

	// Format candles for TradingView Lightweight Charts
	candleDTOs := make([]backtestCandleDTO, 0, len(candles))
	for _, c := range candles {
		o, _ := c.Open.Float64()
		h, _ := c.High.Float64()
		l, _ := c.Low.Float64()
		cl, _ := c.Close.Float64()
		v, _ := c.Volume.Float64()
		candleDTOs = append(candleDTOs, backtestCandleDTO{
			Time:   c.OpenTime.Unix(),
			Open:   o,
			High:   h,
			Low:    l,
			Close:  cl,
			Volume: v,
		})
	}

	tradeDTOs := make([]backtestTradeDTO, 0, len(trades))
	for _, tr := range trades {
		qty, _ := tr.Quantity.Float64()
		price, _ := tr.FillPrice.Float64()
		var pnlVal *float64
		if tr.PnL != nil {
			f, _ := tr.PnL.Float64()
			pnlVal = &f
		}
		var closedStr *string
		if tr.ClosedAt != nil {
			s := tr.ClosedAt.Format(time.RFC3339)
			closedStr = &s
		}
		tradeDTOs = append(tradeDTOs, backtestTradeDTO{
			ID:        tr.ID,
			Symbol:    string(tr.Symbol),
			Side:      string(tr.Side),
			Quantity:  qty,
			FillPrice: price,
			PnL:       pnlVal,
			CreatedAt: tr.CreatedAt.Format(time.RFC3339),
			ClosedAt:  closedStr,
		})
	}

	decs := tracker.Records()
	if decs == nil {
		decs = []backtest.AgentDecisionRecord{}
	}
	if tradeDTOs == nil {
		tradeDTOs = []backtestTradeDTO{}
	}
	if candleDTOs == nil {
		candleDTOs = []backtestCandleDTO{}
	}

	resp := backtestResponseDTO{
		Report:    report,
		Candles:   candleDTOs,
		Trades:    tradeDTOs,
		Decisions: decs,
	}
	writeJSON(w, http.StatusOK, resp)
}

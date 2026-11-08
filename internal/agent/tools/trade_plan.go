package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/adshao/go-binance/v2"
	gobinancefutures "github.com/adshao/go-binance/v2/futures"
	"github.com/azhar/cerebro/internal/adapter/binance/futures"
	"github.com/azhar/cerebro/internal/adapter/binance/spot"
	"github.com/azhar/cerebro/internal/domain"
	"github.com/azhar/cerebro/internal/port"
	"github.com/azhar/cerebro/internal/screening"
	"github.com/shopspring/decimal"
)

// GenerateTradePlan implements the generate_trade_plan agent tool.
// It fetches klines, builds features, classifies regime, matches strategy,
// and generates a complete trade plan with entry/SL/TP levels.
func GenerateTradePlan(spotClient *binance.Client, futuresClient *gobinancefutures.Client) port.Tool {
	return port.Tool{
		Handler: func(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
			var args struct {
				Symbol      string  `json:"symbol"`
				Timeframe   string  `json:"timeframe"`
				Side        string  `json:"side"`
				StopLossPct float64 `json:"stop_loss_pct"`
				TP1RR       float64 `json:"tp1_rr"`
			}
			if err := json.Unmarshal(input, &args); err != nil {
				return nil, fmt.Errorf("generate_trade_plan: bad args: %w", err)
			}
			if args.Symbol == "" {
				return nil, fmt.Errorf("generate_trade_plan: symbol is required")
			}
			if args.Timeframe == "" {
				args.Timeframe = "1h"
			}

			sym := normalizeToolSymbol(args.Symbol)
			tf := domain.Timeframe(args.Timeframe)

			// Determine venue from symbol suffix
			var fetcher KlineFetcher
			if strings.HasSuffix(string(sym), "-PERP") || strings.HasSuffix(string(sym), "_PERP") {
				if futuresClient == nil {
					return nil, fmt.Errorf("generate_trade_plan: futures client not configured")
				}
				fetcher = func(ctx context.Context, symbol domain.Symbol, tf domain.Timeframe, limit int) ([]domain.Candle, error) {
					return futures.FetchKlines(ctx, futuresClient, symbol, tf, limit)
				}
			} else {
				if spotClient == nil {
					return nil, fmt.Errorf("generate_trade_plan: spot client not configured")
				}
				fetcher = func(ctx context.Context, symbol domain.Symbol, tf domain.Timeframe, limit int) ([]domain.Candle, error) {
					return spot.FetchKlines(ctx, spotClient, symbol, tf, limit)
				}
			}

			candles, err := fetcher(ctx, sym, tf, 200)
			if err != nil {
				return nil, fmt.Errorf("generate_trade_plan: fetch klines: %w", err)
			}
			if len(candles) < 50 {
				return nil, fmt.Errorf("generate_trade_plan: insufficient data (%d candles, need >= 50)", len(candles))
			}

			lastCandle := candles[len(candles)-1]

			// Build Features from klines
			var priceChange24h float64
			if len(candles) >= 24 {
				startPrice := candles[len(candles)-24].Close
				change := lastCandle.Close.Sub(startPrice).Div(startPrice).Mul(decimal.NewFromInt(100))
				priceChange24h, _ = change.Float64()
			}

			features := screening.Features{
				Symbol:           sym,
				Venue:            domain.VenueBinanceFutures, // default, adjust if needed
				LastPrice:        lastCandle.Close,
				PriceChangePct24: priceChange24h,
				QuoteVolume24h:   decimal.Zero, // not available from klines
				FundingRate:      0,            // not available without CoinGlass
				OIChange24hPct:   0,
				OIChange4hPct:    0,
				Liquidations24h:  0,
				LiqLongRatio:     0,
				LongShortRatio:   0,
			}

			// Set venue based on symbol
			if strings.HasSuffix(string(sym), "-PERP") || strings.HasSuffix(string(sym), "_PERP") {
				features.Venue = domain.VenueBinanceFutures
			} else {
				features.Venue = domain.VenueBinanceSpot
			}

			// Classify regime
			regime, side := screening.Classify(features, screening.DefaultThresholds())

			// Override side if user specified
			if args.Side != "" {
				if strings.ToUpper(args.Side) == "BUY" {
					side = domain.SideBuy
				} else if strings.ToUpper(args.Side) == "SELL" {
					side = domain.SideSell
				}
			}

			// Match strategies (all enabled)
			fits := screening.MatchStrategies(features, regime, side, nil, 1)
			if len(fits) == 0 {
				return nil, fmt.Errorf("generate_trade_plan: no strategy fit for regime %s", regime)
			}
			fit := fits[0]

			// Build plan params
			params := screening.DefaultPlanParams()
			if args.StopLossPct > 0 {
				params.StopLossPct = args.StopLossPct
			}
			if args.TP1RR > 0 {
				params.TP1RR = args.TP1RR
			}

			// Build trade plan
			plan, ok := screening.BuildTradePlan(
				features,
				regime,
				side,
				fit,
				domain.BiasNeutral,
				params,
				time.Now(),
			)
			if !ok {
				return nil, fmt.Errorf("generate_trade_plan: failed to build trade plan")
			}

			// Convert to JSON-friendly format
			result := map[string]any{
				"symbol":        string(plan.Symbol),
				"venue":         string(plan.Venue),
				"regime":        string(plan.Regime),
				"strategy":      string(plan.Strategy),
				"side":          string(plan.Side),
				"confidence":    plan.Confidence,
				"last_price":    plan.LastPrice.String(),
				"entry_low":     plan.EntryLow.String(),
				"entry_high":    plan.EntryHigh.String(),
				"stop_loss":     plan.StopLoss.String(),
				"take_profit_1": plan.TakeProfit1.String(),
				"rr_ratio":      plan.RRRatio,
				"reasoning":     plan.Reasoning,
				"generated_at":  plan.GeneratedAt.Format(time.RFC3339),
			}

			if !plan.TakeProfit2.IsZero() {
				result["take_profit_2"] = plan.TakeProfit2.String()
			}

			return json.Marshal(result)
		},
		Definition: port.ToolDefinition{
			Name:        "generate_trade_plan",
			Description: "Generate entry/SL/TP levels for any symbol using technical analysis. Fetches klines, classifies market regime, matches best strategy, and produces a complete trade plan with risk/reward ratio.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"symbol": map[string]any{
						"type":        "string",
						"description": "Symbol in any format: BTCUSDT, BTC/USDT, ETH/USDT-PERP",
					},
					"timeframe": map[string]any{
						"type":        "string",
						"description": "Timeframe: 1m, 5m, 15m, 1h, 4h, 1d (default: 1h)",
					},
					"side": map[string]any{
						"type":        "string",
						"description": "Trade side: BUY or SELL (optional, auto-detected if omitted)",
					},
					"stop_loss_pct": map[string]any{
						"type":        "number",
						"description": "Stop loss distance as percentage (default: 0.6%)",
					},
					"tp1_rr": map[string]any{
						"type":        "number",
						"description": "Take profit risk-reward ratio (default: 1.8R)",
					},
				},
				"required": []string{"symbol"},
			},
		},
	}
}

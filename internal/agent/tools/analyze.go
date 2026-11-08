package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/adshao/go-binance/v2"
	gobinancefutures "github.com/adshao/go-binance/v2/futures"
	"github.com/azhar/cerebro/internal/adapter/binance/futures"
	"github.com/azhar/cerebro/internal/adapter/binance/spot"
	"github.com/azhar/cerebro/internal/domain"
	"github.com/azhar/cerebro/internal/port"
	"github.com/azhar/cerebro/internal/strategy/indicators"
	"github.com/shopspring/decimal"
)

// gobinance aliases for readability in the handler body
type spotClient = binance.Client
type futuresClient = gobinancefutures.Client

// KlineFetcher fetches klines for a symbol/timeframe from a venue.
type KlineFetcher func(ctx context.Context, symbol domain.Symbol, tf domain.Timeframe, limit int) ([]domain.Candle, error)

// AnalyzeSymbol implements the analyze_symbol agent tool.
// It fetches klines via REST and computes technical indicators.
func AnalyzeSymbol(spotClient *binance.Client, futuresClient *gobinancefutures.Client) port.Tool {
	return port.Tool{
		Handler: func(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
			var args struct {
				Symbol    string `json:"symbol"`
				Timeframe string `json:"timeframe"`
				Limit     int    `json:"limit"`
			}
			if err := json.Unmarshal(input, &args); err != nil {
				return nil, fmt.Errorf("analyze_symbol: bad args: %w", err)
			}
			if args.Symbol == "" {
				return nil, fmt.Errorf("analyze_symbol: symbol is required")
			}
			if args.Timeframe == "" {
				args.Timeframe = "1h"
			}
			if args.Limit <= 0 {
				args.Limit = 200
			}

			sym := normalizeToolSymbol(args.Symbol)
			tf := domain.Timeframe(args.Timeframe)

			// Determine venue from symbol suffix
			var fetcher KlineFetcher
			if strings.HasSuffix(string(sym), "-PERP") || strings.HasSuffix(string(sym), "_PERP") {
				if futuresClient == nil {
					return nil, fmt.Errorf("analyze_symbol: futures client not configured")
				}
				fetcher = func(ctx context.Context, symbol domain.Symbol, tf domain.Timeframe, limit int) ([]domain.Candle, error) {
					return futures.FetchKlines(ctx, futuresClient, symbol, tf, limit)
				}
			} else {
				if spotClient == nil {
					return nil, fmt.Errorf("analyze_symbol: spot client not configured")
				}
				fetcher = func(ctx context.Context, symbol domain.Symbol, tf domain.Timeframe, limit int) ([]domain.Candle, error) {
					return spot.FetchKlines(ctx, spotClient, symbol, tf, limit)
				}
			}

			candles, err := fetcher(ctx, sym, tf, args.Limit)
			if err != nil {
				return nil, fmt.Errorf("analyze_symbol: fetch klines: %w", err)
			}
			if len(candles) < 50 {
				return nil, fmt.Errorf("analyze_symbol: insufficient data (%d candles, need >= 50)", len(candles))
			}

			// Compute indicators
			atr14 := indicators.NewATR(14)
			rsi14 := indicators.NewRSI(14)
			ema20 := indicators.NewEMA(20)
			ema50 := indicators.NewEMA(50)
			ema200 := indicators.NewEMA(200)
			bb := indicators.NewBollinger(20, 2.0)

			for _, c := range candles {
				atr14.Add(c)
				rsi14.Add(c.Close)
				ema20.Add(c.Close)
				ema50.Add(c.Close)
				ema200.Add(c.Close)
				bb.Add(c.Close)
			}

			lastCandle := candles[len(candles)-1]
			result := map[string]any{
				"symbol":    string(sym),
				"timeframe": string(tf),
				"price":     lastCandle.Close.String(),
				"candles":   len(candles),
			}

			if v, ok := atr14.Value(); ok {
				result["atr_14"] = v.String()
			}
			if v, ok := rsi14.Value(); ok {
				result["rsi_14"] = v.String()
			}
			if v, ok := ema20.Value(); ok {
				result["ema_20"] = v.String()
			}
			if v, ok := ema50.Value(); ok {
				result["ema_50"] = v.String()
			}
			if v, ok := ema200.Value(); ok {
				result["ema_200"] = v.String()
			}
			if upper, middle, lower, ok := bb.Bands(); ok {
				result["bb_upper"] = upper.String()
				result["bb_middle"] = middle.String()
				result["bb_lower"] = lower.String()
			}

			// Simple trend detection
			if ema20v, ok20 := ema20.Value(); ok20 {
				if ema50v, ok50 := ema50.Value(); ok50 {
					if ema200v, ok200 := ema200.Value(); ok200 {
						if ema20v.GreaterThan(ema50v) && ema50v.GreaterThan(ema200v) {
							result["trend"] = "bullish"
						} else if ema20v.LessThan(ema50v) && ema50v.LessThan(ema200v) {
							result["trend"] = "bearish"
						} else {
							result["trend"] = "mixed"
						}
					}
				}
			}

			// Regime hint based on price action
			if len(candles) >= 24 {
				startPrice := candles[len(candles)-24].Close
				change := lastCandle.Close.Sub(startPrice).Div(startPrice).Mul(decimal.NewFromInt(100))
				changeF, _ := change.Float64()
				if changeF > 4.0 {
					result["regime_hint"] = "trending"
				} else if changeF < -4.0 {
					result["regime_hint"] = "trending"
				} else if changeF > -2.0 && changeF < 2.0 {
					result["regime_hint"] = "range"
				}
			}

			return json.Marshal(result)
		},
		Definition: port.ToolDefinition{
			Name:        "analyze_symbol",
			Description: "Fetch klines and compute technical indicators (ATR, RSI, EMA, Bollinger Bands) for ANY symbol on Binance. Works for symbols not in markets.yaml. Returns indicator snapshot for technical analysis.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"symbol": map[string]any{
						"type":        "string",
						"description": "Symbol in any format: BTCUSDT, BTC/USDT, BTW/USDT-PERP",
					},
					"timeframe": map[string]any{
						"type":        "string",
						"description": "Timeframe: 1m, 5m, 15m, 1h, 4h, 1d (default: 1h)",
					},
					"limit": map[string]any{
						"type":        "integer",
						"description": "Number of candles to fetch (default: 200, max: 1000)",
					},
				},
				"required": []string{"symbol"},
			},
		},
	}
}

package backtest

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/azhar/cerebro/internal/domain"
	"github.com/shopspring/decimal"
)

var defaultHTTPClient = &http.Client{Timeout: 15 * time.Second}

// FetchBinanceKlines downloads historical OHLCV data directly from Binance's
// public REST endpoint. Free, public, no API key required.
// Paginates in chunks of 1000 candles from start to end.
func FetchBinanceKlines(
	ctx context.Context,
	symbol domain.Symbol,
	tf domain.Timeframe,
	start time.Time,
	end time.Time,
) ([]domain.Candle, error) {
	symStr := strings.ToUpper(strings.TrimSpace(string(symbol)))
	isPerp := strings.HasSuffix(symStr, "-PERP") || strings.HasSuffix(symStr, "PERP")
	cleanSym := domain.ToExchangeSymbol(domain.Symbol(symStr))
	if cleanSym == "" {
		cleanSym = strings.ReplaceAll(strings.ReplaceAll(strings.TrimSuffix(symStr, "-PERP"), "/", ""), "-", "")
	}
	baseURL := "https://api.binance.com/api/v3/klines"
	if isPerp {
		baseURL = "https://fapi.binance.com/fapi/v1/klines"
	}
	interval := string(tf)
	if interval == "" {
		interval = "1h"
	}
	startMs := start.UnixMilli()
	endMs := end.UnixMilli()
	if endMs <= startMs {
		return nil, fmt.Errorf("end time must be after start time")
	}

	var candles []domain.Candle
	curStart := startMs

	for curStart < endMs {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		apiURL := fmt.Sprintf(
			"%s?symbol=%s&interval=%s&startTime=%d&endTime=%d&limit=1000",
			baseURL, cleanSym, interval, curStart, endMs,
		)

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
		if err != nil {
			return nil, fmt.Errorf("create klines request: %w", err)
		}
		req.Header.Set("User-Agent", "Cerebro-Backtest/1.0")

		resp, err := defaultHTTPClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("fetch klines from binance: %w", err)
		}

		body, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("read klines response: %w", err)
		}

		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("binance public API error (status %d): %s", resp.StatusCode, string(body))
		}

		var raw [][]any
		if err := json.Unmarshal(body, &raw); err != nil {
			return nil, fmt.Errorf("parse binance klines json: %w", err)
		}

		if len(raw) == 0 {
			break
		}

		var lastCloseMs int64
		for _, row := range raw {
			if len(row) < 6 {
				continue
			}
			openMs, ok := toInt64(row[0])
			if !ok {
				continue
			}
			closeMs, ok := toInt64(row[6])
			if !ok {
				closeMs = openMs + int64(timeframeDuration(tf)/time.Millisecond)
			}
			lastCloseMs = closeMs

			open, err := decimal.NewFromString(fmt.Sprintf("%v", row[1]))
			if err != nil {
				continue
			}
			high, err := decimal.NewFromString(fmt.Sprintf("%v", row[2]))
			if err != nil {
				continue
			}
			low, err := decimal.NewFromString(fmt.Sprintf("%v", row[3]))
			if err != nil {
				continue
			}
			close_, err := decimal.NewFromString(fmt.Sprintf("%v", row[4]))
			if err != nil {
				continue
			}
			vol, err := decimal.NewFromString(fmt.Sprintf("%v", row[5]))
			if err != nil {
				continue
			}

			candles = append(candles, domain.Candle{
				Symbol:    symbol,
				Timeframe: tf,
				OpenTime:  time.UnixMilli(openMs).UTC(),
				CloseTime: time.UnixMilli(closeMs).UTC(),
				Open:      open,
				High:      high,
				Low:       low,
				Close:     close_,
				Volume:    vol,
				Closed:    true,
			})
		}

		if len(raw) < 1000 || lastCloseMs <= curStart {
			break
		}
		curStart = lastCloseMs + 1
	}

	return candles, nil
}

func toInt64(v any) (int64, bool) {
	switch val := v.(type) {
	case float64:
		return int64(val), true
	case int64:
		return val, true
	case json.Number:
		n, err := val.Int64()
		return n, err == nil
	default:
		return 0, false
	}
}

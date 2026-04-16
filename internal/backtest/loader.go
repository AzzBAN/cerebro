package backtest

import (
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"strconv"
	"time"

	"github.com/azhar/cerebro/internal/domain"
	"github.com/shopspring/decimal"
)

// CSVCandle is the expected CSV column order:
// timestamp_ms, open, high, low, close, volume
// Header row is optional (detected automatically).
const csvTimeLayout = "2006-01-02 15:04:05"

// LoadCSV loads historical candle data from a CSV file.
// Expected columns (comma-separated, with optional header):
//   timestamp_ms OR timestamp (YYYY-MM-DD HH:MM:SS), open, high, low, close, volume
func LoadCSV(path string, symbol domain.Symbol, tf domain.Timeframe) ([]domain.Candle, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("backtest: open CSV %s: %w", path, err)
	}
	defer f.Close()

	r := csv.NewReader(f)
	r.TrimLeadingSpace = true

	var candles []domain.Candle
	lineNum := 0

	for {
		record, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("backtest: read CSV line %d: %w", lineNum, err)
		}
		lineNum++

		// Skip header row.
		if lineNum == 1 && !isNumeric(record[0]) {
			continue
		}
		if len(record) < 6 {
			return nil, fmt.Errorf("backtest: CSV line %d: expected 6 columns, got %d", lineNum, len(record))
		}

		openTime, err := parseTimestamp(record[0])
		if err != nil {
			return nil, fmt.Errorf("backtest: CSV line %d timestamp: %w", lineNum, err)
		}

		open, err := decimal.NewFromString(record[1])
		if err != nil {
			return nil, fmt.Errorf("backtest: CSV line %d open: %w", lineNum, err)
		}
		high, err := decimal.NewFromString(record[2])
		if err != nil {
			return nil, fmt.Errorf("backtest: CSV line %d high: %w", lineNum, err)
		}
		low, err := decimal.NewFromString(record[3])
		if err != nil {
			return nil, fmt.Errorf("backtest: CSV line %d low: %w", lineNum, err)
		}
		close_, err := decimal.NewFromString(record[4])
		if err != nil {
			return nil, fmt.Errorf("backtest: CSV line %d close: %w", lineNum, err)
		}
		vol, err := decimal.NewFromString(record[5])
		if err != nil {
			return nil, fmt.Errorf("backtest: CSV line %d volume: %w", lineNum, err)
		}

		candles = append(candles, domain.Candle{
			Symbol:    symbol,
			Timeframe: tf,
			OpenTime:  openTime,
			CloseTime: openTime.Add(timeframeDuration(tf)),
			Open:      open,
			High:      high,
			Low:       low,
			Close:     close_,
			Volume:    vol,
			Closed:    true,
		})
	}

	return candles, nil
}

func parseTimestamp(s string) (time.Time, error) {
	// Try Unix milliseconds first.
	if ms, err := strconv.ParseInt(s, 10, 64); err == nil {
		return time.Unix(ms/1000, (ms%1000)*int64(time.Millisecond)).UTC(), nil
	}
	// Try Unix seconds.
	if sec, err := strconv.ParseInt(s, 10, 64); err == nil && sec > 1000000000 {
		return time.Unix(sec, 0).UTC(), nil
	}
	// Try human-readable format.
	t, err := time.Parse(csvTimeLayout, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("cannot parse timestamp %q", s)
	}
	return t.UTC(), nil
}

func isNumeric(s string) bool {
	_, err := strconv.ParseFloat(s, 64)
	return err == nil
}

func timeframeDuration(tf domain.Timeframe) time.Duration {
	switch tf {
	case domain.TF1m:
		return time.Minute
	case domain.TF5m:
		return 5 * time.Minute
	case domain.TF15m:
		return 15 * time.Minute
	case domain.TF1h:
		return time.Hour
	case domain.TF4h:
		return 4 * time.Hour
	case domain.TF1d:
		return 24 * time.Hour
	default:
		return time.Minute
	}
}

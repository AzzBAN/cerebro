package indicators

import (
	"testing"
	"time"

	"github.com/azhar/cerebro/internal/domain"
	"github.com/shopspring/decimal"
)

func makeCandle(open, high, low, close int) domain.Candle {
	return domain.Candle{
		Open:      decimal.NewFromInt(int64(open)),
		High:      decimal.NewFromInt(int64(high)),
		Low:       decimal.NewFromInt(int64(low)),
		Close:     decimal.NewFromInt(int64(close)),
		OpenTime:  time.Now().UTC(),
		CloseTime: time.Now().UTC(),
	}
}

func TestATR_NotReady(t *testing.T) {
	atr := NewATR(3)
	atr.Add(makeCandle(10, 12, 9, 11))

	_, ready := atr.Value()
	if ready {
		t.Error("ATR should not be ready with fewer than period candles")
	}
}

func TestATR_SimpleCalculation(t *testing.T) {
	atr := NewATR(3)

	// Candle 1: TR = H - L = 12 - 9 = 3
	// Candle 2: TR = max(14-11, |14-11|, |11-11|) = 3
	// Candle 3: TR = max(15-12, |15-14|, |12-14|) = 3
	atr.Add(makeCandle(10, 12, 9, 11))   // TR = 3
	atr.Add(makeCandle(11, 14, 11, 14))  // TR = max(3, 3, 2) = 3
	atr.Add(makeCandle(14, 15, 12, 13))  // TR = max(3, 1, 2) = 3

	val, ready := atr.Value()
	if !ready {
		t.Fatal("ATR should be ready")
	}
	// SMA of TRs = (3 + 3 + 3) / 3 = 3
	if !val.Equal(decimal.NewFromInt(3)) {
		t.Errorf("ATR = %s, want 3", val)
	}
}

func TestATR_WilderSmoothing(t *testing.T) {
	atr := NewATR(3)

	atr.Add(makeCandle(10, 12, 9, 11))   // TR = 3
	atr.Add(makeCandle(11, 14, 11, 14))  // TR = 3
	atr.Add(makeCandle(14, 15, 12, 13))  // TR = 3, ATR = 3
	atr.Add(makeCandle(13, 16, 13, 15))  // TR = max(3, 3, 0) = 3

	val, _ := atr.Value()
	// Wilder: (3 * (3-1) + 3) / 3 = (6 + 3) / 3 = 3
	if !val.Equal(decimal.NewFromInt(3)) {
		t.Errorf("ATR = %s, want 3", val)
	}
}

func TestATR_TrueRangeWithGaps(t *testing.T) {
	atr := NewATR(2)

	// Candle 1: TR = 105 - 95 = 10
	atr.Add(makeCandle(100, 105, 95, 100))

	// Candle 2 gaps up: open=120, TR = max(125-115, |125-100|, |115-100|) = max(10, 25, 15) = 25
	atr.Add(makeCandle(120, 125, 115, 122))

	val, ready := atr.Value()
	if !ready {
		t.Fatal("ATR should be ready")
	}
	// ATR = (10 + 25) / 2 = 17.5
	want := decimal.NewFromFloat(17.5)
	if !val.Equal(want) {
		t.Errorf("ATR = %s, want %s", val, want)
	}
}

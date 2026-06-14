package execution

import (
	"testing"
	"time"

	"github.com/azhar/cerebro/internal/domain"
	"github.com/shopspring/decimal"
)

func posWithPnL(sym string, entryPrice, currentPrice int64) domain.Position {
	return domain.Position{
		Symbol:       domain.Symbol(sym),
		Venue:        domain.VenueBinanceFutures,
		Side:         domain.SideBuy,
		EntryPrice:   decimal.NewFromInt(entryPrice),
		CurrentPrice: decimal.NewFromInt(currentPrice),
		Quantity:     decimal.NewFromInt(1),
		StopLoss:     decimal.NewFromFloat(float64(entryPrice) * 0.95),
		TakeProfit1:  decimal.NewFromFloat(float64(entryPrice) * 1.10),
	}
}

func TestTriggerDetector_ProfitThreshold_Fires(t *testing.T) {
	d := NewTriggerDetector(60, 5.0, 0, false)
	// 10% profit on a long position — above the 5% threshold
	pos := posWithPnL("BTCUSDT", 100, 110)
	triggers := d.Detect([]domain.Position{pos}, nil)
	if len(triggers) != 1 {
		t.Fatalf("expected 1 trigger, got %d", len(triggers))
	}
	if triggers[0].Type != domain.TriggerProfitThreshold {
		t.Errorf("trigger type = %q, want %q", triggers[0].Type, domain.TriggerProfitThreshold)
	}
}

func TestTriggerDetector_ProfitThreshold_BelowDoesNotFire(t *testing.T) {
	d := NewTriggerDetector(60, 5.0, 0, false)
	// 2% profit — below 5% threshold
	pos := posWithPnL("BTCUSDT", 100, 102)
	triggers := d.Detect([]domain.Position{pos}, nil)
	if len(triggers) != 0 {
		t.Errorf("expected no triggers, got %d", len(triggers))
	}
}

func TestTriggerDetector_NearSL_Fires(t *testing.T) {
	// nearTPSLPct=2: fires when price is within 2% of SL or TP1
	d := NewTriggerDetector(60, 0, 2.0, false)
	pos := domain.Position{
		Symbol:       "ETHUSDT",
		Venue:        domain.VenueBinanceFutures,
		Side:         domain.SideBuy,
		EntryPrice:   decimal.NewFromInt(3000),
		CurrentPrice: decimal.NewFromInt(2970), // ~1% above SL of 2950
		Quantity:     decimal.NewFromInt(1),
		StopLoss:     decimal.NewFromInt(2950),
		TakeProfit1:  decimal.NewFromInt(3300),
	}
	triggers := d.Detect([]domain.Position{pos}, nil)
	if len(triggers) != 1 {
		t.Fatalf("expected 1 near-SL trigger, got %d", len(triggers))
	}
	if triggers[0].Type != domain.TriggerNearTPSL {
		t.Errorf("trigger type = %q, want %q", triggers[0].Type, domain.TriggerNearTPSL)
	}
}

func TestTriggerDetector_NearTP_Fires(t *testing.T) {
	d := NewTriggerDetector(60, 0, 2.0, false)
	pos := domain.Position{
		Symbol:       "ETHUSDT",
		Venue:        domain.VenueBinanceFutures,
		Side:         domain.SideBuy,
		EntryPrice:   decimal.NewFromInt(3000),
		CurrentPrice: decimal.NewFromInt(3290), // ~0.3% below TP1 of 3300
		Quantity:     decimal.NewFromInt(1),
		StopLoss:     decimal.NewFromInt(2850),
		TakeProfit1:  decimal.NewFromInt(3300),
	}
	triggers := d.Detect([]domain.Position{pos}, nil)
	if len(triggers) != 1 {
		t.Fatalf("expected 1 near-TP trigger, got %d", len(triggers))
	}
	if triggers[0].Type != domain.TriggerNearTPSL {
		t.Errorf("trigger type = %q, want %q", triggers[0].Type, domain.TriggerNearTPSL)
	}
}

func TestTriggerDetector_BiasFlip_Fires(t *testing.T) {
	d := NewTriggerDetector(60, 0, 0, true)
	pos := domain.Position{
		Symbol:       "BTCUSDT",
		Venue:        domain.VenueBinanceFutures,
		Side:         domain.SideBuy, // long position
		EntryPrice:   decimal.NewFromInt(100),
		CurrentPrice: decimal.NewFromInt(100),
		Quantity:     decimal.NewFromInt(1),
	}
	// Bearish bias opposes a long position
	biasFor := func(sym domain.Symbol) (domain.BiasScore, bool) {
		return domain.BiasBearish, true
	}
	triggers := d.Detect([]domain.Position{pos}, biasFor)
	if len(triggers) != 1 {
		t.Fatalf("expected 1 bias-flip trigger, got %d", len(triggers))
	}
	if triggers[0].Type != domain.TriggerBiasFlipAgainst {
		t.Errorf("trigger type = %q, want %q", triggers[0].Type, domain.TriggerBiasFlipAgainst)
	}
}

func TestTriggerDetector_BiasFlip_AlignedDoesNotFire(t *testing.T) {
	d := NewTriggerDetector(60, 0, 0, true)
	pos := domain.Position{
		Symbol:       "BTCUSDT",
		Venue:        domain.VenueBinanceFutures,
		Side:         domain.SideBuy,
		EntryPrice:   decimal.NewFromInt(100),
		CurrentPrice: decimal.NewFromInt(100),
		Quantity:     decimal.NewFromInt(1),
	}
	// Bullish bias aligns with a long position — no trigger
	biasFor := func(sym domain.Symbol) (domain.BiasScore, bool) {
		return domain.BiasBullish, true
	}
	triggers := d.Detect([]domain.Position{pos}, biasFor)
	if len(triggers) != 0 {
		t.Errorf("expected no triggers, got %d", len(triggers))
	}
}

func TestTriggerDetector_Debounce_SuppressesRepeat(t *testing.T) {
	d := NewTriggerDetector(60, 5.0, 0, false)
	pos := posWithPnL("BTCUSDT", 100, 110)

	first := d.Detect([]domain.Position{pos}, nil)
	if len(first) != 1 {
		t.Fatalf("expected 1 trigger on first call, got %d", len(first))
	}
	second := d.Detect([]domain.Position{pos}, nil)
	if len(second) != 0 {
		t.Errorf("expected debounce to suppress repeat trigger, got %d", len(second))
	}
}

func TestTriggerDetector_Debounce_ExpiresAndRefires(t *testing.T) {
	d := NewTriggerDetector(1, 5.0, 0, false) // 1-second debounce
	pos := posWithPnL("BTCUSDT", 100, 110)

	first := d.Detect([]domain.Position{pos}, nil)
	if len(first) != 1 {
		t.Fatalf("expected 1 trigger on first call, got %d", len(first))
	}

	// Manually backdate the last-fire time to simulate expiry.
	d.mu.Lock()
	key := triggerKey{Symbol: "BTCUSDT", Trigger: domain.TriggerProfitThreshold}
	d.lastFire[key] = time.Now().Add(-2 * time.Second)
	d.mu.Unlock()

	second := d.Detect([]domain.Position{pos}, nil)
	if len(second) != 1 {
		t.Errorf("expected re-fire after debounce expiry, got %d", len(second))
	}
}

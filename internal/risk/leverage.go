package risk

import (
	"fmt"

	"github.com/azhar/cerebro/internal/domain"
	"github.com/shopspring/decimal"
)

// LeverageResult holds dynamic leverage and liquidation calculations.
type LeverageResult struct {
	Quantity            decimal.Decimal `json:"quantity"`
	Notional            decimal.Decimal `json:"notional"`
	MaxSafeLeverage     int             `json:"max_safe_leverage"`
	RecommendedLeverage int             `json:"recommended_leverage"`
	LiquidationPrice    decimal.Decimal `json:"liquidation_price"`
	RequiredMargin      decimal.Decimal `json:"required_margin"`
	StopLossDistancePct decimal.Decimal `json:"stop_loss_distance_pct"`
}

// CalculateSafeLeverage computes safe leverage such that estimated liquidation
// is at least 1.5x the stop-loss distance away from entry price.
func CalculateSafeLeverage(
	entryPrice, stopLoss decimal.Decimal,
	side domain.Side,
	equity decimal.Decimal,
	riskPct float64,
	mmr decimal.Decimal,
) (LeverageResult, error) {
	if entryPrice.IsZero() || entryPrice.IsNegative() {
		return LeverageResult{}, fmt.Errorf("entry price must be positive")
	}
	if stopLoss.IsZero() || stopLoss.IsNegative() {
		return LeverageResult{}, fmt.Errorf("stop loss must be positive")
	}
	if equity.IsZero() || equity.IsNegative() {
		return LeverageResult{}, fmt.Errorf("equity must be positive")
	}
	if riskPct <= 0 {
		return LeverageResult{}, fmt.Errorf("risk pct must be positive")
	}
	if mmr.IsZero() {
		mmr = decimal.NewFromFloat(0.005) // 0.5% default maintenance margin rate
	}

	slDist := entryPrice.Sub(stopLoss).Abs()
	if slDist.IsZero() {
		return LeverageResult{}, fmt.Errorf("stop loss distance is zero")
	}

	slDistPct := slDist.Div(entryPrice)
	riskAmt := equity.Mul(decimal.NewFromFloat(riskPct / 100))
	qty := riskAmt.Div(slDist)
	notional := qty.Mul(entryPrice)

	// Safe buffer: require liquidation to be at least 1.5x the stop-loss distance away.
	safeBufferDistPct := slDistPct.Mul(decimal.NewFromFloat(1.5)).Add(mmr)
	if safeBufferDistPct.GreaterThanOrEqual(decimal.NewFromInt(1)) {
		safeBufferDistPct = decimal.NewFromFloat(0.99)
	}

	// Max safe leverage = 1 / safeBufferDistPct
	maxSafeLevDecimal := decimal.NewFromInt(1).Div(safeBufferDistPct)
	maxSafeLev := int(maxSafeLevDecimal.IntPart())
	if maxSafeLev < 1 {
		maxSafeLev = 1
	}
	if maxSafeLev > 125 {
		maxSafeLev = 125
	}

	// Recommended leverage: balance margin requirements against risk.
	recLev := maxSafeLev
	if recLev > 20 {
		recLev = 20
	}
	if recLev < 1 {
		recLev = 1
	}

	reqMargin := notional.Div(decimal.NewFromInt(int64(recLev)))

	// Liquidation price calculation:
	// Long: Liq = Entry * (1 - 1/Lev + MMR)
	// Short: Liq = Entry * (1 + 1/Lev - MMR)
	levDec := decimal.NewFromInt(int64(recLev))
	var liqPrice decimal.Decimal
	if side == domain.SideBuy {
		factor := decimal.NewFromInt(1).Sub(decimal.NewFromInt(1).Div(levDec)).Add(mmr)
		liqPrice = entryPrice.Mul(factor)
		if liqPrice.IsNegative() {
			liqPrice = decimal.Zero
		}
	} else {
		factor := decimal.NewFromInt(1).Add(decimal.NewFromInt(1).Div(levDec)).Sub(mmr)
		liqPrice = entryPrice.Mul(factor)
	}

	return LeverageResult{
		Quantity:            qty,
		Notional:            notional,
		MaxSafeLeverage:     maxSafeLev,
		RecommendedLeverage: recLev,
		LiquidationPrice:    liqPrice,
		RequiredMargin:      reqMargin,
		StopLossDistancePct: slDistPct.Mul(decimal.NewFromInt(100)),
	}, nil
}

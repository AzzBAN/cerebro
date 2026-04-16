package risk

import (
	"fmt"

	"github.com/azhar/cerebro/internal/domain"
	"github.com/shopspring/decimal"
)

// CalculatePositionSize computes the position quantity for a given trade.
// Formula: quantity = (accountEquity × riskPct) / stopLossDistance
// The result is clamped to [minLot, maxLot] and validated against minNotional.
func CalculatePositionSize(
	accountEquity decimal.Decimal,
	riskPctPerTrade float64,
	entryPrice decimal.Decimal,
	stopLoss decimal.Decimal,
	minLot decimal.Decimal,
	maxLot decimal.Decimal,
	minNotional decimal.Decimal,
) (domain.RiskParams, error) {
	if accountEquity.IsZero() || accountEquity.IsNegative() {
		return domain.RiskParams{}, fmt.Errorf("account equity must be positive")
	}
	if entryPrice.IsZero() {
		return domain.RiskParams{}, fmt.Errorf("entry price must be > 0")
	}
	if stopLoss.IsZero() {
		return domain.RiskParams{}, fmt.Errorf("stop loss must be set")
	}

	slDistance := entryPrice.Sub(stopLoss).Abs()
	if slDistance.IsZero() {
		return domain.RiskParams{}, fmt.Errorf("stop loss distance is zero")
	}

	riskAmt := accountEquity.Mul(decimal.NewFromFloat(riskPctPerTrade / 100))
	qty := riskAmt.Div(slDistance)

	// Clamp to lot bounds.
	if !minLot.IsZero() && qty.LessThan(minLot) {
		qty = minLot
	}
	if !maxLot.IsZero() && qty.GreaterThan(maxLot) {
		qty = maxLot
	}

	// Validate min notional.
	notional := qty.Mul(entryPrice)
	if !minNotional.IsZero() && notional.LessThan(minNotional) {
		return domain.RiskParams{}, fmt.Errorf("position notional %.2f < min notional %.2f",
			notional.InexactFloat64(), minNotional.InexactFloat64())
	}

	return domain.RiskParams{
		Quantity:        qty,
		StopLoss:        stopLoss,
		RiskAmountQuote: riskAmt,
	}, nil
}

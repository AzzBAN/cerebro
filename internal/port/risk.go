package port

import "github.com/shopspring/decimal"

// PnLReporter receives realised trade PnL as positions close. The execution
// layer depends on this narrow interface (not the concrete *risk.Gate) so
// that the drawdown / daily-loss limits in the gate are fed by real fills
// without the execution package importing risk for anything more than this.
//
// *risk.Gate satisfies this via its UpdatePnL method. Implementations MUST be
// safe for concurrent use — the matcher/monitor call it from the per-venue
// execution goroutines.
type PnLReporter interface {
	// UpdatePnL records a realised trade PnL (positive = profit, negative =
	// loss) so session and daily drawdown limits can trip.
	UpdatePnL(pnl decimal.Decimal)
}

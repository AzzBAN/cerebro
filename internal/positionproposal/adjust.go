package positionproposal

import (
	"context"
	"fmt"

	"github.com/azhar/cerebro/internal/domain"
)

// Bracketer places and cancels protective brackets (satisfied by port.Broker).
type Bracketer interface {
	PlaceBracket(ctx context.Context, req domain.BracketRequest) (domain.BracketResponse, error)
	CancelBracket(ctx context.Context, resp domain.BracketResponse) error
}

// ProtectiveLookup exposes the externally-set protective orders cached by the
// adapter for a symbol (implemented by *futures.FuturesBroker / *spot.SpotBroker).
type ProtectiveLookup interface {
	ProtectiveBracket(sym domain.Symbol) (domain.BracketResponse, bool)
}

// Recorder records a freshly-placed bracket as Cerebro-owned (satisfied by
// *execution.BracketTracker).
type Recorder interface {
	Record(sym domain.Symbol, resp domain.BracketResponse)
}

// ApplyAdjustment returns an ApplyFunc that, on confirmation, cancels the
// operator's externally-set protective orders, places a Cerebro bracket at the
// proposed SL/TP, and records it so the reconciler treats it as Cerebro-owned.
func ApplyAdjustment(b Bracketer, look ProtectiveLookup, rec Recorder) ApplyFunc {
	return func(ctx context.Context, p Proposal) error {
		if existing, ok := look.ProtectiveBracket(p.Symbol); ok {
			if err := b.CancelBracket(ctx, existing); err != nil {
				return fmt.Errorf("cancel existing protection for %s: %w", p.Symbol, err)
			}
		}
		req := domain.BracketRequest{
			Symbol:     p.Symbol,
			Venue:      p.Venue,
			Side:       p.Side,
			StopLoss:   p.ProposedStop,
			TakeProfit: p.ProposedTP,
			ClientTag:  "proposal_adjust",
		}
		resp, err := b.PlaceBracket(ctx, req)
		if err != nil {
			return fmt.Errorf("place adjusted bracket for %s: %w", p.Symbol, err)
		}
		rec.Record(p.Symbol, resp)
		return nil
	}
}

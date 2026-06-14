package paper

import (
	"sync"
	"time"

	"github.com/azhar/cerebro/internal/domain"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// PaperOrder tracks an open simulated order waiting for fill.
type PaperOrder struct {
	Intent    domain.OrderIntent
	Status    domain.OrderStatus
	CreatedAt time.Time
}

// PaperBracket tracks the simulated OCO-style protective pair attached to a
// position. When the trigger price is crossed by a subsequent candle, the
// Matcher closes the position at the trigger price and removes the bracket.
type PaperBracket struct {
	ListID            string
	StopOrderID       string
	TakeProfitOrderID string
	Symbol            domain.Symbol
	EntrySide         domain.Side // entry side; exit side is opposite
	Quantity          decimal.Decimal
	Stop              decimal.Decimal // 0 = no stop
	TakeProfit        decimal.Decimal // 0 = no take profit
	ParentIntentID    string
	CorrelationID     string
	Strategy          domain.StrategyName
}

// BracketTrigger is the outcome of evaluating a bracket against a candle:
// which leg fired and the price at which the position was closed.
type BracketTrigger struct {
	Bracket   PaperBracket
	Leg       string // "stop" | "take_profit"
	FillPrice decimal.Decimal
	FillTime  time.Time
	// EntryPrice is the entry price of the position being closed, captured
	// from the book before the position is deleted. Used by the Matcher to
	// compute realised PnL for the close. Zero if the position was missing.
	EntryPrice decimal.Decimal
}

// Book is an in-memory paper order book.
// It holds open orders, positions, and any protective brackets attached to
// those positions. Fills are simulated on candle data.
type Book struct {
	mu        sync.Mutex
	orders    map[string]*PaperOrder // keyed by Intent.ID
	positions map[domain.Symbol]*domain.Position
	brackets  map[domain.Symbol]*PaperBracket // one bracket per open position
}

// NewBook creates an empty paper order book.
func NewBook() *Book {
	return &Book{
		orders:    make(map[string]*PaperOrder),
		positions: make(map[domain.Symbol]*domain.Position),
		brackets:  make(map[domain.Symbol]*PaperBracket),
	}
}

// AddOrder places a new simulated order.
func (b *Book) AddOrder(intent domain.OrderIntent) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.orders[intent.ID] = &PaperOrder{
		Intent:    intent,
		Status:    domain.OrderStatusPending,
		CreatedAt: time.Now().UTC(),
	}
}

// OpenOrders returns all pending paper orders.
func (b *Book) OpenOrders() []PaperOrder {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]PaperOrder, 0, len(b.orders))
	for _, o := range b.orders {
		out = append(out, *o)
	}
	return out
}

// Positions returns all currently open paper positions.
func (b *Book) Positions() []domain.Position {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]domain.Position, 0, len(b.positions))
	for _, p := range b.positions {
		out = append(out, *p)
	}
	return out
}

// Fill marks an order as filled and creates/updates a position.
func (b *Book) Fill(orderID string, fillPrice decimal.Decimal, fillTime time.Time) domain.Trade {
	b.mu.Lock()
	defer b.mu.Unlock()

	order, ok := b.orders[orderID]
	if !ok {
		return domain.Trade{}
	}
	order.Status = domain.OrderStatusFilled
	delete(b.orders, orderID)

	intent := order.Intent
	existing, hasExisting := b.positions[intent.Symbol]

	// Reduce-only opposite-side fill reduces or flattens the existing position
	// rather than opening a reversed one. This matches futures reduce-only
	// semantics and is what reconciler/monitor closes rely on.
	if hasExisting && intent.ReduceOnly && intent.Side != existing.Side {
		remaining := existing.Quantity.Sub(intent.Quantity)
		if remaining.LessThanOrEqual(decimal.Zero) {
			delete(b.positions, intent.Symbol)
		} else {
			existing.Quantity = remaining
			existing.CurrentPrice = fillPrice
		}
		return domain.Trade{
			ID:            uuid.New().String(),
			IntentID:      intent.ID,
			CorrelationID: intent.CorrelationID,
			Symbol:        intent.Symbol,
			Side:          intent.Side,
			Quantity:      intent.Quantity,
			FillPrice:     fillPrice,
			Strategy:      intent.Strategy,
			Venue:         intent.Venue,
			CreatedAt:     fillTime,
		}
	}

	pos := &domain.Position{
		Symbol:        intent.Symbol,
		Venue:         intent.Venue,
		Side:          intent.Side,
		Quantity:      intent.Quantity,
		EntryPrice:    fillPrice,
		CurrentPrice:  fillPrice,
		StopLoss:      intent.StopLoss,
		TakeProfit1:   intent.TakeProfit1,
		Strategy:      intent.Strategy,
		CorrelationID: intent.CorrelationID,
		OpenedAt:      fillTime,
	}
	b.positions[intent.Symbol] = pos

	return domain.Trade{
		ID:            uuid.New().String(),
		IntentID:      intent.ID,
		CorrelationID: intent.CorrelationID,
		Symbol:        intent.Symbol,
		Side:          intent.Side,
		Quantity:      intent.Quantity,
		FillPrice:     fillPrice,
		Strategy:      intent.Strategy,
		Venue:         intent.Venue,
		CreatedAt:     fillTime,
	}
}

// UpdatePrice updates the current price of a position (for PnL display).
func (b *Book) UpdatePrice(symbol domain.Symbol, price decimal.Decimal) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if pos, ok := b.positions[symbol]; ok {
		pos.CurrentPrice = price
	}
}

// CancelExpired cancels orders older than maxAge.
func (b *Book) CancelExpired(maxAge time.Duration) []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	var cancelled []string
	for id, o := range b.orders {
		if time.Since(o.CreatedAt) > maxAge {
			o.Status = domain.OrderStatusCancelled
			cancelled = append(cancelled, id)
			delete(b.orders, id)
		}
	}
	return cancelled
}

// CancelPending cancels a single pending order by Intent.ID.
// Returns true if the order existed and was cancelled.
func (b *Book) CancelPending(intentID string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	o, ok := b.orders[intentID]
	if !ok {
		return false
	}
	o.Status = domain.OrderStatusCancelled
	delete(b.orders, intentID)
	return true
}

// AddBracket registers a protective bracket for the given symbol's position.
// Any pre-existing bracket on the symbol is overwritten — Cerebro's design
// is one active bracket per position.
func (b *Book) AddBracket(bk PaperBracket) {
	b.mu.Lock()
	defer b.mu.Unlock()
	copy := bk
	b.brackets[bk.Symbol] = &copy
}

// RemoveBracketByListID removes a bracket identified by its list/stop/tp id.
// Matches any of ListID, StopOrderID, TakeProfitOrderID. Returns the removed
// bracket and true when found.
func (b *Book) RemoveBracketByListID(id string) (PaperBracket, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for sym, bk := range b.brackets {
		if bk.ListID == id || bk.StopOrderID == id || bk.TakeProfitOrderID == id {
			out := *bk
			delete(b.brackets, sym)
			return out, true
		}
	}
	return PaperBracket{}, false
}

// Brackets returns a snapshot of all active brackets.
func (b *Book) Brackets() []PaperBracket {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]PaperBracket, 0, len(b.brackets))
	for _, bk := range b.brackets {
		out = append(out, *bk)
	}
	return out
}

// EvaluateBrackets walks every active bracket against the given candle and
// returns triggers for brackets whose stop or take-profit was crossed.
// Triggered brackets AND their underlying positions are removed from the
// book as part of this call, which makes the caller responsible only for
// recording the resulting Trade.
//
// Priority when both legs are crossed on the same candle: stop wins. That
// is the pessimistic assumption — realistic when candles are wide enough
// to straddle both trigger prices. Callers that need a more nuanced
// intra-bar model (e.g. TP-first on green candles) should add their own
// policy on top.
func (b *Book) EvaluateBrackets(c domain.Candle) []BracketTrigger {
	b.mu.Lock()
	defer b.mu.Unlock()

	var triggers []BracketTrigger
	for sym, bk := range b.brackets {
		if sym != c.Symbol {
			continue
		}
		leg, fill := evaluateBracketAgainstCandle(*bk, c)
		if leg == "" {
			continue
		}
		// Capture entry price before deleting the position so the caller can
		// compute realised PnL for the close.
		var entry decimal.Decimal
		if pos, ok := b.positions[sym]; ok {
			entry = pos.EntryPrice
		}
		triggers = append(triggers, BracketTrigger{
			Bracket:    *bk,
			Leg:        leg,
			FillPrice:  fill,
			FillTime:   c.CloseTime,
			EntryPrice: entry,
		})
		// Remove bracket and the position it protected.
		delete(b.brackets, sym)
		delete(b.positions, sym)
	}
	return triggers
}

// evaluateBracketAgainstCandle returns ("stop"|"take_profit", fill) when the
// candle crosses a trigger price, or ("", zero) when neither leg fired.
// Stop wins ties.
func evaluateBracketAgainstCandle(bk PaperBracket, c domain.Candle) (string, decimal.Decimal) {
	switch bk.EntrySide {
	case domain.SideBuy:
		// Long: stop fires when low <= stop; TP when high >= TP.
		if !bk.Stop.IsZero() && c.Low.LessThanOrEqual(bk.Stop) {
			return "stop", bk.Stop
		}
		if !bk.TakeProfit.IsZero() && c.High.GreaterThanOrEqual(bk.TakeProfit) {
			return "take_profit", bk.TakeProfit
		}
	case domain.SideSell:
		// Short: stop fires when high >= stop; TP when low <= TP.
		if !bk.Stop.IsZero() && c.High.GreaterThanOrEqual(bk.Stop) {
			return "stop", bk.Stop
		}
		if !bk.TakeProfit.IsZero() && c.Low.LessThanOrEqual(bk.TakeProfit) {
			return "take_profit", bk.TakeProfit
		}
	}
	return "", decimal.Zero
}

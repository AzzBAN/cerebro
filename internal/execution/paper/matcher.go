package paper

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/azhar/cerebro/internal/domain"
	"github.com/azhar/cerebro/internal/port"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// Matcher is the simulated paper execution engine.
// It implements port.Broker, routing all orders through the in-memory Book.
// Fills use a next-candle-open price model for entries; bracket legs fill at
// their trigger price on the candle whose high/low crosses the trigger.
type Matcher struct {
	book       *Book
	store      port.TradeStore
	commission decimal.Decimal // as a fraction, e.g. 0.0004 = 0.04%
	// pnl receives realised PnL each time a bracket exit closes a position.
	// Optional — nil in backtests and unit tests that don't exercise the
	// drawdown limits. Wired to the shared *risk.Gate in production via the
	// composition root so daily-loss / drawdown limits actually trip.
	pnl port.PnLReporter
}

// NewMatcher creates a paper Matcher.
func NewMatcher(book *Book, store port.TradeStore, commissionPct float64) *Matcher {
	return &Matcher{
		book:       book,
		store:      store,
		commission: decimal.NewFromFloat(commissionPct / 100),
	}
}

// SetPnLReporter wires the realised-PnL sink (the risk gate). Safe to call
// once at startup before the matcher begins processing candles; not safe to
// call concurrently with OnCandle.
func (m *Matcher) SetPnLReporter(r port.PnLReporter) {
	m.pnl = r
}

// Venue identifies this as a paper broker.
func (m *Matcher) Venue() domain.Venue { return domain.VenueBinanceSpot }

// Connect is a no-op for the paper matcher.
func (m *Matcher) Connect(_ context.Context) error { return nil }

// StreamQuotes is not supported on the paper matcher.
func (m *Matcher) StreamQuotes(_ context.Context, _ []domain.Symbol) (<-chan domain.Quote, error) {
	return nil, fmt.Errorf("paper matcher: use the market data hub for quotes")
}

// PlaceOrder adds the intent to the paper book; fill happens on the next candle open.
func (m *Matcher) PlaceOrder(_ context.Context, intent domain.OrderIntent) (string, error) {
	m.book.AddOrder(intent)
	slog.Info("paper order placed",
		"id", intent.ID,
		"symbol", intent.Symbol,
		"side", intent.Side,
		"order_type", intent.OrderTypeOrDefault(),
		"qty", intent.Quantity,
	)
	return "paper-" + intent.ID, nil
}

// PlaceBracket registers a simulated OCO bracket on the open position for
// the requested symbol. The bracket fires when a future candle's high/low
// crosses the stop or take-profit price — see Book.EvaluateBrackets.
//
// Returns synthetic order IDs prefixed "paper-br-" so they can be round-
// tripped through CancelBracket.
func (m *Matcher) PlaceBracket(_ context.Context, req domain.BracketRequest) (domain.BracketResponse, error) {
	if req.StopLoss.IsZero() {
		return domain.BracketResponse{}, fmt.Errorf("paper bracket: missing StopLoss")
	}
	listID := "paper-br-" + req.ParentIntentID
	stopID := "paper-brS-" + req.ParentIntentID
	tpID := ""
	if !req.TakeProfit.IsZero() {
		tpID = "paper-brT-" + req.ParentIntentID
	}

	m.book.AddBracket(PaperBracket{
		ListID:            listID,
		StopOrderID:       stopID,
		TakeProfitOrderID: tpID,
		Symbol:            req.Symbol,
		EntrySide:         req.Side,
		Quantity:          req.Quantity,
		Stop:              req.StopLoss,
		TakeProfit:        req.TakeProfit,
		ParentIntentID:    req.ParentIntentID,
		CorrelationID:     req.CorrelationID,
	})

	slog.Info("paper bracket placed",
		"symbol", req.Symbol,
		"parent", req.ParentIntentID,
		"stop", req.StopLoss.String(),
		"tp", req.TakeProfit.String(),
	)
	return domain.BracketResponse{
		ListID:            listID,
		StopOrderID:       stopID,
		TakeProfitOrderID: tpID,
		Symbol:            req.Symbol,
	}, nil
}

// CancelBracket removes a previously placed paper bracket.
func (m *Matcher) CancelBracket(_ context.Context, resp domain.BracketResponse) error {
	id := resp.ListID
	if id == "" {
		id = resp.StopOrderID
	}
	if id == "" {
		id = resp.TakeProfitOrderID
	}
	if id == "" {
		return fmt.Errorf("paper: CancelBracket with no identifiers")
	}
	if _, ok := m.book.RemoveBracketByListID(id); !ok {
		slog.Debug("paper: CancelBracket no-op (not found)", "id", id)
	}
	return nil
}

// CancelOrder removes a pending paper entry order. Bracket orders must be
// cancelled via CancelBracket.
func (m *Matcher) CancelOrder(_ context.Context, req domain.CancelRequest) error {
	id := req.BrokerOrderID
	if id == "" {
		id = req.ClientOrderID
	}
	// Strip "paper-" prefix if present.
	const paperPrefix = "paper-"
	if len(id) > len(paperPrefix) && id[:len(paperPrefix)] == paperPrefix {
		id = id[len(paperPrefix):]
	}
	if id == "" {
		return fmt.Errorf("paper: CancelOrder requires an identifier")
	}
	if !m.book.CancelPending(id) {
		slog.Debug("paper: CancelOrder no-op (not found)", "id", id)
	}
	return nil
}

// Positions returns current paper positions.
func (m *Matcher) Positions(_ context.Context) ([]domain.Position, error) {
	return m.book.Positions(), nil
}

// Balance returns the paper account balance.
func (m *Matcher) Balance(_ context.Context) (port.AccountBalance, error) {
	equity := decimal.NewFromFloat(10_000)
	return port.AccountBalance{
		Venue:      domain.VenueBinanceSpot,
		TotalUSDT:  equity,
		FreeUSDT:   equity,
		LockedUSDT: decimal.Zero,
	}, nil
}

// OnCandle drives fills for open paper orders and evaluates active brackets
// against the new candle. Call this from the strategy engine's candle loop.
//
// Order of operations:
//  1. Update last-seen price for PnL display.
//  2. Fill pending entry orders at the candle open (next-bar model, no
//     lookahead bias).
//  3. Evaluate brackets against the full candle range (high/low).
//     Triggered brackets close their positions at the trigger price.
func (m *Matcher) OnCandle(ctx context.Context, c domain.Candle) {
	m.book.UpdatePrice(c.Symbol, c.Close)

	// Fill pending entry orders.
	for _, order := range m.book.OpenOrders() {
		if order.Intent.Symbol != c.Symbol {
			continue
		}
		fillPrice := c.Open
		fees := order.Intent.Quantity.Mul(fillPrice).Mul(m.commission)

		trade := m.book.Fill(order.Intent.ID, fillPrice, c.OpenTime)
		if trade.ID == "" {
			continue
		}
		trade.Fees = fees

		if err := m.store.SaveTrade(ctx, trade); err != nil {
			slog.Error("paper: save entry trade failed", "error", err, "trade_id", trade.ID)
			continue
		}

		slog.Info("paper entry filled",
			"trade_id", trade.ID,
			"symbol", trade.Symbol, "side", trade.Side,
			"qty", trade.Quantity, "fill_price", fillPrice, "fees", fees,
		)
	}

	// Evaluate brackets for this symbol.
	for _, trig := range m.book.EvaluateBrackets(c) {
		m.recordBracketExit(ctx, trig)
	}
}

// recordBracketExit persists the simulated exit trade generated when a
// paper bracket triggered. Fees are charged symmetrically with entries.
func (m *Matcher) recordBracketExit(ctx context.Context, trig BracketTrigger) {
	exitSide := domain.SideSell
	if trig.Bracket.EntrySide == domain.SideSell {
		exitSide = domain.SideBuy
	}
	fees := trig.Bracket.Quantity.Mul(trig.FillPrice).Mul(m.commission)
	trade := domain.Trade{
		ID:            uuid.New().String(),
		IntentID:      trig.Bracket.ParentIntentID,
		CorrelationID: trig.Bracket.CorrelationID,
		Symbol:        trig.Bracket.Symbol,
		Side:          exitSide,
		Quantity:      trig.Bracket.Quantity,
		FillPrice:     trig.FillPrice,
		Fees:          fees,
		Strategy:      trig.Bracket.Strategy,
		Venue:         domain.VenueBinanceSpot,
		CreatedAt:     trig.FillTime,
		ClosedAt:      &trig.FillTime,
	}
	if err := m.store.SaveTrade(ctx, trade); err != nil {
		slog.Error("paper: save bracket exit failed", "error", err, "symbol", trig.Bracket.Symbol)
		return
	}

	// Feed realised PnL into the risk gate so drawdown / daily-loss limits
	// can trip. PnL is signed from the position holder's perspective:
	// for a long (entry side BUY) it is (exit - entry) * qty; for a short it
	// is (entry - exit) * qty. Commission on the exit leg is netted out.
	if m.pnl != nil && !trig.EntryPrice.IsZero() {
		var gross decimal.Decimal
		if trig.Bracket.EntrySide == domain.SideBuy {
			gross = trig.FillPrice.Sub(trig.EntryPrice).Mul(trig.Bracket.Quantity)
		} else {
			gross = trig.EntryPrice.Sub(trig.FillPrice).Mul(trig.Bracket.Quantity)
		}
		realized := gross.Sub(fees)
		m.pnl.UpdatePnL(realized)
		slog.Debug("paper: realised PnL reported to risk gate",
			"symbol", trig.Bracket.Symbol,
			"entry", trig.EntryPrice.String(),
			"exit", trig.FillPrice.String(),
			"realized", realized.String(),
		)
	}

	slog.Info("paper bracket fired",
		"symbol", trig.Bracket.Symbol,
		"leg", trig.Leg,
		"fill_price", trig.FillPrice.String(),
		"qty", trig.Bracket.Quantity.String(),
	)
}

// AutoFillAll immediately fills all pending orders at the current price.
// Useful for backtest determinism at end-of-data.
func (m *Matcher) AutoFillAll(ctx context.Context, prices map[domain.Symbol]decimal.Decimal) {
	for _, order := range m.book.OpenOrders() {
		price, ok := prices[order.Intent.Symbol]
		if !ok {
			continue
		}
		trade := m.book.Fill(order.Intent.ID, price, time.Now().UTC())
		if trade.ID == "" {
			continue
		}
		_ = m.store.SaveTrade(ctx, trade)
	}
}

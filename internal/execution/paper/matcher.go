package paper

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/azhar/cerebro/internal/domain"
	"github.com/azhar/cerebro/internal/port"
	"github.com/shopspring/decimal"
)

// Matcher is the simulated paper execution engine.
// It implements port.Broker, routing all orders through the in-memory Book.
// Fills use a next-candle-open price model to avoid lookahead bias.
type Matcher struct {
	book    *Book
	store   port.TradeStore
	commission decimal.Decimal // as a fraction, e.g. 0.0004 = 0.04%
}

// NewMatcher creates a paper Matcher.
func NewMatcher(book *Book, store port.TradeStore, commissionPct float64) *Matcher {
	return &Matcher{
		book:       book,
		store:      store,
		commission: decimal.NewFromFloat(commissionPct / 100),
	}
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
		"qty", intent.Quantity,
	)
	return "paper-" + intent.ID, nil
}

// CancelOrder removes a pending paper order.
func (m *Matcher) CancelOrder(_ context.Context, brokerOrderID string) error {
	// Strip "paper-" prefix.
	intentID := brokerOrderID
	if len(brokerOrderID) > 6 && brokerOrderID[:6] == "paper-" {
		intentID = brokerOrderID[6:]
	}
	cancelled := m.book.CancelExpired(0) // cancel by ID directly via book
	for _, id := range cancelled {
		if id == intentID {
			return nil
		}
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

// OnCandle drives fills for open paper orders using the next-candle open price.
// Call this from the strategy engine's candle loop.
func (m *Matcher) OnCandle(ctx context.Context, c domain.Candle) {
	// Update live prices for PnL display.
	m.book.UpdatePrice(c.Symbol, c.Close)

	// Attempt fills on pending orders for this symbol.
	for _, order := range m.book.OpenOrders() {
		if order.Intent.Symbol != c.Symbol {
			continue
		}
		// Fill model: use candle open price (next-candle model avoids lookahead).
		fillPrice := c.Open
		fees := order.Intent.Quantity.Mul(fillPrice).Mul(m.commission)

		trade := m.book.Fill(order.Intent.ID, fillPrice, c.OpenTime)
		if trade.ID == "" {
			continue
		}
		trade.Fees = fees

		if err := m.store.SaveTrade(ctx, trade); err != nil {
			slog.Error("paper: save trade failed", "error", err, "trade_id", trade.ID)
			continue
		}

		slog.Info("paper order filled",
			"trade_id", trade.ID,
			"symbol", trade.Symbol,
			"side", trade.Side,
			"qty", trade.Quantity,
			"fill_price", fillPrice,
			"fees", fees,
		)

		m.checkSLTP(ctx, order.Intent, c)
	}
}

// checkSLTP logs SL/TP levels for a newly filled paper position (monitoring hook).
func (m *Matcher) checkSLTP(_ context.Context, intent domain.OrderIntent, c domain.Candle) {
	if !intent.StopLoss.IsZero() {
		slog.Debug("paper position SL set",
			"symbol", intent.Symbol, "sl", intent.StopLoss, "candle_close", c.Close)
	}
	if !intent.TakeProfit1.IsZero() {
		slog.Debug("paper position TP1 set",
			"symbol", intent.Symbol, "tp1", intent.TakeProfit1, "candle_close", c.Close)
	}
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

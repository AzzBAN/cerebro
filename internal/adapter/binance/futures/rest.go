package futures

import (
	"context"
	"fmt"
	"strconv"
	"time"

	gobinancefutures "github.com/adshao/go-binance/v2/futures"
	"github.com/azhar/cerebro/internal/domain"
	"github.com/shopspring/decimal"
)

// FuturesBroker implements port.Broker for Binance USDT-M Futures REST API.
type FuturesBroker struct {
	client *gobinancefutures.Client
}

// NewFuturesBroker creates a FuturesBroker.
func NewFuturesBroker(client *gobinancefutures.Client) *FuturesBroker {
	return &FuturesBroker{client: client}
}

// Venue identifies this broker endpoint.
func (b *FuturesBroker) Venue() domain.Venue { return domain.VenueBinanceFutures }

// Connect is a no-op for the REST broker.
func (b *FuturesBroker) Connect(_ context.Context) error { return nil }

// StreamQuotes is not supported on REST; use the futures KlinesWS.
func (b *FuturesBroker) StreamQuotes(_ context.Context, _ []domain.Symbol) (<-chan domain.Quote, error) {
	return nil, fmt.Errorf("futures broker: use KlinesWS for market data")
}

// PlaceOrder submits a futures order.
func (b *FuturesBroker) PlaceOrder(ctx context.Context, intent domain.OrderIntent) (string, error) {
	side := gobinancefutures.SideTypeBuy
	if intent.Side == domain.SideSell {
		side = gobinancefutures.SideTypeSell
	}

	resp, err := b.client.NewCreateOrderService().
		Symbol(string(intent.Symbol)).
		Side(side).
		Type(gobinancefutures.OrderTypeMarket).
		NewClientOrderID(intent.ID).
		Quantity(intent.Quantity.String()).
		Do(ctx)
	if err != nil {
		return "", fmt.Errorf("binance futures place order: %w", err)
	}
	return strconv.FormatInt(resp.OrderID, 10), nil
}

// CancelOrder cancels a pending futures order.
func (b *FuturesBroker) CancelOrder(ctx context.Context, brokerOrderID string) error {
	orderID, err := strconv.ParseInt(brokerOrderID, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid broker order ID: %w", err)
	}
	// Futures cancel requires symbol — not stored here. Phase 4+ enriches this.
	_ = orderID
	return fmt.Errorf("cancel order not fully implemented; symbol required")
}

// Positions returns all open futures positions.
func (b *FuturesBroker) Positions(ctx context.Context) ([]domain.Position, error) {
	account, err := b.client.NewGetAccountService().Do(ctx)
	if err != nil {
		return nil, fmt.Errorf("binance futures get account: %w", err)
	}

	var positions []domain.Position
	for _, p := range account.Positions {
		qty, _ := decimal.NewFromString(p.PositionAmt)
		if qty.IsZero() {
			continue
		}
		entry, _ := decimal.NewFromString(p.EntryPrice)
		unrealised, _ := decimal.NewFromString(p.UnrealizedProfit)
		_ = unrealised

		side := domain.SideBuy
		if qty.IsNegative() {
			side = domain.SideSell
			qty = qty.Abs()
		}

		positions = append(positions, domain.Position{
			Symbol:     domain.Symbol(p.Symbol),
			Venue:      domain.VenueBinanceFutures,
			Side:       side,
			Quantity:   qty,
			EntryPrice: entry,
			OpenedAt:   time.Now().UTC(),
		})
	}
	return positions, nil
}

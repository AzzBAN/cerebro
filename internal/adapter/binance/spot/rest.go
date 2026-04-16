package spot

import (
	"context"
	"fmt"
	"strconv"
	"time"

	gobinance "github.com/adshao/go-binance/v2"
	"github.com/azhar/cerebro/internal/domain"
	"github.com/shopspring/decimal"
)

// SpotBroker implements port.Broker for Binance Spot REST calls.
type SpotBroker struct {
	client *gobinance.Client
}

// NewSpotBroker creates a SpotBroker wrapping the provided client.
func NewSpotBroker(client *gobinance.Client) *SpotBroker {
	return &SpotBroker{client: client}
}

// Venue identifies this broker.
func (b *SpotBroker) Venue() domain.Venue { return domain.VenueBinanceSpot }

// Connect is a no-op for REST; WebSocket connections are managed by KlinesWS.
func (b *SpotBroker) Connect(_ context.Context) error { return nil }

// StreamQuotes is not implemented on the REST broker; use KlinesWS.
func (b *SpotBroker) StreamQuotes(_ context.Context, _ []domain.Symbol) (<-chan domain.Quote, error) {
	return nil, fmt.Errorf("StreamQuotes: use KlinesWS for market data")
}

// PlaceOrder submits a new order to Binance Spot REST API.
func (b *SpotBroker) PlaceOrder(ctx context.Context, intent domain.OrderIntent) (string, error) {
	side := gobinance.SideTypeBuy
	if intent.Side == domain.SideSell {
		side = gobinance.SideTypeSell
	}

	var orderType gobinance.OrderType
	switch intent.Strategy {
	default:
		orderType = gobinance.OrderTypeMarket
	}

	svc := b.client.NewCreateOrderService().
		Symbol(string(intent.Symbol)).
		Side(side).
		Type(orderType).
		NewClientOrderID(intent.ID). // idempotency key
		Quantity(intent.Quantity.String())

	resp, err := svc.Do(ctx)
	if err != nil {
		return "", fmt.Errorf("binance spot place order: %w", err)
	}

	return strconv.FormatInt(resp.OrderID, 10), nil
}

// CancelOrder cancels a pending Binance Spot order by broker order ID.
func (b *SpotBroker) CancelOrder(ctx context.Context, brokerOrderID string) error {
	orderID, err := strconv.ParseInt(brokerOrderID, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid broker order ID %q: %w", brokerOrderID, err)
	}

	_, err = b.client.NewCancelOrderService().
		Symbol(""). // Symbol must be provided; caller must pass it separately in V2.
		OrderID(orderID).
		Do(ctx)
	return err
}

// Positions returns all currently open Spot positions.
// Note: Binance Spot doesn't have "positions" natively — we approximate via
// open orders and account balances. Phase 4 watchdog uses this for reconciliation.
func (b *SpotBroker) Positions(ctx context.Context) ([]domain.Position, error) {
	account, err := b.client.NewGetAccountService().Do(ctx)
	if err != nil {
		return nil, fmt.Errorf("binance spot get account: %w", err)
	}

	var positions []domain.Position
	for _, bal := range account.Balances {
		free, _ := decimal.NewFromString(bal.Free)
		locked, _ := decimal.NewFromString(bal.Locked)
		total := free.Add(locked)
		if total.IsZero() {
			continue
		}
		// Report non-zero balances as approximate positions.
		// Phase 4 reconciliation will enrich these with PnL data.
		positions = append(positions, domain.Position{
			Symbol:   domain.Symbol(bal.Asset + "USDT"),
			Venue:    domain.VenueBinanceSpot,
			Side:     domain.SideBuy,
			Quantity: total,
			OpenedAt: time.Now().UTC(),
		})
	}
	return positions, nil
}

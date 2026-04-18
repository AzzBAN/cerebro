package port

import (
	"context"

	"github.com/azhar/cerebro/internal/domain"
	"github.com/shopspring/decimal"
)

// AccountBalance holds balance information for a single venue.
type AccountBalance struct {
	Venue      domain.Venue
	TotalUSDT  decimal.Decimal
	FreeUSDT   decimal.Decimal
	LockedUSDT decimal.Decimal
	Assets     []AssetBalance
}

// AssetBalance holds balance for a non-USDT asset.
type AssetBalance struct {
	Asset  string
	Free   decimal.Decimal
	Locked decimal.Decimal
}

// Broker is the abstraction over a single exchange venue.
// One implementation exists per venue (Binance Spot, Binance Futures).
type Broker interface {
	// Connect opens the WebSocket feed for this venue.
	Connect(ctx context.Context) error

	// StreamQuotes returns a read-only channel of normalised Quote events.
	StreamQuotes(ctx context.Context, symbols []domain.Symbol) (<-chan domain.Quote, error)

	// PlaceOrder submits a single order; returns the broker-assigned order ID.
	PlaceOrder(ctx context.Context, intent domain.OrderIntent) (string, error)

	// Positions returns all currently open positions on this venue.
	Positions(ctx context.Context) ([]domain.Position, error)

	// Balance returns current account balance for this venue.
	Balance(ctx context.Context) (AccountBalance, error)

	// CancelOrder cancels a pending order by broker order ID.
	CancelOrder(ctx context.Context, brokerOrderID string) error

	// Venue identifies this broker endpoint.
	Venue() domain.Venue
}

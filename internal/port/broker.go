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
// One implementation exists per venue (Binance Spot, Binance Futures, paper).
//
// Ordering invariants:
//   - PlaceOrder is called by at most one goroutine per venue (enforced by
//     the execution Router/Worker pair).
//   - PlaceBracket is called after PlaceOrder's fill has been confirmed.
//     Paper implementations may short-circuit this: they know the entry
//     filled on the next candle open.
//   - CancelOrder and CancelBracket may be called from any goroutine and
//     must therefore be safe for concurrent use with themselves.
type Broker interface {
	// Connect opens the WebSocket feed for this venue.
	Connect(ctx context.Context) error

	// StreamQuotes returns a read-only channel of normalised Quote events.
	StreamQuotes(ctx context.Context, symbols []domain.Symbol) (<-chan domain.Quote, error)

	// PlaceOrder submits the entry order described by intent and returns the
	// broker-assigned order ID. It does NOT attach protective SL/TP; the
	// caller invokes PlaceBracket once the entry has filled.
	PlaceOrder(ctx context.Context, intent domain.OrderIntent) (string, error)

	// PlaceBracket attaches a protective stop-loss and take-profit pair to an
	// already-open position. For Binance spot this creates an OCO order; for
	// Binance futures it creates two reduce-only algo orders (STOP_MARKET +
	// TAKE_PROFIT_MARKET) anchored on the MARK price.
	//
	// Returns a zero BracketResponse and a non-nil error when neither leg
	// could be placed. A bracket with only one leg (e.g. TP but no SL) is
	// rejected — Cerebro never runs a position with no protective stop.
	PlaceBracket(ctx context.Context, req domain.BracketRequest) (domain.BracketResponse, error)

	// CancelOrder cancels a single pending order. Symbol is required on
	// Binance for every cancel call — passing an empty symbol is a caller
	// error and the adapter will return an error without hitting the API.
	CancelOrder(ctx context.Context, req domain.CancelRequest) error

	// CancelBracket cancels both legs of a previously-placed bracket.
	// Implementations should tolerate per-leg failure (e.g. leg already
	// filled) and best-effort cancel the other side.
	CancelBracket(ctx context.Context, resp domain.BracketResponse) error

	// Positions returns all currently open positions on this venue.
	Positions(ctx context.Context) ([]domain.Position, error)

	// Balance returns current account balance for this venue.
	Balance(ctx context.Context) (AccountBalance, error)

	// Venue identifies this broker endpoint.
	Venue() domain.Venue
}

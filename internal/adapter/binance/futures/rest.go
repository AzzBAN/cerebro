package futures

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	gobinancefutures "github.com/adshao/go-binance/v2/futures"
	"github.com/azhar/cerebro/internal/domain"
	"github.com/azhar/cerebro/internal/port"
	"github.com/gorilla/websocket"
	"github.com/shopspring/decimal"
)

type futuresMode string

const userDataKeepaliveInterval = 25 * time.Minute

const (
	futuresModeMainnet futuresMode = "mainnet"
	futuresModeTestnet futuresMode = "testnet"
	futuresModeDemo    futuresMode = "demo"
)

// FuturesBroker implements port.Broker for Binance USDT-M Futures REST API.
type FuturesBroker struct {
	client *gobinancefutures.Client
	mode   futuresMode

	mu        sync.RWMutex
	positions map[domain.Symbol]domain.Position
}

// NewFuturesBroker creates a FuturesBroker.
func NewFuturesBroker(client *gobinancefutures.Client, mode string) *FuturesBroker {
	return &FuturesBroker{
		client:    client,
		mode:      futuresMode(mode),
		positions: make(map[domain.Symbol]domain.Position),
	}
}

// Venue identifies this broker endpoint.
func (b *FuturesBroker) Venue() domain.Venue { return domain.VenueBinanceFutures }

// Connect bootstraps positions once, then keeps them fresh via the private user-data websocket.
func (b *FuturesBroker) Connect(ctx context.Context) error {
	if err := b.bootstrapPositions(ctx); err != nil {
		return err
	}
	go b.runUserDataStream(ctx)
	return nil
}

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
		Symbol(domain.ToExchangeSymbol(intent.Symbol)).
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
	_ = orderID
	return fmt.Errorf("cancel order not fully implemented; symbol required")
}

// Positions returns the locally cached futures position view.
func (b *FuturesBroker) Positions(_ context.Context) ([]domain.Position, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	out := make([]domain.Position, 0, len(b.positions))
	for _, p := range b.positions {
		out = append(out, p)
	}
	return out, nil
}

// Balance returns the current futures wallet balance.
func (b *FuturesBroker) Balance(ctx context.Context) (port.AccountBalance, error) {
	account, err := b.client.NewGetAccountService().Do(ctx)
	if err != nil {
		return port.AccountBalance{}, fmt.Errorf("binance futures get account: %w", err)
	}

	total, _ := decimal.NewFromString(account.TotalWalletBalance)
	free, _ := decimal.NewFromString(account.AvailableBalance)
	locked := total.Sub(free)
	if locked.IsNegative() {
		locked = decimal.Zero
	}

	return port.AccountBalance{
		Venue:      domain.VenueBinanceFutures,
		TotalUSDT:  total,
		FreeUSDT:   free,
		LockedUSDT: locked,
	}, nil
}

func (b *FuturesBroker) bootstrapPositions(ctx context.Context) error {
	account, err := b.client.NewGetAccountService().Do(ctx)
	if err != nil {
		return fmt.Errorf("binance futures get account: %w", err)
	}

	next := make(map[domain.Symbol]domain.Position)
	for _, p := range account.Positions {
		pos, ok, err := futuresAccountPositionToDomain(p.Symbol, p.PositionAmt, p.EntryPrice, p.UnrealizedProfit)
		if err != nil {
			return err
		}
		if ok {
			next[pos.Symbol] = pos
		}
	}

	b.mu.Lock()
	b.positions = next
	b.mu.Unlock()
	return nil
}

func (b *FuturesBroker) runUserDataStream(ctx context.Context) {
	backoff := time.Second
	for {
		if ctx.Err() != nil {
			return
		}
		if err := b.runUserDataSession(ctx); err != nil && ctx.Err() == nil {
			slog.Warn("futures user data WS disconnected", "error", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			if backoff < 30*time.Second {
				backoff *= 2
			}
			continue
		}
		return
	}
}

func (b *FuturesBroker) runUserDataSession(ctx context.Context) error {
	listenKey, err := b.client.NewStartUserStreamService().Do(ctx)
	if err != nil {
		return fmt.Errorf("start futures user stream: %w", err)
	}

	endpoint := fmt.Sprintf("%s/%s", b.userDataEndpoint(), listenKey)
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, endpoint, nil)
	if err != nil {
		return fmt.Errorf("dial futures user data stream: %w", err)
	}
	defer conn.Close()

	keepaliveDone := make(chan struct{})
	go b.keepaliveUserStream(ctx, listenKey, keepaliveDone)
	defer close(keepaliveDone)

	go func() {
		<-ctx.Done()
		_ = conn.Close()
	}()

	for {
		_, message, err := conn.ReadMessage()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		if err := b.handleUserDataMessage(message); err != nil {
			slog.Warn("futures user data message ignored", "error", err)
		}
	}
}

func (b *FuturesBroker) keepaliveUserStream(ctx context.Context, listenKey string, done <-chan struct{}) {
	ticker := time.NewTicker(userDataKeepaliveInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-done:
			return
		case <-ticker.C:
			if err := b.client.NewKeepaliveUserStreamService().ListenKey(listenKey).Do(ctx); err != nil {
				slog.Warn("futures user data keepalive failed", "error", err)
			}
		}
	}
}

func (b *FuturesBroker) handleUserDataMessage(message []byte) error {
	var envelope struct {
		Event string `json:"e"`
	}
	if err := json.Unmarshal(message, &envelope); err != nil {
		return err
	}
	if envelope.Event != "ACCOUNT_UPDATE" {
		return nil
	}

	var evt struct {
		Account struct {
			Positions []struct {
				Symbol     string `json:"s"`
				Amount     string `json:"pa"`
				EntryPrice string `json:"ep"`
				MarkPrice  string `json:"mp"`
			} `json:"P"`
		} `json:"a"`
	}
	if err := json.Unmarshal(message, &evt); err != nil {
		return err
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	for _, p := range evt.Account.Positions {
		pos, ok, err := futuresAccountPositionToDomain(p.Symbol, p.Amount, p.EntryPrice, p.MarkPrice)
		if err != nil {
			return err
		}
		if !ok {
			delete(b.positions, domain.Symbol(strings.TrimSpace(p.Symbol)))
			if sym, normErr := domain.NormalizeExchangeSymbol(p.Symbol, domain.ContractFuturesPerp); normErr == nil {
				delete(b.positions, sym)
			}
			continue
		}
		if existing, ok := b.positions[pos.Symbol]; ok {
			pos.OpenedAt = existing.OpenedAt
			pos.StopLoss = existing.StopLoss
			pos.TakeProfit1 = existing.TakeProfit1
			pos.Strategy = existing.Strategy
			pos.CorrelationID = existing.CorrelationID
		}
		b.positions[pos.Symbol] = pos
	}
	return nil
}

func futuresAccountPositionToDomain(rawSymbol, amountStr, entryStr, currentStr string) (domain.Position, bool, error) {
	qty, _ := decimal.NewFromString(amountStr)
	if qty.IsZero() {
		return domain.Position{}, false, nil
	}

	entry, _ := decimal.NewFromString(entryStr)
	current, _ := decimal.NewFromString(currentStr)
	side := domain.SideBuy
	if qty.IsNegative() {
		side = domain.SideSell
		qty = qty.Abs()
	}

	sym, err := domain.NormalizeExchangeSymbol(rawSymbol, domain.ContractFuturesPerp)
	if err != nil {
		return domain.Position{}, false, fmt.Errorf("normalize futures symbol %q: %w", rawSymbol, err)
	}

	return domain.Position{
		Symbol:       sym,
		Venue:        domain.VenueBinanceFutures,
		Side:         side,
		Quantity:     qty,
		EntryPrice:   entry,
		CurrentPrice: current,
		OpenedAt:     time.Now().UTC(),
	}, true, nil
}

// FetchKlines fetches historical closed candles from Binance USDT-M Futures REST API.
// Returns candles in chronological order (oldest first).
// No API key is required — kline endpoints are public.
func FetchKlines(ctx context.Context, client *gobinancefutures.Client, symbol domain.Symbol, tf domain.Timeframe, limit int) ([]domain.Candle, error) {
	raw, err := client.NewKlinesService().
		Symbol(domain.ToExchangeSymbol(symbol)).
		Interval(string(tf)).
		Limit(limit).
		Do(ctx)
	if err != nil {
		return nil, fmt.Errorf("binance futures klines %s %s: %w", symbol, tf, err)
	}

	out := make([]domain.Candle, 0, len(raw))
	for _, k := range raw {
		parse := func(s string) (decimal.Decimal, error) {
			d, err := decimal.NewFromString(s)
			if err != nil {
				return decimal.Zero, fmt.Errorf("parse %q: %w", s, err)
			}
			return d, nil
		}
		open, err := parse(k.Open)
		if err != nil {
			return nil, fmt.Errorf("kline open: %w", err)
		}
		high, err := parse(k.High)
		if err != nil {
			return nil, fmt.Errorf("kline high: %w", err)
		}
		low, err := parse(k.Low)
		if err != nil {
			return nil, fmt.Errorf("kline low: %w", err)
		}
		close_, err := parse(k.Close)
		if err != nil {
			return nil, fmt.Errorf("kline close: %w", err)
		}
		vol, err := parse(k.Volume)
		if err != nil {
			return nil, fmt.Errorf("kline volume: %w", err)
		}

		out = append(out, domain.Candle{
			Symbol:    symbol,
			Timeframe: tf,
			OpenTime:  time.Unix(k.OpenTime/1000, 0).UTC(),
			CloseTime: time.Unix(k.CloseTime/1000, 0).UTC(),
			Open:      open,
			High:      high,
			Low:       low,
			Close:     close_,
			Volume:    vol,
			Closed:    true,
		})
	}

	return out, nil
}

func (b *FuturesBroker) userDataEndpoint() string {
	switch b.mode {
	case futuresModeDemo:
		return gobinancefutures.BaseWsPrivateDemoURL
	case futuresModeTestnet:
		return gobinancefutures.BaseWsPrivateTestnetUrl
	default:
		return gobinancefutures.BaseWsPrivateMainUrl
	}
}

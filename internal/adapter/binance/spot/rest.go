package spot

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	gobinance "github.com/adshao/go-binance/v2"
	commonws "github.com/adshao/go-binance/v2/common/websocket"
	"github.com/azhar/cerebro/internal/domain"
	"github.com/azhar/cerebro/internal/port"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/shopspring/decimal"
)

type spotMode string

const (
	spotModeMainnet spotMode = "mainnet"
	spotModeTestnet spotMode = "testnet"
	spotModeDemo    spotMode = "demo"
)

type spotBalance struct {
	free   decimal.Decimal
	locked decimal.Decimal
}

// knownStablecoins are assets that represent fiat-pegged value, not tradeable
// base assets against USDT. These must not appear as spot positions.
var knownStablecoins = map[string]bool{
	"USDC": true, "BUSD": true, "FDUSD": true, "TUSD": true,
	"DAI": true, "USDP": true, "PYUSD": true, "USDS": true,
}

// SpotBroker implements port.Broker for Binance Spot REST calls.
type SpotBroker struct {
	client   *gobinance.Client
	mode     spotMode
	symbols  map[domain.Symbol]bool // allowed trading pairs from config

	mu        sync.RWMutex
	balances  map[string]spotBalance
	positions map[domain.Symbol]domain.Position
}

// NewSpotBroker creates a SpotBroker wrapping the provided client.
// symbols is the set of canonical symbols configured for spot trading;
// only balances matching these symbols are promoted to positions.
func NewSpotBroker(client *gobinance.Client, mode string, symbols []domain.Symbol) *SpotBroker {
	symSet := make(map[domain.Symbol]bool, len(symbols))
	for _, s := range symbols {
		symSet[s] = true
	}
	return &SpotBroker{
		client:    client,
		mode:      spotMode(mode),
		symbols:   symSet,
		balances:  make(map[string]spotBalance),
		positions: make(map[domain.Symbol]domain.Position),
	}
}

// Venue identifies this broker.
func (b *SpotBroker) Venue() domain.Venue { return domain.VenueBinanceSpot }

// Connect bootstraps positions once, then maintains a local position cache from
// the private user-data websocket stream.
func (b *SpotBroker) Connect(ctx context.Context) error {
	if err := b.bootstrapPositions(ctx); err != nil {
		return err
	}
	go b.runUserDataStream(ctx)
	return nil
}

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
		Symbol(domain.ToExchangeSymbol(intent.Symbol)).
		Side(side).
		Type(orderType).
		NewClientOrderID(intent.ID).
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
		Symbol("").
		OrderID(orderID).
		Do(ctx)
	return err
}

// Positions returns the locally cached position view maintained by the user-data stream.
func (b *SpotBroker) Positions(_ context.Context) ([]domain.Position, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	out := make([]domain.Position, 0, len(b.positions))
	for _, p := range b.positions {
		out = append(out, p)
	}
	return out, nil
}

// Balance returns the current spot account balance from the cached user-data stream.
func (b *SpotBroker) Balance(_ context.Context) (port.AccountBalance, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	usdt, hasUSDT := b.balances["USDT"]
	var total, free, locked decimal.Decimal
	if hasUSDT {
		total = usdt.free.Add(usdt.locked)
		free = usdt.free
		locked = usdt.locked
	}

	var assets []port.AssetBalance
	for asset, bal := range b.balances {
		if asset == "USDT" || knownStablecoins[asset] {
			continue
		}
		totalQty := bal.free.Add(bal.locked)
		if totalQty.IsZero() {
			continue
		}
		assets = append(assets, port.AssetBalance{
			Asset:  asset,
			Free:   bal.free,
			Locked: bal.locked,
		})
	}

	return port.AccountBalance{
		Venue:      domain.VenueBinanceSpot,
		TotalUSDT:  total,
		FreeUSDT:   free,
		LockedUSDT: locked,
		Assets:     assets,
	}, nil
}

func (b *SpotBroker) bootstrapPositions(ctx context.Context) error {
	account, err := b.client.NewGetAccountService().Do(ctx)
	if err != nil {
		return fmt.Errorf("binance spot get account: %w", err)
	}

	b.mu.Lock()
	for _, bal := range account.Balances {
		free, _ := decimal.NewFromString(bal.Free)
		locked, _ := decimal.NewFromString(bal.Locked)
		b.balances[strings.ToUpper(bal.Asset)] = spotBalance{free: free, locked: locked}
	}
	b.rebuildPositionsLocked()
	b.mu.Unlock()

	// Fetch current market prices for each discovered position so that
	// CurrentPrice and EntryPrice are not zero on startup.
	b.fetchCurrentPrices(ctx)

	return nil
}

// fetchCurrentPrices queries the Binance ticker API for each open position and
// populates CurrentPrice (and EntryPrice when unset).
func (b *SpotBroker) fetchCurrentPrices(ctx context.Context) {
	b.mu.RLock()
	symbols := make([]domain.Symbol, 0, len(b.positions))
	for sym := range b.positions {
		symbols = append(symbols, sym)
	}
	b.mu.RUnlock()

	for _, sym := range symbols {
		exchangeSym := domain.ToExchangeSymbol(sym)
		prices, err := b.client.NewListPricesService().Symbol(exchangeSym).Do(ctx)
		if err != nil {
			slog.Warn("spot bootstrap: fetch price failed", "symbol", sym, "error", err)
			continue
		}
		if len(prices) == 0 {
			continue
		}
		price, err := decimal.NewFromString(prices[0].Price)
		if err != nil {
			continue
		}

		b.mu.Lock()
		if pos, ok := b.positions[sym]; ok {
			pos.CurrentPrice = price
			if pos.EntryPrice.IsZero() {
				pos.EntryPrice = price
			}
			b.positions[sym] = pos
		}
		b.mu.Unlock()
	}
}

func (b *SpotBroker) runUserDataStream(ctx context.Context) {
	backoff := time.Second
	for {
		if ctx.Err() != nil {
			return
		}
		if err := b.runUserDataSession(ctx); err != nil && ctx.Err() == nil {
			slog.Warn("spot user data WS disconnected", "error", err)
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

func (b *SpotBroker) runUserDataSession(ctx context.Context) error {
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, b.userDataAPIEndpoint(), nil)
	if err != nil {
		return fmt.Errorf("dial user data stream: %w", err)
	}
	defer conn.Close()

	reqData := commonws.NewRequestData(
		uuid.New().String(),
		b.client.APIKey,
		b.client.SecretKey,
		b.client.TimeOffset,
		b.client.KeyType,
	)
	subscribeRequest, err := commonws.CreateRequest(
		reqData,
		commonws.UserDataStreamSubscribeSignatureSpotWsApiMethod,
		map[string]any{},
	)
	if err != nil {
		return fmt.Errorf("create signed user data subscription: %w", err)
	}
	if err := conn.WriteMessage(websocket.TextMessage, subscribeRequest); err != nil {
		return fmt.Errorf("subscribe user data stream: %w", err)
	}

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
			slog.Warn("spot user data message ignored", "error", err)
		}
	}
}

func (b *SpotBroker) handleUserDataMessage(message []byte) error {
	var ack struct {
		ID     string `json:"id"`
		Status int    `json:"status"`
	}
	if err := json.Unmarshal(message, &ack); err == nil && ack.ID != "" && ack.Status == 200 {
		return nil
	}

	var envelope struct {
		Event     json.RawMessage `json:"e"`
		EventData json.RawMessage `json:"event"`
		Data      json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(message, &envelope); err != nil {
		return err
	}

	event := ""
	if len(envelope.Event) > 0 {
		if err := json.Unmarshal(envelope.Event, &event); err != nil {
			// `e` is not a string (e.g. numeric); ignore this message.
			return nil
		}
	}
	if len(envelope.EventData) > 0 {
		message = envelope.EventData
		if err := json.Unmarshal(message, &envelope); err != nil {
			return err
		}
	} else if len(envelope.Data) > 0 && event == "" {
		message = envelope.Data
		if err := json.Unmarshal(message, &envelope); err != nil {
			return err
		}
	}

	switch event {
	case "outboundAccountPosition":
		var evt struct {
			Balances []struct {
				Asset  string `json:"a"`
				Free   string `json:"f"`
				Locked string `json:"l"`
			} `json:"B"`
		}
		if err := json.Unmarshal(message, &evt); err != nil {
			return err
		}

		b.mu.Lock()
		defer b.mu.Unlock()
		for _, bal := range evt.Balances {
			free, _ := decimal.NewFromString(bal.Free)
			locked, _ := decimal.NewFromString(bal.Locked)
			b.balances[strings.ToUpper(bal.Asset)] = spotBalance{free: free, locked: locked}
		}
		b.rebuildPositionsLocked()
	}

	return nil
}

func (b *SpotBroker) rebuildPositionsLocked() {
	next := make(map[domain.Symbol]domain.Position)
	now := time.Now().UTC()
	for asset, bal := range b.balances {
		if asset == "USDT" || knownStablecoins[asset] {
			continue
		}
		total := bal.free.Add(bal.locked)
		if total.IsZero() {
			continue
		}
		sym := domain.Symbol(asset + "/USDT")
		// Only promote to position if this symbol is in the configured trading universe.
		if !b.symbols[sym] {
			continue
		}
		pos := domain.Position{
			Symbol:   sym,
			Venue:    domain.VenueBinanceSpot,
			Side:     domain.SideBuy,
			Quantity: total,
			OpenedAt: now,
		}
		if existing, ok := b.positions[sym]; ok {
			pos.OpenedAt = existing.OpenedAt
			pos.EntryPrice = existing.EntryPrice
			pos.CurrentPrice = existing.CurrentPrice
			pos.StopLoss = existing.StopLoss
			pos.TakeProfit1 = existing.TakeProfit1
			pos.Strategy = existing.Strategy
			pos.CorrelationID = existing.CorrelationID
		}
		next[sym] = pos
	}
	b.positions = next
}

// FetchKlines fetches historical closed candles from Binance Spot REST API.
// Returns candles in chronological order (oldest first).
// No API key is required — kline endpoints are public.
func FetchKlines(ctx context.Context, client *gobinance.Client, symbol domain.Symbol, tf domain.Timeframe, limit int) ([]domain.Candle, error) {
	raw, err := client.NewKlinesService().
		Symbol(domain.ToExchangeSymbol(symbol)).
		Interval(string(tf)).
		Limit(limit).
		Do(ctx)
	if err != nil {
		return nil, fmt.Errorf("binance spot klines %s %s: %w", symbol, tf, err)
	}

	out := make([]domain.Candle, 0, len(raw))
	for _, k := range raw {
		open, err := parseDecimal(k.Open)
		if err != nil {
			return nil, fmt.Errorf("kline open: %w", err)
		}
		high, err := parseDecimal(k.High)
		if err != nil {
			return nil, fmt.Errorf("kline high: %w", err)
		}
		low, err := parseDecimal(k.Low)
		if err != nil {
			return nil, fmt.Errorf("kline low: %w", err)
		}
		close_, err := parseDecimal(k.Close)
		if err != nil {
			return nil, fmt.Errorf("kline close: %w", err)
		}
		vol, err := parseDecimal(k.Volume)
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

func (b *SpotBroker) userDataAPIEndpoint() string {
	switch b.mode {
	case spotModeDemo:
		return gobinance.BaseWsApiDemoURL
	case spotModeTestnet:
		return gobinance.BaseWsApiTestnetURL
	default:
		return gobinance.BaseWsApiMainURL
	}
}

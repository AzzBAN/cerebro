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

// defaultPositionResyncInterval is how often the broker re-fetches the
// authoritative balance snapshot over REST to recover any open/close the
// user-data WS stream may have missed (dropped events, silent socket stalls).
// Overridable per-deployment via engine.position_resync_interval_ms.
const defaultPositionResyncInterval = 5 * time.Second

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

// spotProtectiveOrders holds the IDs needed to cancel an externally-set spot
// protective order (OCO list, or a lone STOP_LOSS_LIMIT).
type spotProtectiveOrders struct {
	ListID            string // OCO orderListId; "" for a lone stop
	StopOrderID       string
	TakeProfitOrderID string
}

// SpotBroker implements port.Broker for Binance Spot REST calls.
type SpotBroker struct {
	client   *gobinance.Client
	mode     spotMode
	symbols  map[domain.Symbol]bool            // allowed trading pairs from config
	minLots  map[domain.Symbol]decimal.Decimal // minimum tradeable qty per symbol (dust filter)
	filters  *SpotExchangeInfo                 // symbol filter cache (PRICE/LOT/NOTIONAL)

	// resyncInterval overrides defaultPositionResyncInterval when positive.
	resyncInterval time.Duration

	mu        sync.RWMutex
	balances  map[string]spotBalance
	positions map[domain.Symbol]domain.Position
	// protective caches detected externally-set protective order IDs per symbol
	// (OCO list id + leg order ids) so a confirmed adjustment can cancel them.
	protective map[domain.Symbol]spotProtectiveOrders
}

// SetResyncInterval overrides the periodic REST position-resync cadence. A
// non-positive value is ignored, leaving the default in effect. Must be called
// before Connect.
func (b *SpotBroker) SetResyncInterval(d time.Duration) {
	if d > 0 {
		b.resyncInterval = d
	}
}

// NewSpotBroker creates a SpotBroker wrapping the provided client.
// symbols is the set of canonical symbols configured for spot trading;
// only balances matching these symbols are promoted to positions.
// minLots maps each symbol to its minimum lot size; balances below this
// threshold are treated as dust and excluded from the position list.
func NewSpotBroker(client *gobinance.Client, mode string, symbols []domain.Symbol, minLots map[domain.Symbol]decimal.Decimal) *SpotBroker {
	symSet := make(map[domain.Symbol]bool, len(symbols))
	for _, s := range symbols {
		symSet[s] = true
	}
	if minLots == nil {
		minLots = make(map[domain.Symbol]decimal.Decimal)
	}
	return &SpotBroker{
		client:     client,
		mode:       spotMode(mode),
		symbols:    symSet,
		minLots:    minLots,
		filters:    NewSpotExchangeInfo(client),
		balances:   make(map[string]spotBalance),
		positions:  make(map[domain.Symbol]domain.Position),
		protective: make(map[domain.Symbol]spotProtectiveOrders),
	}
}

// ExchangeInfo returns the filter store — useful to satisfy
// port.ExchangeInfoStore via the broker when the app wiring wants a single
// entry point per venue.
func (b *SpotBroker) ExchangeInfo() *SpotExchangeInfo { return b.filters }

// Venue identifies this broker.
func (b *SpotBroker) Venue() domain.Venue { return domain.VenueBinanceSpot }

// Connect bootstraps positions once, then maintains a local position cache from
// the private user-data websocket stream. A periodic REST resync runs alongside
// as a safety net for any WS events that are dropped or never delivered. It
// also loads the exchangeInfo filter cache so subsequent order placements can
// quantise qty/price to each symbol's tickSize and stepSize without an extra
// round-trip.
func (b *SpotBroker) Connect(ctx context.Context) error {
	if err := b.filters.Refresh(ctx); err != nil {
		// Soft-fail: we still want to come up even if exchangeInfo is
		// temporarily slow. PlaceOrder will return ErrSymbolFilterUnknown
		// until a later Refresh succeeds.
		slog.Warn("spot: exchange info refresh failed on connect; orders will reject until recovered", "error", err)
	}
	if err := b.bootstrapPositions(ctx); err != nil {
		return err
	}
	go b.runUserDataStream(ctx)
	go b.runPositionResync(ctx)
	return nil
}

// runPositionResync periodically re-fetches the authoritative balance snapshot
// over REST so the cache converges to the exchange's true state even when the
// user-data WS stream misses events. Exits cleanly on ctx cancel.
func (b *SpotBroker) runPositionResync(ctx context.Context) {
	interval := b.resyncInterval
	if interval <= 0 {
		interval = defaultPositionResyncInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			b.resyncPositions(ctx)
		}
	}
}

// StreamQuotes is not implemented on the REST broker; use KlinesWS.
func (b *SpotBroker) StreamQuotes(_ context.Context, _ []domain.Symbol) (<-chan domain.Quote, error) {
	return nil, fmt.Errorf("StreamQuotes: use KlinesWS for market data")
}

// PlaceOrder submits a new entry order to Binance Spot REST API, respecting
// intent.OrderType. Quantity is quantised to stepSize and price (where
// applicable) to tickSize using the cached exchangeInfo. Filter violations
// are returned as domain.ErrOrderBelowMinNotional etc. without hitting the
// exchange.
func (b *SpotBroker) PlaceOrder(ctx context.Context, intent domain.OrderIntent) (string, error) {
	side := gobinance.SideTypeBuy
	if intent.Side == domain.SideSell {
		side = gobinance.SideTypeSell
	}

	filter, err := b.filters.Filter(intent.Symbol)
	if err != nil {
		return "", err
	}

	qty := filter.QuantiseQty(intent.Quantity)
	if qty.IsZero() {
		return "", fmt.Errorf("spot: quantity rounded to zero after stepSize (%s); raw=%s",
			filter.StepSize, intent.Quantity)
	}

	svc := b.client.NewCreateOrderService().
		Symbol(domain.ToExchangeSymbol(intent.Symbol)).
		Side(side).
		NewClientOrderID(intent.ID).
		Quantity(qty.String())

	switch intent.OrderTypeOrDefault() {
	case domain.OrderTypeMarket:
		// For MARKET entries we cannot pre-check notional locally because
		// we don't know the fill price; Binance will accept and fill.
		svc = svc.Type(gobinance.OrderTypeMarket)

	case domain.OrderTypeLimit:
		if intent.LimitPrice.IsZero() {
			return "", fmt.Errorf("spot: limit order missing LimitPrice")
		}
		limitPx := filter.QuantisePrice(intent.LimitPrice, intent.Side)
		if err := filter.Validate(qty, limitPx); err != nil {
			return "", fmt.Errorf("spot: limit order filter: %w", err)
		}
		svc = svc.Type(gobinance.OrderTypeLimit).
			TimeInForce(toGoBinanceTIF(intent.TIF)).
			Price(limitPx.String())

	case domain.OrderTypeStopLimit:
		if intent.LimitPrice.IsZero() || intent.StopPrice.IsZero() {
			return "", fmt.Errorf("spot: stop-limit order requires both StopPrice and LimitPrice")
		}
		limitPx := filter.QuantisePrice(intent.LimitPrice, intent.Side)
		stopPx := filter.QuantisePrice(intent.StopPrice, intent.Side)
		if err := filter.Validate(qty, limitPx); err != nil {
			return "", fmt.Errorf("spot: stop-limit filter: %w", err)
		}
		svc = svc.Type(gobinance.OrderTypeStopLossLimit).
			TimeInForce(toGoBinanceTIF(intent.TIF)).
			Price(limitPx.String()).
			StopPrice(stopPx.String())

	default:
		return "", fmt.Errorf("spot: unsupported order type %q", intent.OrderType)
	}

	resp, err := svc.Do(ctx)
	if err != nil {
		return "", fmt.Errorf("binance spot place order: %w", err)
	}
	return strconv.FormatInt(resp.OrderID, 10), nil
}

// PlaceBracket attaches a protective OCO order to an open spot position.
// The OCO consists of a STOP_LOSS_LIMIT leg (triggered at req.StopLoss) and
// a LIMIT_MAKER leg (at req.TakeProfit). Whichever fills first cancels the
// other.
//
// Cerebro never runs a position without a stop, so a missing StopLoss is an
// error. TakeProfit is optional — when zero we fall back to a single
// STOP_LOSS_LIMIT order rather than an OCO, since Binance OCO requires both
// legs.
func (b *SpotBroker) PlaceBracket(ctx context.Context, req domain.BracketRequest) (domain.BracketResponse, error) {
	if req.StopLoss.IsZero() {
		return domain.BracketResponse{}, fmt.Errorf("spot bracket: missing StopLoss")
	}
	if req.Quantity.IsZero() {
		return domain.BracketResponse{}, fmt.Errorf("spot bracket: missing quantity")
	}

	filter, err := b.filters.Filter(req.Symbol)
	if err != nil {
		return domain.BracketResponse{}, err
	}
	qty := filter.QuantiseQty(req.Quantity)
	if qty.IsZero() {
		return domain.BracketResponse{}, fmt.Errorf("spot bracket: quantity zeroed by stepSize")
	}

	// Exit side is opposite the entry side.
	exitSide := gobinance.SideTypeSell
	if req.Side == domain.SideSell {
		exitSide = gobinance.SideTypeBuy
	}

	// Quantise prices. For an exit, SellSide wants prices rounded up and
	// BuySide rounded down (same rule as QuantisePrice).
	exitDomainSide := domain.SideSell
	if req.Side == domain.SideSell {
		exitDomainSide = domain.SideBuy
	}
	stopPx := filter.QuantisePrice(req.StopLoss, exitDomainSide)
	// The STOP_LOSS_LIMIT "price" leg is the worst price we'll accept after
	// the stop triggers. For longs exiting via sell: limit = stop * 0.99
	// gives us a 1% slippage budget. For shorts exiting via buy: 1.01×.
	// Callers may pre-set limitBufferedStopPx via req.TakeProfit when they
	// want a tighter bound; for now we hard-code a 0.5% buffer.
	stopLimitPx := applyStopLimitBuffer(stopPx, exitDomainSide, decimal.NewFromFloat(0.005))
	stopLimitPx = filter.QuantisePrice(stopLimitPx, exitDomainSide)

	sym := domain.ToExchangeSymbol(req.Symbol)
	tag := req.ClientTag
	if tag == "" {
		tag = "br"
	}
	stopCID := shortClientID(tag+"S", req.ParentIntentID)
	tpCID := shortClientID(tag+"T", req.ParentIntentID)
	listCID := shortClientID(tag+"L", req.ParentIntentID)

	// If TakeProfit is set, use OCO.
	if !req.TakeProfit.IsZero() {
		tpPx := filter.QuantisePrice(req.TakeProfit, exitDomainSide)
		if err := filter.Validate(qty, tpPx); err != nil {
			return domain.BracketResponse{}, fmt.Errorf("spot bracket TP filter: %w", err)
		}

		resp, err := b.client.NewCreateOCOService().
			Symbol(sym).
			Side(exitSide).
			Quantity(qty.String()).
			ListClientOrderID(listCID).
			LimitClientOrderID(tpCID).
			Price(tpPx.String()).
			StopClientOrderID(stopCID).
			StopPrice(stopPx.String()).
			StopLimitPrice(stopLimitPx.String()).
			StopLimitTimeInForce(gobinance.TimeInForceTypeGTC).
			Do(ctx)
		if err != nil {
			return domain.BracketResponse{}, fmt.Errorf("spot OCO: %w", err)
		}

		br := domain.BracketResponse{
			ListID: strconv.FormatInt(resp.OrderListID, 10),
			Symbol: req.Symbol,
		}
		for _, r := range resp.Orders {
			switch r.ClientOrderID {
			case stopCID:
				br.StopOrderID = strconv.FormatInt(r.OrderID, 10)
			case tpCID:
				br.TakeProfitOrderID = strconv.FormatInt(r.OrderID, 10)
			}
		}
		return br, nil
	}

	// SL-only fallback: a single STOP_LOSS_LIMIT order.
	if err := filter.Validate(qty, stopLimitPx); err != nil {
		return domain.BracketResponse{}, fmt.Errorf("spot bracket SL filter: %w", err)
	}
	resp, err := b.client.NewCreateOrderService().
		Symbol(sym).
		Side(exitSide).
		Type(gobinance.OrderTypeStopLossLimit).
		TimeInForce(gobinance.TimeInForceTypeGTC).
		NewClientOrderID(stopCID).
		Quantity(qty.String()).
		Price(stopLimitPx.String()).
		StopPrice(stopPx.String()).
		Do(ctx)
	if err != nil {
		return domain.BracketResponse{}, fmt.Errorf("spot stop-loss-limit: %w", err)
	}
	return domain.BracketResponse{
		StopOrderID: strconv.FormatInt(resp.OrderID, 10),
		Symbol:      req.Symbol,
	}, nil
}

// CancelBracket cancels both legs of a previously placed bracket.
// For OCO (ListID != "") one cancel cancels the whole list; for the
// SL-only fallback we cancel the single stop order directly.
func (b *SpotBroker) CancelBracket(ctx context.Context, resp domain.BracketResponse) error {
	if resp.Symbol == "" {
		return fmt.Errorf("spot: CancelBracket requires symbol")
	}
	sym := domain.ToExchangeSymbol(resp.Symbol)

	if resp.ListID != "" {
		listID, err := strconv.ParseInt(resp.ListID, 10, 64)
		if err != nil {
			return fmt.Errorf("spot: invalid bracket listID %q: %w", resp.ListID, err)
		}
		_, err = b.client.NewCancelOCOService().
			Symbol(sym).
			OrderListID(listID).
			Do(ctx)
		return err
	}

	// SL-only: cancel each leg that exists, tolerating per-leg failures.
	var firstErr error
	for _, id := range []string{resp.StopOrderID, resp.TakeProfitOrderID} {
		if id == "" {
			continue
		}
		if err := b.cancelSpotByID(ctx, sym, id); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// CancelOrder cancels a pending Binance Spot order by (symbol, brokerOrderID).
// The old single-arg form was a latent bug: Binance requires symbol on every
// cancel. The new domain.CancelRequest type makes this explicit.
func (b *SpotBroker) CancelOrder(ctx context.Context, req domain.CancelRequest) error {
	if req.Symbol == "" {
		return fmt.Errorf("spot: CancelOrder requires Symbol")
	}
	if req.BrokerOrderID == "" && req.ClientOrderID == "" {
		return fmt.Errorf("spot: CancelOrder requires BrokerOrderID or ClientOrderID")
	}
	sym := domain.ToExchangeSymbol(req.Symbol)

	svc := b.client.NewCancelOrderService().Symbol(sym)
	if req.BrokerOrderID != "" {
		orderID, err := strconv.ParseInt(req.BrokerOrderID, 10, 64)
		if err != nil {
			return fmt.Errorf("spot: invalid broker order ID %q: %w", req.BrokerOrderID, err)
		}
		svc = svc.OrderID(orderID)
	} else {
		svc = svc.OrigClientOrderID(req.ClientOrderID)
	}
	_, err := svc.Do(ctx)
	return err
}

// cancelSpotByID cancels by broker order ID; helper for CancelBracket's
// SL-only branch. brokerOrderID must be a numeric string.
func (b *SpotBroker) cancelSpotByID(ctx context.Context, sym, brokerOrderID string) error {
	orderID, err := strconv.ParseInt(brokerOrderID, 10, 64)
	if err != nil {
		return fmt.Errorf("spot: invalid broker order ID %q: %w", brokerOrderID, err)
	}
	_, err = b.client.NewCancelOrderService().Symbol(sym).OrderID(orderID).Do(ctx)
	return err
}

// toGoBinanceTIF maps our domain.TimeInForce to go-binance's type. Falls
// back to GTC when unset or unrecognised — matches Binance's default.
func toGoBinanceTIF(tif domain.TimeInForce) gobinance.TimeInForceType {
	switch tif {
	case domain.TIFIOC:
		return gobinance.TimeInForceTypeIOC
	case domain.TIFFOK:
		return gobinance.TimeInForceTypeFOK
	default:
		return gobinance.TimeInForceTypeGTC
	}
}

// applyStopLimitBuffer moves a stop-trigger price outward by bufferPct on
// the exit side so that the STOP_LOSS_LIMIT's price leg is executable
// after the trigger fires. Buffer is expressed as a fraction (0.005 = 0.5%).
// For a long exiting via sell: price = stop × (1 − buffer).
// For a short exiting via buy:  price = stop × (1 + buffer).
func applyStopLimitBuffer(stop decimal.Decimal, exitSide domain.Side, bufferPct decimal.Decimal) decimal.Decimal {
	one := decimal.NewFromInt(1)
	if exitSide == domain.SideSell {
		return stop.Mul(one.Sub(bufferPct))
	}
	return stop.Mul(one.Add(bufferPct))
}

// shortClientID produces a Binance newClientOrderID <= 36 chars combining a
// 2-3 char prefix and a truncated parent ID. This keeps bracket legs
// traceable back to their parent intent in audit logs and cancel flows.
func shortClientID(prefix, parent string) string {
	const maxLen = 36
	s := prefix + "-" + parent
	if len(s) > maxLen {
		s = s[:maxLen]
	}
	return s
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
	snapshot, err := b.fetchBalanceSnapshot(ctx)
	if err != nil {
		return err
	}
	// Fetch protective levels outside the lock — network call must not hold mu.
	stops, tps, ids := b.fetchProtectiveLevels(ctx)

	b.mu.Lock()
	b.balances = snapshot
	b.rebuildPositionsLocked()
	b.applyProtectiveLocked(stops, tps, ids)
	b.mu.Unlock()

	// Fetch current market prices for each discovered position so that
	// CurrentPrice and EntryPrice are not zero on startup.
	b.fetchCurrentPrices(ctx)

	return nil
}

// fetchBalanceSnapshot queries the spot account REST endpoint and returns the
// authoritative balance map. Shared by bootstrap (replace + price backfill)
// and the periodic resync (replace via applyBalanceSnapshot).
func (b *SpotBroker) fetchBalanceSnapshot(ctx context.Context) (map[string]spotBalance, error) {
	account, err := b.client.NewGetAccountService().Do(ctx)
	if err != nil {
		return nil, fmt.Errorf("binance spot get account: %w", err)
	}
	next := make(map[string]spotBalance, len(account.Balances))
	for _, bal := range account.Balances {
		free, _ := decimal.NewFromString(bal.Free)
		locked, _ := decimal.NewFromString(bal.Locked)
		next[strings.ToUpper(bal.Asset)] = spotBalance{free: free, locked: locked}
	}
	return next, nil
}

// resyncPositions fetches an authoritative REST balance snapshot, applies it
// wholesale, and backfills prices for any newly-discovered positions. This
// recovers any open/close the user-data WS stream missed. Errors are logged,
// not fatal — the next tick retries.
func (b *SpotBroker) resyncPositions(ctx context.Context) {
	snapshot, err := b.fetchBalanceSnapshot(ctx)
	if err != nil {
		slog.WarnContext(ctx, "spot position resync failed", "err", err)
		return
	}
	// Fetch protective levels outside the lock — network call must not hold mu.
	stops, tps, ids := b.fetchProtectiveLevels(ctx)
	b.applyBalanceSnapshot(snapshot, stops, tps, ids)
	b.fetchCurrentPrices(ctx)
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

	// Unwrap: the WS API may nest the actual event under "event" or "data".
	payload := message
	var wrapper struct {
		Inner json.RawMessage `json:"event"`
		Data  json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(message, &wrapper); err != nil {
		return err
	}
	if len(wrapper.Inner) > 0 {
		payload = wrapper.Inner
	} else if len(wrapper.Data) > 0 {
		payload = wrapper.Data
	}

	// Extract event type. The E field (timestamp) is declared to prevent
	// case-insensitive collision with the e field (event type).
	var header struct {
		EventType string `json:"e"`
		Timestamp int64  `json:"E"`
	}
	if err := json.Unmarshal(payload, &header); err != nil {
		return err
	}

	switch header.EventType {
	case "outboundAccountPosition":
		var evt struct {
			Balances []struct {
				Asset  string `json:"a"`
				Free   string `json:"f"`
				Locked string `json:"l"`
			} `json:"B"`
		}
		if err := json.Unmarshal(payload, &evt); err != nil {
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

// applyBalanceSnapshot replaces the cached balance map with a fresh,
// authoritative snapshot (e.g. from a REST resync) and rebuilds the position
// view. Balances are REPLACED wholesale rather than merged: an asset sold to
// zero is simply absent from the snapshot, so a merge would leave a stale
// non-zero balance and a phantom position. Cerebro-internal position metadata
// (SL/TP/Strategy/CorrelationID/OpenedAt) is preserved by
// rebuildPositionsLocked for surviving symbols. This recovers state the
// user-data WS may have missed.
func (b *SpotBroker) applyBalanceSnapshot(snapshot map[string]spotBalance, stops, tps map[string]decimal.Decimal, ids map[string]spotProtectiveOrders) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.balances = make(map[string]spotBalance, len(snapshot))
	for asset, bal := range snapshot {
		b.balances[strings.ToUpper(asset)] = bal
	}
	b.rebuildPositionsLocked()
	b.applyProtectiveLocked(stops, tps, ids)
}

// spotSymbolToDomain converts a raw exchange symbol (e.g. "ETHUSDT") to the
// domain canonical form ("ETH/USDT"). Only USDT-quoted pairs are supported;
// others are returned unchanged as a defensive fallback.
func spotSymbolToDomain(raw string) string {
	if strings.HasSuffix(raw, "USDT") {
		return raw[:len(raw)-4] + "/USDT"
	}
	return raw
}

// detectSpotProtectiveLevels extracts externally-set stop / take-profit levels
// and their order IDs from open spot orders. STOP_LOSS_LIMIT supplies the stop
// (from StopPrice); the OCO LIMIT_MAKER leg supplies the take-profit (from
// Price). Keys are raw exchange symbols (e.g. "ETHUSDT"). If a symbol has
// multiple orders of the same type, the last one seen wins.
func detectSpotProtectiveLevels(orders []*gobinance.Order) (
	stops map[string]decimal.Decimal,
	tps map[string]decimal.Decimal,
	ids map[string]spotProtectiveOrders,
) {
	stops = make(map[string]decimal.Decimal)
	tps = make(map[string]decimal.Decimal)
	ids = make(map[string]spotProtectiveOrders)
	for _, o := range orders {
		if o == nil {
			continue
		}
		cur := ids[o.Symbol]
		if o.OrderListId > 0 {
			cur.ListID = strconv.FormatInt(o.OrderListId, 10)
		}
		switch o.Type {
		case gobinance.OrderTypeStopLossLimit:
			px, err := decimal.NewFromString(o.StopPrice)
			if err != nil || px.IsZero() {
				continue
			}
			stops[o.Symbol] = px
			cur.StopOrderID = strconv.FormatInt(o.OrderID, 10)
		case gobinance.OrderTypeLimitMaker:
			px, err := decimal.NewFromString(o.Price)
			if err != nil || px.IsZero() {
				continue
			}
			tps[o.Symbol] = px
			cur.TakeProfitOrderID = strconv.FormatInt(o.OrderID, 10)
		default:
			continue
		}
		ids[o.Symbol] = cur
	}
	return stops, tps, ids
}

// fetchProtectiveLevels lists open orders and detects externally-set protective
// levels. Non-fatal: on error it logs and returns empty maps so a transient
// failure never blocks position bootstrap/resync.
func (b *SpotBroker) fetchProtectiveLevels(ctx context.Context) (
	map[string]decimal.Decimal, map[string]decimal.Decimal, map[string]spotProtectiveOrders,
) {
	orders, err := b.client.NewListOpenOrdersService().Do(ctx)
	if err != nil {
		slog.WarnContext(ctx, "spot open-orders fetch for SL/TP detection failed", "err", err)
		return nil, nil, nil
	}
	return detectSpotProtectiveLevels(orders)
}

// applyProtectiveLocked stamps detected exchange-side SL/TP levels onto the
// freshly-rebuilt positions and records the cancellation IDs. Caller MUST hold
// b.mu. Keys in the input maps are raw exchange symbols (e.g. "ETHUSDT").
func (b *SpotBroker) applyProtectiveLocked(stops, tps map[string]decimal.Decimal, ids map[string]spotProtectiveOrders) {
	detected := make(map[domain.Symbol]spotProtectiveOrders, len(ids))
	for rawSym, po := range ids {
		sym := domain.Symbol(spotSymbolToDomain(rawSym))
		pos, ok := b.positions[sym]
		if !ok {
			continue
		}
		if sl, has := stops[rawSym]; has {
			pos.StopLoss = sl
		}
		if tp, has := tps[rawSym]; has {
			pos.TakeProfit1 = tp
		}
		if !pos.StopLoss.IsZero() || !pos.TakeProfit1.IsZero() {
			pos.ExternallyProtected = true
		}
		b.positions[sym] = pos
		detected[sym] = po
	}
	b.protective = detected
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
		// Filter dust: skip balances below the minimum lot size for this symbol.
		if minLot, ok := b.minLots[sym]; ok && !minLot.IsZero() && total.LessThan(minLot) {
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

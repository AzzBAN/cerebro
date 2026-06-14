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

// protectiveOrders holds the exchange order IDs of an externally-set
// stop / take-profit pair for one symbol.
type protectiveOrders struct {
	StopOrderID       string
	TakeProfitOrderID string
}

const userDataKeepaliveInterval = 25 * time.Minute

// defaultPositionResyncInterval is how often the broker re-fetches the
// authoritative position snapshot over REST to recover any open/close the
// user-data WS stream may have missed (dropped events, silent socket stalls).
// Overridable per-deployment via engine.position_resync_interval_ms.
const defaultPositionResyncInterval = 5 * time.Second

const (
	futuresModeMainnet futuresMode = "mainnet"
	futuresModeTestnet futuresMode = "testnet"
	futuresModeDemo    futuresMode = "demo"
)

// orderTypeStopMarket and orderTypeTakeProfitMarket are the go-binance futures
// OrderType values for conditional close legs. The SDK only pre-declares Limit,
// Market and Liquidation — these two are cast from their wire strings.
const (
	orderTypeStopMarket      = gobinancefutures.OrderType("STOP_MARKET")
	orderTypeTakeProfitMarket = gobinancefutures.OrderType("TAKE_PROFIT_MARKET")
)

// FuturesBroker implements port.Broker for Binance USDT-M Futures REST API.
type FuturesBroker struct {
	client  *gobinancefutures.Client
	mode    futuresMode
	filters *FuturesExchangeInfo

	// resyncInterval overrides defaultPositionResyncInterval when positive.
	resyncInterval time.Duration

	mu            sync.RWMutex
	positions     map[domain.Symbol]domain.Position
	leverageCache map[domain.Symbol]int // last-applied leverage per symbol (avoids redundant REST calls)

	// protective caches the exchange order IDs of detected externally-set
	// STOP_MARKET / TAKE_PROFIT_MARKET orders per symbol, so a confirmed
	// adjustment can cancel the exact orders. Guarded by b.mu.
	protective map[domain.Symbol]protectiveOrders
}

// SetResyncInterval overrides the periodic REST position-resync cadence. A
// non-positive value is ignored, leaving the default in effect. Must be called
// before Connect.
func (b *FuturesBroker) SetResyncInterval(d time.Duration) {
	if d > 0 {
		b.resyncInterval = d
	}
}

// NewFuturesBroker creates a FuturesBroker.
func NewFuturesBroker(client *gobinancefutures.Client, mode string) *FuturesBroker {
	return &FuturesBroker{
		client:        client,
		mode:          futuresMode(mode),
		filters:       NewFuturesExchangeInfo(client),
		positions:     make(map[domain.Symbol]domain.Position),
		leverageCache: make(map[domain.Symbol]int),
		protective:    make(map[domain.Symbol]protectiveOrders),
	}
}

// ExchangeInfo exposes the filter store for callers that need to satisfy
// port.ExchangeInfoStore directly.
func (b *FuturesBroker) ExchangeInfo() *FuturesExchangeInfo { return b.filters }

// Venue identifies this broker endpoint.
func (b *FuturesBroker) Venue() domain.Venue { return domain.VenueBinanceFutures }

// Connect bootstraps positions once, then keeps them fresh via the private
// user-data websocket. A periodic REST resync runs alongside as a safety net
// for any WS events that are dropped or never delivered. It also preloads the
// exchangeInfo filter cache.
func (b *FuturesBroker) Connect(ctx context.Context) error {
	if err := b.filters.Refresh(ctx); err != nil {
		slog.Warn("futures: exchange info refresh failed on connect; orders will reject until recovered", "error", err)
	}
	if err := b.bootstrapPositions(ctx); err != nil {
		return err
	}
	go b.runUserDataStream(ctx)
	go b.runPositionResync(ctx)
	return nil
}

// runPositionResync periodically re-fetches the authoritative position
// snapshot over REST so the cache converges to the exchange's true state even
// when the user-data WS stream misses events. Exits cleanly on ctx cancel.
func (b *FuturesBroker) runPositionResync(ctx context.Context) {
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

// StreamQuotes is not supported on REST; use the futures KlinesWS.
func (b *FuturesBroker) StreamQuotes(_ context.Context, _ []domain.Symbol) (<-chan domain.Quote, error) {
	return nil, fmt.Errorf("futures broker: use KlinesWS for market data")
}

// PlaceOrder submits a futures entry order. Supports MARKET, LIMIT, and
// STOP_LIMIT types. Also applies intent.Leverage / intent.PositionSide /
// intent.ReduceOnly where set.
func (b *FuturesBroker) PlaceOrder(ctx context.Context, intent domain.OrderIntent) (string, error) {
	side := gobinancefutures.SideTypeBuy
	if intent.Side == domain.SideSell {
		side = gobinancefutures.SideTypeSell
	}

	filter, err := b.filters.Filter(intent.Symbol)
	if err != nil {
		return "", err
	}
	qty := filter.QuantiseQty(intent.Quantity)
	if qty.IsZero() {
		return "", fmt.Errorf("futures: quantity rounded to zero after stepSize (%s); raw=%s",
			filter.StepSize, intent.Quantity)
	}

	// Sync leverage if the caller asked for a specific value and it differs
	// from the last-applied cache. Skipped when intent.Leverage is 0.
	if intent.Leverage > 0 {
		if err := b.ensureLeverage(ctx, intent.Symbol, intent.Leverage); err != nil {
			return "", fmt.Errorf("futures: set leverage: %w", err)
		}
	}

	svc := b.client.NewCreateOrderService().
		Symbol(domain.ToExchangeSymbol(intent.Symbol)).
		Side(side).
		NewClientOrderID(intent.ID).
		Quantity(qty.String())

	if intent.PositionSide != "" {
		svc = svc.PositionSide(toFuturesPositionSide(intent.PositionSide))
	}
	if intent.ReduceOnly {
		svc = svc.ReduceOnly(true)
	}

	switch intent.OrderTypeOrDefault() {
	case domain.OrderTypeMarket:
		svc = svc.Type(gobinancefutures.OrderTypeMarket)

	case domain.OrderTypeLimit:
		if intent.LimitPrice.IsZero() {
			return "", fmt.Errorf("futures: limit order missing LimitPrice")
		}
		limitPx := filter.QuantisePrice(intent.LimitPrice, intent.Side)
		if err := filter.Validate(qty, limitPx); err != nil {
			return "", fmt.Errorf("futures: limit filter: %w", err)
		}
		svc = svc.Type(gobinancefutures.OrderTypeLimit).
			TimeInForce(toFuturesTIF(intent.TIF)).
			Price(limitPx.String())

	case domain.OrderTypeStopLimit:
		if intent.LimitPrice.IsZero() || intent.StopPrice.IsZero() {
			return "", fmt.Errorf("futures: stop-limit requires both StopPrice and LimitPrice")
		}
		limitPx := filter.QuantisePrice(intent.LimitPrice, intent.Side)
		stopPx := filter.QuantisePrice(intent.StopPrice, intent.Side)
		if err := filter.Validate(qty, limitPx); err != nil {
			return "", fmt.Errorf("futures: stop-limit filter: %w", err)
		}
		// Futures uses a single "STOP" order type with both price + stopPrice.
		svc = svc.Type(gobinancefutures.OrderType("STOP")).
			TimeInForce(toFuturesTIF(intent.TIF)).
			Price(limitPx.String()).
			StopPrice(stopPx.String()).
			WorkingType(gobinancefutures.WorkingTypeMarkPrice)

	default:
		return "", fmt.Errorf("futures: unsupported order type %q", intent.OrderType)
	}

	resp, err := svc.Do(ctx)
	if err != nil {
		return "", fmt.Errorf("binance futures place order: %w", err)
	}
	return strconv.FormatInt(resp.OrderID, 10), nil
}

// PlaceBracket attaches a reduce-only STOP_MARKET + TAKE_PROFIT_MARKET pair
// to an open futures position, both anchored to the MARK price. Either leg
// firing closes the full position via closePosition=true, which is Binance's
// recommended pattern for algo brackets in one-way mode.
//
// Returns a non-nil error when the stop leg could not be placed. The TP leg
// is best-effort — if it fails after the stop succeeded, we log and return
// the partial bracket with an error so the caller can decide whether to
// retry TP or cancel the stop.
func (b *FuturesBroker) PlaceBracket(ctx context.Context, req domain.BracketRequest) (domain.BracketResponse, error) {
	if req.StopLoss.IsZero() {
		return domain.BracketResponse{}, fmt.Errorf("futures bracket: missing StopLoss")
	}
	if req.Quantity.IsZero() {
		return domain.BracketResponse{}, fmt.Errorf("futures bracket: missing quantity")
	}

	filter, err := b.filters.Filter(req.Symbol)
	if err != nil {
		return domain.BracketResponse{}, err
	}

	// Exit side is opposite the entry side.
	exitSide := gobinancefutures.SideTypeSell
	exitDomainSide := domain.SideSell
	if req.Side == domain.SideSell {
		exitSide = gobinancefutures.SideTypeBuy
		exitDomainSide = domain.SideBuy
	}

	stopPx := filter.QuantisePrice(req.StopLoss, exitDomainSide)
	tag := req.ClientTag
	if tag == "" {
		tag = "br"
	}
	sym := domain.ToExchangeSymbol(req.Symbol)
	stopCID := shortFuturesClientID(tag+"S", req.ParentIntentID)

	// Protective stop leg (STOP_MARKET, closePosition=true). On the Demo
	// endpoint these conditional types are rejected by the standard order
	// endpoint (code -4120) and must go through the Algo Order API; see
	// placeProtectiveLeg.
	stopID, err := b.placeProtectiveLeg(ctx, sym, exitSide, "STOP_MARKET", stopPx.String(), stopCID, req.PositionSide)
	if err != nil {
		return domain.BracketResponse{}, fmt.Errorf("futures STOP_MARKET: %w", err)
	}
	br := domain.BracketResponse{
		StopOrderID: stopID,
		Symbol:      req.Symbol,
	}

	// TAKE_PROFIT_MARKET (optional — only when caller provided a TP level).
	if !req.TakeProfit.IsZero() {
		tpPx := filter.QuantisePrice(req.TakeProfit, exitDomainSide)
		tpCID := shortFuturesClientID(tag+"T", req.ParentIntentID)
		tpID, tperr := b.placeProtectiveLeg(ctx, sym, exitSide, "TAKE_PROFIT_MARKET", tpPx.String(), tpCID, req.PositionSide)
		if tperr != nil {
			// Partial bracket: stop is live, TP failed. Surface the error
			// to the caller; they own the retry / unwind decision.
			slog.Error("futures: TP_MARKET leg failed; bracket left partial",
				"symbol", req.Symbol, "error", tperr,
				"stop_order_id", br.StopOrderID)
			return br, fmt.Errorf("futures TAKE_PROFIT_MARKET (stop placed OK): %w", tperr)
		}
		br.TakeProfitOrderID = tpID
	}

	return br, nil
}

// placeProtectiveLeg submits one conditional close-position leg (STOP_MARKET or
// TAKE_PROFIT_MARKET, closePosition=true, anchored to the mark price).
//
// The Demo endpoint (demo-fapi.binance.com) rejects these conditional types on
// the standard /fapi/v1/order endpoint with code -4120 and requires the Algo
// Order API (/fapi/v1/algoOrder, algoType=CONDITIONAL). Mainnet and testnet
// accept them on the standard endpoint, so we branch on mode and keep the
// proven standard path for live trading. Returns the leg's order ID as a
// string (the algo id on demo, the order id otherwise).
func (b *FuturesBroker) placeProtectiveLeg(
	ctx context.Context,
	sym string,
	exitSide gobinancefutures.SideType,
	orderType string,
	triggerPx string,
	clientID string,
	positionSide domain.PositionSide,
) (string, error) {
	if b.mode == futuresModeDemo {
		svc := b.client.NewCreateAlgoOrderService().
			AlgoType(gobinancefutures.OrderAlgoTypeConditional).
			Symbol(sym).
			Side(exitSide).
			Type(gobinancefutures.AlgoOrderType(orderType)).
			TriggerPrice(triggerPx).
			ClosePosition(true).
			WorkingType(gobinancefutures.WorkingTypeMarkPrice).
			PriceProtect(true).
			ClientAlgoId(clientID)
		if positionSide != "" {
			svc = svc.PositionSide(toFuturesPositionSide(positionSide))
		}
		resp, err := svc.Do(ctx)
		if err != nil {
			return "", err
		}
		return strconv.FormatInt(resp.AlgoId, 10), nil
	}

	// Mainnet / testnet: standard order endpoint.
	//   - closePosition exits the full position regardless of quantity.
	//   - reduceOnly is mutually exclusive with closePosition; omitted here.
	svc := b.client.NewCreateOrderService().
		Symbol(sym).
		Side(exitSide).
		Type(gobinancefutures.OrderType(orderType)).
		StopPrice(triggerPx).
		ClosePosition(true).
		WorkingType(gobinancefutures.WorkingTypeMarkPrice).
		PriceProtect(true).
		NewClientOrderID(clientID)
	if positionSide != "" {
		svc = svc.PositionSide(toFuturesPositionSide(positionSide))
	}
	resp, err := svc.Do(ctx)
	if err != nil {
		return "", err
	}
	return strconv.FormatInt(resp.OrderID, 10), nil
}
// Each leg is cancelled independently; errors are aggregated so that one
// failed cancel doesn't leave the other leg live unnoticed.
func (b *FuturesBroker) CancelBracket(ctx context.Context, resp domain.BracketResponse) error {
	if resp.Symbol == "" {
		return fmt.Errorf("futures: CancelBracket requires symbol")
	}
	sym := domain.ToExchangeSymbol(resp.Symbol)

	var errs []string
	for _, id := range []string{resp.StopOrderID, resp.TakeProfitOrderID} {
		if id == "" {
			continue
		}
		orderID, perr := strconv.ParseInt(id, 10, 64)
		if perr != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", id, perr))
			continue
		}
		// Demo brackets are placed via the Algo Order API, so they must be
		// cancelled through it too — the standard cancel endpoint does not
		// know these IDs. Mainnet/testnet use the standard order cancel.
		if b.mode == futuresModeDemo {
			if _, cerr := b.client.NewCancelAlgoOrderService().
				AlgoID(orderID).
				Do(ctx); cerr != nil {
				errs = append(errs, fmt.Sprintf("%s: %v", id, cerr))
			}
			continue
		}
		if _, cerr := b.client.NewCancelOrderService().
			Symbol(sym).
			OrderID(orderID).
			Do(ctx); cerr != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", id, cerr))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("futures CancelBracket: %s", strings.Join(errs, "; "))
	}
	return nil
}

// CancelOrder cancels a pending futures order. Unlike the old single-arg
// form, this takes a CancelRequest — Binance futures requires symbol on
// every cancel.
func (b *FuturesBroker) CancelOrder(ctx context.Context, req domain.CancelRequest) error {
	if req.Symbol == "" {
		return fmt.Errorf("futures: CancelOrder requires Symbol")
	}
	if req.BrokerOrderID == "" && req.ClientOrderID == "" {
		return fmt.Errorf("futures: CancelOrder requires BrokerOrderID or ClientOrderID")
	}
	sym := domain.ToExchangeSymbol(req.Symbol)
	svc := b.client.NewCancelOrderService().Symbol(sym)
	if req.BrokerOrderID != "" {
		orderID, err := strconv.ParseInt(req.BrokerOrderID, 10, 64)
		if err != nil {
			return fmt.Errorf("futures: invalid broker order ID %q: %w", req.BrokerOrderID, err)
		}
		svc = svc.OrderID(orderID)
	} else {
		svc = svc.OrigClientOrderID(req.ClientOrderID)
	}
	_, err := svc.Do(ctx)
	return err
}

// ensureLeverage calls ChangeLeverage only when the requested value differs
// from what we previously set. Binance tolerates idempotent writes but the
// endpoint counts against request-weight, so we cache.
func (b *FuturesBroker) ensureLeverage(ctx context.Context, symbol domain.Symbol, lev int) error {
	b.mu.RLock()
	cached := b.leverageCache[symbol]
	b.mu.RUnlock()
	if cached == lev {
		return nil
	}
	if _, err := b.client.NewChangeLeverageService().
		Symbol(domain.ToExchangeSymbol(symbol)).
		Leverage(lev).
		Do(ctx); err != nil {
		return err
	}
	b.mu.Lock()
	b.leverageCache[symbol] = lev
	b.mu.Unlock()
	return nil
}

// toFuturesTIF maps our domain.TimeInForce to the futures SDK's type.
func toFuturesTIF(tif domain.TimeInForce) gobinancefutures.TimeInForceType {
	switch tif {
	case domain.TIFIOC:
		return gobinancefutures.TimeInForceTypeIOC
	case domain.TIFFOK:
		return gobinancefutures.TimeInForceTypeFOK
	default:
		return gobinancefutures.TimeInForceTypeGTC
	}
}

// toFuturesPositionSide maps our PositionSide to the futures SDK's type.
func toFuturesPositionSide(ps domain.PositionSide) gobinancefutures.PositionSideType {
	switch ps {
	case domain.PositionSideLong:
		return gobinancefutures.PositionSideTypeLong
	case domain.PositionSideShort:
		return gobinancefutures.PositionSideTypeShort
	default:
		return gobinancefutures.PositionSideTypeBoth
	}
}

// shortFuturesClientID builds a bracket-leg client order ID <= 36 chars.
// Binance futures has the same 36-char limit as spot on newClientOrderID.
func shortFuturesClientID(prefix, parent string) string {
	const maxLen = 36
	s := prefix + "-" + parent
	if len(s) > maxLen {
		s = s[:maxLen]
	}
	return s
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

// applyPositionSnapshot reconciles the in-memory cache against a fresh,
// authoritative snapshot (e.g. from a REST resync). Positions absent from the
// snapshot are dropped (closed on the exchange); positions present are taken
// from the snapshot for live fields (qty/price/leverage/margin) while
// Cerebro-internal metadata that the exchange does not track — OpenedAt, SL/TP
// levels, Strategy, CorrelationID — is carried forward from any existing entry.
// This is the recovery path for user-data WS events that were missed or never
// delivered.
func (b *FuturesBroker) applyPositionSnapshot(snapshot map[domain.Symbol]domain.Position, detected map[domain.Symbol]protectiveOrders) {
	b.mu.Lock()
	defer b.mu.Unlock()

	next := make(map[domain.Symbol]domain.Position, len(snapshot))
	for sym, pos := range snapshot {
		if existing, ok := b.positions[sym]; ok {
			if !existing.OpenedAt.IsZero() {
				pos.OpenedAt = existing.OpenedAt
			}
			if pos.StopLoss.IsZero() && pos.TakeProfit1.IsZero() && !pos.ExternallyProtected {
				pos.StopLoss = existing.StopLoss
				pos.TakeProfit1 = existing.TakeProfit1
			}
			pos.Strategy = existing.Strategy
			pos.CorrelationID = existing.CorrelationID
		}
		next[sym] = pos
	}
	b.positions = next
	b.protective = detected
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
	snapshot, detected, err := b.fetchPositionSnapshot(ctx)
	if err != nil {
		return err
	}
	b.mu.Lock()
	b.positions = snapshot
	b.protective = detected
	b.mu.Unlock()
	return nil
}

// fetchPositionSnapshot queries the futures account REST endpoint and returns
// the authoritative open-position map. Shared by bootstrap (replace) and the
// periodic resync (merge via applyPositionSnapshot).
func (b *FuturesBroker) fetchPositionSnapshot(ctx context.Context) (map[domain.Symbol]domain.Position, map[domain.Symbol]protectiveOrders, error) {
	account, err := b.client.NewGetAccountService().Do(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("binance futures get account: %w", err)
	}

	next := make(map[domain.Symbol]domain.Position)
	for _, p := range account.Positions {
		// The account endpoint does not expose markPrice directly. Derive it
		// from notional (= markPrice * positionAmt, signed): markPrice =
		// |notional| / |positionAmt|. Passing UnrealizedProfit here was a bug —
		// it stored PnL (which is negative for a loser) into CurrentPrice.
		markStr := deriveFuturesMarkPrice(p.Notional, p.PositionAmt)
		pos, ok, err := futuresAccountPositionToDomain(p.Symbol, p.PositionAmt, p.EntryPrice, markStr)
		if err != nil {
			return nil, nil, err
		}
		if !ok {
			continue
		}
		if lev, convErr := strconv.Atoi(p.Leverage); convErr == nil {
			pos.Leverage = lev
		}
		pos.Isolated = p.Isolated
		// Prefer the actual exchange-allocated margin: `isolatedWallet` for
		// isolated mode, otherwise `positionInitialMargin` for cross. Falls
		// back to the derived notional/leverage via Position.EffectiveMargin
		// when both are unset.
		if p.Isolated {
			if iw, err := decimal.NewFromString(p.IsolatedWallet); err == nil {
				pos.Margin = iw
			}
		} else {
			if pim, err := decimal.NewFromString(p.PositionInitialMargin); err == nil {
				pos.Margin = pim
			}
		}
		next[pos.Symbol] = pos
	}

	openOrders, ooErr := b.client.NewListOpenOrdersService().Do(ctx)
	if ooErr != nil {
		slog.WarnContext(ctx, "futures open-orders fetch for SL/TP detection failed", "err", ooErr)
		return next, nil, nil
	}
	stops, tps, ids := detectProtectiveLevels(openOrders)
	detected := make(map[domain.Symbol]protectiveOrders, len(ids))
	for rawSym, po := range ids {
		sym := domain.Symbol(normaliseFuturesSymbol(rawSym))
		if sym == "" {
			continue
		}
		pos, ok := next[sym]
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
		next[sym] = pos
		detected[sym] = po
	}

	return next, detected, nil
}

// resyncPositions fetches an authoritative REST snapshot and merges it into the
// cache, recovering any open/close the user-data WS stream missed. Errors are
// logged, not fatal — the next tick retries.
func (b *FuturesBroker) resyncPositions(ctx context.Context) {
	snapshot, detected, err := b.fetchPositionSnapshot(ctx)
	if err != nil {
		slog.WarnContext(ctx, "futures position resync failed", "err", err)
		return
	}
	b.applyPositionSnapshot(snapshot, detected)
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
		// EventTime absorbs the numeric "E" (event time, ms) key. Without an
		// explicit field, encoding/json's case-insensitive fallback matches
		// "E" to the "e" string field and fails to unmarshal the number,
		// dropping otherwise-valid messages.
		EventTime int64 `json:"E"`
	}
	if err := json.Unmarshal(message, &envelope); err != nil {
		return err
	}
	switch envelope.Event {
	case "ACCOUNT_UPDATE":
		return b.handleAccountUpdate(message)
	case "ACCOUNT_CONFIG_UPDATE":
		return b.handleAccountConfigUpdate(message)
	}
	return nil
}

func (b *FuturesBroker) handleAccountUpdate(message []byte) error {
	var evt struct {
		Account struct {
			Positions []struct {
				Symbol         string `json:"s"`
				Amount         string `json:"pa"`
				EntryPrice     string `json:"ep"`
				MarkPrice      string `json:"mp"`
				IsolatedWallet string `json:"iw"`
				MarginType     string `json:"mt"`
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
		existing, hasExisting := b.positions[pos.Symbol]
		if hasExisting {
			pos.OpenedAt = existing.OpenedAt
			pos.StopLoss = existing.StopLoss
			pos.TakeProfit1 = existing.TakeProfit1
			pos.Strategy = existing.Strategy
			pos.CorrelationID = existing.CorrelationID
			pos.Leverage = existing.Leverage
			// Carry forward margin/isolated by default; the WS payload is
			// updated below only when the relevant fields are present.
			pos.Margin = existing.Margin
			pos.Isolated = existing.Isolated
		}
		// `mt` / `iw` are only sent when isolated margin changes. Cross
		// positions don't get `positionInitialMargin` over the user-data
		// stream — those refresh on the next REST bootstrap.
		switch strings.ToLower(p.MarginType) {
		case "isolated":
			pos.Isolated = true
		case "cross", "crossed":
			pos.Isolated = false
			pos.Margin = decimal.Zero
		}
		if p.IsolatedWallet != "" {
			if iw, parseErr := decimal.NewFromString(p.IsolatedWallet); parseErr == nil {
				pos.Margin = iw
			}
		}
		b.positions[pos.Symbol] = pos
	}
	return nil
}

// handleAccountConfigUpdate tracks leverage changes pushed via the user-data
// stream so the cached Position.Leverage stays in sync with Binance.
// Payload shape: {"e":"ACCOUNT_CONFIG_UPDATE","ac":{"s":"BTCUSDT","l":125}}
func (b *FuturesBroker) handleAccountConfigUpdate(message []byte) error {
	var evt struct {
		AC struct {
			Symbol   string `json:"s"`
			Leverage int    `json:"l"`
		} `json:"ac"`
	}
	if err := json.Unmarshal(message, &evt); err != nil {
		return err
	}
	if evt.AC.Symbol == "" || evt.AC.Leverage <= 0 {
		return nil
	}
	sym, err := domain.NormalizeExchangeSymbol(evt.AC.Symbol, domain.ContractFuturesPerp)
	if err != nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if existing, ok := b.positions[sym]; ok {
		existing.Leverage = evt.AC.Leverage
		b.positions[sym] = existing
	}
	return nil
}

// deriveFuturesMarkPrice computes the mark price from a position's signed
// notional and signed position amount: markPrice = |notional| / |positionAmt|.
// The futures account endpoint exposes notional and positionAmt but not
// markPrice directly. Returns "" when either input is missing or the amount is
// zero, so the caller's decimal parse degrades to zero rather than a bad value.
func deriveFuturesMarkPrice(notionalStr, amountStr string) string {
	notional, err := decimal.NewFromString(notionalStr)
	if err != nil {
		return ""
	}
	amount, err := decimal.NewFromString(amountStr)
	if err != nil || amount.IsZero() {
		return ""
	}
	return notional.Abs().Div(amount.Abs()).String()
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

// normaliseFuturesSymbol converts a raw exchange symbol (e.g. "BTCUSDT") to the
// canonical domain.Symbol used as the positions map key (e.g. "BTC/USDT-PERP").
// It mirrors the conversion inside futuresAccountPositionToDomain exactly.
func normaliseFuturesSymbol(raw string) string {
	sym, err := domain.NormalizeExchangeSymbol(raw, domain.ContractFuturesPerp)
	if err != nil {
		return ""
	}
	return string(sym)
}

// detectProtectiveLevels extracts externally-set stop / take-profit levels and
// their order IDs from a list of open futures orders. Only reduce-only or
// closePosition conditional orders count as protective. Keys are raw exchange
// symbols (e.g. "BTCUSDT"); caller maps to domain.Symbol.
func detectProtectiveLevels(orders []*gobinancefutures.Order) (
	stops map[string]decimal.Decimal,
	tps map[string]decimal.Decimal,
	ids map[string]protectiveOrders,
) {
	stops = make(map[string]decimal.Decimal)
	tps = make(map[string]decimal.Decimal)
	ids = make(map[string]protectiveOrders)
	for _, o := range orders {
		if o == nil || (!o.ClosePosition && !o.ReduceOnly) {
			continue
		}
		px, err := decimal.NewFromString(o.StopPrice)
		if err != nil || px.IsZero() {
			continue
		}
		cur := ids[o.Symbol]
		// If a symbol has multiple orders of the same type, the last one seen
		// wins — Binance should only have one live protective order per leg.
		switch o.Type {
		case orderTypeStopMarket:
			stops[o.Symbol] = px
			cur.StopOrderID = strconv.FormatInt(o.OrderID, 10)
		case orderTypeTakeProfitMarket:
			tps[o.Symbol] = px
			cur.TakeProfitOrderID = strconv.FormatInt(o.OrderID, 10)
		default:
			continue
		}
		ids[o.Symbol] = cur
	}
	return stops, tps, ids
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

// ProtectiveBracket returns a BracketResponse describing the externally-set
// protective orders detected for sym, suitable for CancelBracket. ok is false
// when no externally-set protection is cached.
func (b *FuturesBroker) ProtectiveBracket(sym domain.Symbol) (domain.BracketResponse, bool) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	po, ok := b.protective[sym]
	if !ok {
		return domain.BracketResponse{}, false
	}
	return domain.BracketResponse{
		Symbol:            sym,
		StopOrderID:       po.StopOrderID,
		TakeProfitOrderID: po.TakeProfitOrderID,
	}, true
}

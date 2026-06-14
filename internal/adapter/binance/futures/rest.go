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
	client  *gobinancefutures.Client
	mode    futuresMode
	filters *FuturesExchangeInfo

	mu            sync.RWMutex
	positions     map[domain.Symbol]domain.Position
	leverageCache map[domain.Symbol]int // last-applied leverage per symbol (avoids redundant REST calls)
}

// NewFuturesBroker creates a FuturesBroker.
func NewFuturesBroker(client *gobinancefutures.Client, mode string) *FuturesBroker {
	return &FuturesBroker{
		client:        client,
		mode:          futuresMode(mode),
		filters:       NewFuturesExchangeInfo(client),
		positions:     make(map[domain.Symbol]domain.Position),
		leverageCache: make(map[domain.Symbol]int),
	}
}

// ExchangeInfo exposes the filter store for callers that need to satisfy
// port.ExchangeInfoStore directly.
func (b *FuturesBroker) ExchangeInfo() *FuturesExchangeInfo { return b.filters }

// Venue identifies this broker endpoint.
func (b *FuturesBroker) Venue() domain.Venue { return domain.VenueBinanceFutures }

// Connect bootstraps positions once, then keeps them fresh via the private
// user-data websocket. It also preloads the exchangeInfo filter cache.
func (b *FuturesBroker) Connect(ctx context.Context) error {
	if err := b.filters.Refresh(ctx); err != nil {
		slog.Warn("futures: exchange info refresh failed on connect; orders will reject until recovered", "error", err)
	}
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

	// STOP_MARKET with closePosition=true.
	// Notes:
	//   - closePosition exits the full position regardless of our quantity
	//     field, which is what we want for a protective bracket.
	//   - reduceOnly is mutually exclusive with closePosition on the API;
	//     we intentionally omit reduceOnly here.
	//   - Hedge mode requires PositionSide; one-way mode requires BOTH.
	stopSvc := b.client.NewCreateOrderService().
		Symbol(sym).
		Side(exitSide).
		Type(gobinancefutures.OrderType("STOP_MARKET")).
		StopPrice(stopPx.String()).
		ClosePosition(true).
		WorkingType(gobinancefutures.WorkingTypeMarkPrice).
		PriceProtect(true).
		NewClientOrderID(stopCID)
	if req.PositionSide != "" {
		stopSvc = stopSvc.PositionSide(toFuturesPositionSide(req.PositionSide))
	}
	stopResp, err := stopSvc.Do(ctx)
	if err != nil {
		return domain.BracketResponse{}, fmt.Errorf("futures STOP_MARKET: %w", err)
	}
	br := domain.BracketResponse{
		StopOrderID: strconv.FormatInt(stopResp.OrderID, 10),
		Symbol:      req.Symbol,
	}

	// TAKE_PROFIT_MARKET (optional — only when caller provided a TP level).
	if !req.TakeProfit.IsZero() {
		tpPx := filter.QuantisePrice(req.TakeProfit, exitDomainSide)
		tpCID := shortFuturesClientID(tag+"T", req.ParentIntentID)
		tpSvc := b.client.NewCreateOrderService().
			Symbol(sym).
			Side(exitSide).
			Type(gobinancefutures.OrderType("TAKE_PROFIT_MARKET")).
			StopPrice(tpPx.String()).
			ClosePosition(true).
			WorkingType(gobinancefutures.WorkingTypeMarkPrice).
			PriceProtect(true).
			NewClientOrderID(tpCID)
		if req.PositionSide != "" {
			tpSvc = tpSvc.PositionSide(toFuturesPositionSide(req.PositionSide))
		}
		tpResp, tperr := tpSvc.Do(ctx)
		if tperr != nil {
			// Partial bracket: stop is live, TP failed. Surface the error
			// to the caller; they own the retry / unwind decision.
			slog.Error("futures: TP_MARKET leg failed; bracket left partial",
				"symbol", req.Symbol, "error", tperr,
				"stop_order_id", br.StopOrderID)
			return br, fmt.Errorf("futures TAKE_PROFIT_MARKET (stop placed OK): %w", tperr)
		}
		br.TakeProfitOrderID = strconv.FormatInt(tpResp.OrderID, 10)
	}

	return br, nil
}

// CancelBracket cancels both legs of a previously placed futures bracket.
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

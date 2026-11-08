package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	gobinance "github.com/adshao/go-binance/v2"
	gobinancefutures "github.com/adshao/go-binance/v2/futures"
	binanceadapter "github.com/azhar/cerebro/internal/adapter/binance"
	"github.com/azhar/cerebro/internal/adapter/binance/futures"
	"github.com/azhar/cerebro/internal/adapter/binance/spot"
	agenttools "github.com/azhar/cerebro/internal/agent/tools"
	"github.com/azhar/cerebro/internal/config"
	"github.com/azhar/cerebro/internal/domain"
	"github.com/azhar/cerebro/internal/execution"
	"github.com/azhar/cerebro/internal/marketdata"
	"github.com/azhar/cerebro/internal/port"
	"github.com/azhar/cerebro/internal/risk"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"golang.org/x/sync/errgroup"
)

// klineClients holds the Binance REST clients needed to fetch historical klines
// for strategy warmup. Only populated in live/demo mode.
type klineClients struct {
	spotClient    *gobinance.Client
	futuresClient *gobinancefutures.Client
}

// buildLiveBrokers creates broker instances for each active venue, using the
// appropriate credentials for the environment (demo, testnet, or live).
// Also returns klineClients for historical candle fetching.
func buildLiveBrokers(ctx context.Context, cfg *config.Config, env domain.Environment, venues []domain.Venue) (map[domain.Venue]port.Broker, []port.Broker, *klineClients, error) {
	brokers := make(map[domain.Venue]port.Broker, len(venues))
	unique := make([]port.Broker, 0, len(venues))
	var kc klineClients

	// Collect configured spot symbols and their min lot sizes for position/dust filtering.
	spotSymbols := collectSymbolsForVenue(cfg.Markets, domain.VenueBinanceSpot)
	spotMinLots := collectMinLotsForVenue(cfg.Markets, domain.VenueBinanceSpot)

	for _, venue := range venues {
		var broker port.Broker
		switch venue {
		case domain.VenueBinanceSpot:
			switch env {
			case domain.EnvironmentDemo:
				spotClient := binanceadapter.NewDemoSpotClient(
					cfg.Secrets.BinanceDemoAPIKey,
					cfg.Secrets.BinanceDemoAPISecret,
				)
				broker = spot.NewSpotBroker(spotClient, "demo", spotSymbols, spotMinLots)
				kc.spotClient = spotClient
			case domain.EnvironmentLive:
				isTestnet := cfg.Secrets.BinanceTestnetAPIKey != ""
				apiKey := cfg.Secrets.BinanceAPIKey
				apiSecret := cfg.Secrets.BinanceAPISecret
				if isTestnet {
					apiKey = cfg.Secrets.BinanceTestnetAPIKey
					apiSecret = cfg.Secrets.BinanceTestnetAPISecret
				}
				mode := "mainnet"
				if isTestnet {
					mode = "testnet"
				}
				spotClient := binanceadapter.NewSpotClient(apiKey, apiSecret, isTestnet)
				broker = spot.NewSpotBroker(spotClient, mode, spotSymbols, spotMinLots)
				kc.spotClient = spotClient
			}
		case domain.VenueBinanceFutures:
			switch env {
			case domain.EnvironmentDemo:
				key := cfg.Secrets.BinanceDemoFuturesAPIKey
				secret := cfg.Secrets.BinanceDemoFuturesAPISecret
				if key == "" {
					key = cfg.Secrets.BinanceFuturesTestnetAPIKey
					secret = cfg.Secrets.BinanceFuturesTestnetAPISecret
				}
				if key == "" || secret == "" {
					return nil, nil, nil, fmt.Errorf("demo futures broker requested but no BINANCE_DEMO_FUTURES_* or BINANCE_FUTURES_TESTNET_* credentials are set")
				}
				futClient := binanceadapter.NewDemoFuturesClient(key, secret)
				broker = futures.NewFuturesBroker(futClient, "demo")
				kc.futuresClient = futClient
			case domain.EnvironmentLive:
				isTestnet := cfg.Secrets.BinanceFuturesTestnetAPIKey != ""
				apiKey := cfg.Secrets.BinanceFuturesAPIKey
				apiSecret := cfg.Secrets.BinanceFuturesAPISecret
				if isTestnet {
					apiKey = cfg.Secrets.BinanceFuturesTestnetAPIKey
					apiSecret = cfg.Secrets.BinanceFuturesTestnetAPISecret
				}
				if apiKey == "" || apiSecret == "" {
					return nil, nil, nil, fmt.Errorf("live futures broker requested but BINANCE_FUTURES_API_KEY / BINANCE_FUTURES_API_SECRET are not set")
				}
				mode := "mainnet"
				if isTestnet {
					mode = "testnet"
				}
				futClient := binanceadapter.NewFuturesClient(apiKey, apiSecret, isTestnet)
				broker = futures.NewFuturesBroker(futClient, mode)
				kc.futuresClient = futClient
			}
		default:
			return nil, nil, nil, fmt.Errorf("unsupported venue %q", venue)
		}

		if broker == nil {
			return nil, nil, nil, fmt.Errorf("failed to wire broker for venue %s", venue)
		}
		// Apply the configured position-resync cadence (zero leaves the broker
		// default in effect). Brokers ignore non-positive overrides.
		if ms := cfg.Engine.PositionResyncIntervalMS; ms > 0 {
			resync := time.Duration(ms) * time.Millisecond
			switch b := broker.(type) {
			case *spot.SpotBroker:
				b.SetResyncInterval(resync)
			case *futures.FuturesBroker:
				b.SetResyncInterval(resync)
			}
		}
		if err := broker.Connect(ctx); err != nil {
			return nil, nil, nil, fmt.Errorf("%s broker connect: %w", venue, err)
		}
		brokers[venue] = broker
		unique = append(unique, broker)
		slog.Info("venue broker wired", "venue", venue)
	}

	return brokers, unique, &kc, nil
}

// klineConnectors holds references to active WebSocket kline streams so they
// can be dynamically updated with new symbols from discovery candidates.
type klineConnectors struct {
	mu         sync.Mutex
	spotKlines map[domain.Timeframe]*spot.KlinesWS
	futKlines  map[domain.Timeframe]*futures.KlinesWS
}

// newKlineConnectors creates an empty connector registry.
func newKlineConnectors() *klineConnectors {
	return &klineConnectors{
		spotKlines: make(map[domain.Timeframe]*spot.KlinesWS),
		futKlines:  make(map[domain.Timeframe]*futures.KlinesWS),
	}
}

// updateSpotSymbols updates all spot kline streams with the given symbols.
func (kc *klineConnectors) updateSpotSymbols(symbols []domain.Symbol) {
	kc.mu.Lock()
	defer kc.mu.Unlock()
	for tf, ws := range kc.spotKlines {
		slog.Debug("updating spot klines symbols", "timeframe", tf, "count", len(symbols))
		ws.UpdateSymbols(symbols)
	}
}

// updateFuturesSymbols updates all futures kline streams with the given symbols.
func (kc *klineConnectors) updateFuturesSymbols(symbols []domain.Symbol) {
	kc.mu.Lock()
	defer kc.mu.Unlock()
	for tf, ws := range kc.futKlines {
		slog.Debug("updating futures klines symbols", "timeframe", tf, "count", len(symbols))
		ws.UpdateSymbols(symbols)
	}
}

// spawnLiveKlinesWS starts one Binance WebSocket kline stream per
// (venue, timeframe) pair found in markets config, publishing closed candles
// to the hub.
// Also spawns bookTicker and 24hr ticker streams for real-time bid/ask and
// change/volume data used by the TUI market watch panel.
//
// Returns a klineConnectors registry for dynamic symbol updates (or nil if no streams created).
func spawnLiveKlinesWS(
	g *errgroup.Group,
	ctx context.Context,
	cfg *config.Config,
	hub *marketdata.Hub,
) *klineConnectors {
	connectors := newKlineConnectors()
	for _, venueCfg := range cfg.Markets {
		tfSymbols := groupSymbolsByTimeframe(venueCfg)
		allSymbols := collectEnabledSymbolsFromVenue(venueCfg)
		if len(allSymbols) == 0 {
			continue
		}

		switch venueCfg.Venue {
		case domain.VenueBinanceSpot:
			for tf, syms := range tfSymbols {
				tf, syms := tf, syms
				ws := spot.NewKlinesWS(hub, syms, tf, nil)
				connectors.mu.Lock()
				connectors.spotKlines[tf] = ws
				connectors.mu.Unlock()
				g.Go(func() error {
					if err := ws.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
						return fmt.Errorf("spot klines WS %s: %w", tf, err)
					}
					return nil
				})
			}

			btWS := spot.NewBookTickerWS(hub, allSymbols, nil)
			g.Go(func() error {
				if err := btWS.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
					return fmt.Errorf("spot bookTicker WS: %w", err)
				}
				return nil
			})

			tickerWS := spot.NewTickerWS(hub, allSymbols, nil)
			g.Go(func() error {
				if err := tickerWS.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
					return fmt.Errorf("spot 24hr ticker WS: %w", err)
				}
				return nil
			})

		case domain.VenueBinanceFutures:
			for tf, syms := range tfSymbols {
				tf, syms := tf, syms
				ws := futures.NewKlinesWS(hub, syms, tf, nil)
				connectors.mu.Lock()
				connectors.futKlines[tf] = ws
				connectors.mu.Unlock()
				g.Go(func() error {
					if err := ws.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
						return fmt.Errorf("futures klines WS %s: %w", tf, err)
					}
					return nil
				})
			}

			btWS := futures.NewBookTickerWS(hub, allSymbols, nil)
			g.Go(func() error {
				if err := btWS.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
					return fmt.Errorf("futures bookTicker WS: %w", err)
				}
				return nil
			})

			tickerWS := futures.NewTickerWS(hub, allSymbols, nil)
			g.Go(func() error {
				if err := tickerWS.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
					return fmt.Errorf("futures 24hr ticker WS: %w", err)
				}
				return nil
			})

		default:
			slog.Warn("unknown venue in markets config; skipping live WS", "venue", venueCfg.Venue)
		}
	}
	return connectors
}

// collectEnabledSymbolsFromVenue returns all enabled symbols for a single venue config.
func collectEnabledSymbolsFromVenue(vc config.VenueConfig) []domain.Symbol {
	var syms []domain.Symbol
	for _, s := range vc.Symbols {
		if s.Enabled {
			syms = append(syms, domain.Symbol(s.Symbol))
		}
	}
	return syms
}

// groupSymbolsByTimeframe groups enabled symbols from a single venue config by
// their configured timeframes. Used to create one WS stream per timeframe.
func groupSymbolsByTimeframe(vc config.VenueConfig) map[domain.Timeframe][]domain.Symbol {
	out := make(map[domain.Timeframe][]domain.Symbol)
	for _, s := range vc.Symbols {
		if !s.Enabled {
			continue
		}
		tfs := s.Timeframes
		if len(tfs) == 0 {
			tfs = []domain.Timeframe{domain.TF1m}
		}
		for _, tf := range tfs {
			out[tf] = append(out[tf], domain.Symbol(s.Symbol))
		}
	}
	return out
}

func collectPositions(ctx context.Context, brokers map[domain.Venue]port.Broker) []domain.Position {
	var out []domain.Position
	for venue, broker := range brokers {
		out = append(out, positionsForVenue(ctx, broker, venue)...)
	}
	return out
}

func positionsForVenue(ctx context.Context, broker port.Broker, venue domain.Venue) []domain.Position {
	positions, err := broker.Positions(ctx)
	if err != nil {
		slog.Warn("positions fetch failed", "venue", venue, "error", err)
		return nil
	}

	filtered := make([]domain.Position, 0, len(positions))
	for _, p := range positions {
		if p.Venue == "" {
			p.Venue = venue
		}
		if p.Venue == venue {
			filtered = append(filtered, p)
		}
	}
	return filtered
}

func countPositionsByVenue(positions []domain.Position, venue domain.Venue) int {
	n := 0
	for _, p := range positions {
		if p.Venue == venue {
			n++
		}
	}
	return n
}

// buildRouteOrderFn returns the function the agent tools invoke when the
// risk agent decides to route an order. The caller supplies an
// AgentOrderRequest carrying every optional field (OrderType, LimitPrice,
// StopPrice, TIF, StopLoss, TakeProfit1, ScaleOutPct, ReduceOnly,
// PositionSide, Leverage). Zero values fall back to the deterministic
// defaults:
//   - OrderType defaults to MARKET
//   - TIF defaults to GTC
//   - Leverage on futures inherits meta.cfg.Leverage
//   - When StopLoss is set, the Worker attaches a broker-side bracket
//     (OCO on spot, STOP_MARKET + TAKE_PROFIT_MARKET on futures).
//   - When StopLoss is zero on a non-reduce-only entry, the safety net
//     derives SL/TP from cfg.RiskAgent.{DefaultSLPct,DefaultTPPct} applied
//     to the live mark price (hub.LatestQuote). This closes the
//     "no SL/TP levels to attach" flatten race the reconciler would
//     otherwise force-close within the 30s grace window. Reduce-only
//     (position-exit) orders are intentionally untouched.
func buildRouteOrderFn(
	router *execution.Router,
	gate *risk.Gate,
	brokers map[domain.Venue]port.Broker,
	symbolMeta map[domain.Symbol]symbolMeta,
	env domain.Environment,
	uiSink multiSink,
	cfg config.RiskAgentConfig,
	hub *marketdata.Hub,
) func(ctx context.Context, req agenttools.AgentOrderRequest) error {
	return func(ctx context.Context, req agenttools.AgentOrderRequest) error {
		meta, resolved, err := resolveRouteSymbol(req.Symbol, symbolMeta)
		if err != nil {
			return err
		}

		// ── Risk gate: single chokepoint for the AGENT entry path ───────────
		// The agent (approve_and_route_order / resize_and_route_order) reaches
		// the broker through this function only. The strategy path gates at
		// strategies.go *before* routing through the same Router/Worker, and
		// the Router/Worker themselves are intentionally NOT gated — so the
		// check here is the agent path's one and only gate and does NOT
		// double-gate strategy orders.
		//
		// Reduce-only orders are position EXITS: they must never be blocked on
		// position / notional / drawdown limits (same rule as ChatOps /close),
		// so they skip the gate here only on Binance Futures. Spot does not
		// support reduce-only, so reject it to prevent bypassing the risk gate.
		if req.ReduceOnly && meta.venue == domain.VenueBinanceSpot {
			return fmt.Errorf("reduce-only orders are not supported on spot")
		}
		if !(req.ReduceOnly && meta.venue == domain.VenueBinanceFutures) {
			sig := domain.Signal{
				CorrelationID: req.CorrelationID,
				Strategy:      domain.StrategyName("risk_agent"),
				Symbol:        resolved,
				Side:          req.Side,
				GeneratedAt:   time.Now().UTC(),
			}
			positions := collectPositions(ctx, brokers)
			if gerr := gate.Check(ctx, sig, positions); gerr != nil {
				slog.Warn("✗ agent order rejected by risk gate",
					"symbol", resolved, "side", req.Side, "reason", gerr)
				pushTUIOrder(uiSink, fmt.Sprintf("✗ RISK-REJECT(agent) %s %s — %v",
					resolved, req.Side, gerr))
				return fmt.Errorf("risk gate: %w", gerr)
			}
		}

		qty := decimal.NewFromFloat(req.Size)
		if !meta.cfg.LotSize.IsZero() {
			qty = qty.Div(meta.cfg.LotSize).Floor().Mul(meta.cfg.LotSize)
		}

		orderType := req.OrderType
		if orderType == "" {
			orderType = domain.OrderTypeMarket
		}
		tif := req.TIF
		if tif == "" {
			tif = domain.TIFGTC
		}
		leverage := req.Leverage
		if leverage == 0 && meta.venue == domain.VenueBinanceFutures {
			leverage = meta.cfg.Leverage
		}

		// ── SL/TP safety net ─────────────────────────────────────────────
		// If the LLM approved the order without supplying stop_loss, derive
		// it (and take_profit) from the live mark price and the configured
		// defaults. Without this the reconciler would flatten the entry as
		// "no SL/TP levels to attach" within the 30s grace window.
		stopLoss := req.StopLoss
		takeProfit := req.TakeProfit1
		if !req.ReduceOnly && stopLoss.IsZero() && (cfg.DefaultSLPct > 0 || cfg.DefaultTPPct > 0) {
			derivedSL, derivedTP, derived := deriveSafetyBracket(resolved, req.Side, hub, cfg)
			if derived {
				stopLoss = derivedSL
				takeProfit = derivedTP
				slog.Warn("agent order approved without stop_loss; applied safety bracket from mark price",
					"symbol", resolved, "side", req.Side,
					"stop_loss", stopLoss.String(), "take_profit", takeProfit.String(),
					"sl_pct", cfg.DefaultSLPct, "tp_pct", cfg.DefaultTPPct)
			} else {
				slog.Warn("agent order approved without stop_loss and no live quote available; reconciler will flatten",
					"symbol", resolved, "side", req.Side)
			}
		}

		intent := domain.OrderIntent{
			ID:            uuid.New().String(),
			CorrelationID: req.CorrelationID,
			Symbol:        resolved,
			Venue:         meta.venue,
			Side:          req.Side,
			OrderType:     orderType,
			Quantity:      qty,
			LimitPrice:    req.LimitPrice,
			StopPrice:     req.StopPrice,
			TIF:           tif,
			StopLoss:      stopLoss,
			TakeProfit1:   takeProfit,
			ScaleOutPct:   req.ScaleOutPct,
			ReduceOnly:    req.ReduceOnly,
			PositionSide:  req.PositionSide,
			Leverage:      leverage,
			Strategy:      domain.StrategyName("risk_agent"),
			Environment:   env,
			CreatedAt:     time.Now().UTC(),
		}
		if _, err := router.Route(ctx, intent, meta.venue); err != nil {
			return err
		}

		// TUI summary line. Include the order type and bracket info when
		// present so operators can see what the agent actually did.
		summary := fmt.Sprintf("AGENT %s %s %s %.6f {%s}",
			orderType, req.Side, resolved, req.Size, meta.venue)
		if !stopLoss.IsZero() {
			summary += fmt.Sprintf(" SL=%s", stopLoss.StringFixed(4))
		}
		if !takeProfit.IsZero() {
			summary += fmt.Sprintf(" TP=%s", takeProfit.StringFixed(4))
		}
		// Flag safety-net-derived brackets so the operator sees the LLM
		// omitted them. The "(safety)" tag is appended AFTER the SL/TP fields
		// so existing parser-friendly layouts stay intact.
		if !req.StopLoss.IsZero() {
			// nothing to flag — LLM supplied the bracket
		} else if !stopLoss.IsZero() {
			summary += " (safety)"
		}
		pushTUIOrder(uiSink, summary)
		return nil
	}
}

// deriveSafetyBracket returns a (sl, tp, derived) triple anchored on the
// live mark price for sym when one is available. Returns derived=false when
// the hub is nil, the quote is missing, or the mark price is zero — in which
// case the caller logs and lets the request through with zero bracket so the
// reconciler's flatten-grace path is the only safety net (mirrors behaviour
// before this helper existed).
func deriveSafetyBracket(
	sym domain.Symbol,
	side domain.Side,
	hub *marketdata.Hub,
	cfg config.RiskAgentConfig,
) (decimal.Decimal, decimal.Decimal, bool) {
	if hub == nil {
		return decimal.Zero, decimal.Zero, false
	}
	q, ok := hub.LatestQuote(sym)
	if !ok {
		return decimal.Zero, decimal.Zero, false
	}
	mark := q.Last
	if mark.IsZero() {
		mark = q.Mid
	}
	if mark.IsZero() {
		return decimal.Zero, decimal.Zero, false
	}
	var sl, tp decimal.Decimal
	if cfg.DefaultSLPct > 0 {
		// Stop loss: BUY → below entry, SELL → above entry.
		sl = applyBracketOffset(mark, cfg.DefaultSLPct, side == domain.SideSell)
	}
	if cfg.DefaultTPPct > 0 {
		// Take profit: BUY → above entry, SELL → below entry.
		tp = applyBracketOffset(mark, cfg.DefaultTPPct, side == domain.SideBuy)
	}
	if sl.IsZero() && tp.IsZero() {
		return decimal.Zero, decimal.Zero, false
	}
	return sl, tp, true
}

// applyBracketOffset shifts price by pct% above (above=true) or below
// (above=false) the mark price. Used for both SL and TP derivation:
//   - BUY stop loss:  above=false → mark × (1 - pct/100)
//   - SELL stop loss: above=true  → mark × (1 + pct/100)
//   - BUY take profit: above=true  → mark × (1 + pct/100)
//   - SELL take profit: above=false → mark × (1 - pct/100)
func applyBracketOffset(price decimal.Decimal, pct float64, above bool) decimal.Decimal {
	factor := decimal.NewFromFloat(pct / 100)
	delta := price.Mul(factor)
	if above {
		return price.Add(delta)
	}
	return price.Sub(delta)
}

// buildCloseFn returns a ClosePositionFn the chatops dispatcher can use to
// close a single position via the execution router. The close is a
// reduce-only MARKET order for the full position quantity and goes through
// the normal Router → Worker → broker path, which preserves the single-
// writer invariant and captures the close in the audit trail.
//
// Brackets attached to the position are intentionally NOT cancelled here —
// the server-side OCO or STOP_MARKET + TAKE_PROFIT_MARKET pair will fire
// naturally when the market-close reduces the position to zero, or can be
// cleaned up by the next periodic reconcile.
func buildCloseFn(
	router *execution.Router,
	env domain.Environment,
) func(ctx context.Context, pos domain.Position) (string, error) {
	return func(ctx context.Context, pos domain.Position) (string, error) {
		if pos.Symbol == "" {
			return "", fmt.Errorf("close: position has no symbol")
		}
		if pos.Quantity.IsZero() {
			return "", fmt.Errorf("close: position has zero quantity")
		}

		closeSide := domain.SideSell
		if pos.Side == domain.SideSell {
			closeSide = domain.SideBuy
		}

		intent := domain.OrderIntent{
			ID:            uuid.New().String(),
			CorrelationID: pos.CorrelationID,
			Symbol:        pos.Symbol,
			Venue:         pos.Venue,
			Side:          closeSide,
			OrderType:     domain.OrderTypeMarket,
			Quantity:      pos.Quantity,
			Strategy:      domain.StrategyName("chatops_close"),
			Environment:   env,
			CreatedAt:     time.Now().UTC(),
			// ReduceOnly is honoured on futures; it's a safety-net on spot
			// because spot doesn't over-fill anyway. It prevents a race
			// where the server-side bracket fires between our positions
			// snapshot and this close submission.
			ReduceOnly: true,
		}
		resp, err := router.Route(ctx, intent, pos.Venue)
		if err != nil {
			return "", err
		}
		return resp.BrokerOrderID, nil
	}
}

func resolveRouteSymbol(symbol domain.Symbol, symbolIndex map[domain.Symbol]symbolMeta) (symbolMeta, domain.Symbol, error) {
	if meta, ok := symbolIndex[symbol]; ok {
		return meta, symbol, nil
	}

	candidates := make(map[domain.Symbol]symbolMeta)
	for _, contractType := range []domain.ContractType{domain.ContractSpot, domain.ContractFuturesPerp} {
		canonical, err := domain.NormalizeExchangeSymbol(string(symbol), contractType)
		if err == nil {
			if meta, ok := symbolIndex[canonical]; ok {
				candidates[canonical] = meta
			}
		}
	}
	if len(candidates) == 1 {
		for sym, meta := range candidates {
			return meta, sym, nil
		}
	}
	if len(candidates) > 1 {
		return symbolMeta{}, "", fmt.Errorf("ambiguous symbol %q; use canonical internal symbol", symbol)
	}
	return symbolMeta{}, "", fmt.Errorf("unknown symbol %q", symbol)
}

// collectSymbolsForVenue returns all enabled symbols for a specific venue as canonical domain symbols.
func collectSymbolsForVenue(venues []config.VenueConfig, target domain.Venue) []domain.Symbol {
	var syms []domain.Symbol
	for _, vc := range venues {
		if vc.Venue != target {
			continue
		}
		for _, s := range vc.Symbols {
			if s.Enabled {
				syms = append(syms, domain.Symbol(s.Symbol))
			}
		}
	}
	return syms
}

// collectMinLotsForVenue returns a map of symbol → minimum lot size for dust filtering.
func collectMinLotsForVenue(venues []config.VenueConfig, target domain.Venue) map[domain.Symbol]decimal.Decimal {
	out := make(map[domain.Symbol]decimal.Decimal)
	for _, vc := range venues {
		if vc.Venue != target {
			continue
		}
		for _, s := range vc.Symbols {
			if s.Enabled && !s.LotSize.IsZero() {
				out[domain.Symbol(s.Symbol)] = s.LotSize
			}
		}
	}
	return out
}

// noopProtectiveLookup satisfies positionproposal.ProtectiveLookup for brokers
// that don't expose exchange-side protective orders (the paper matcher). It
// reports no existing protection, so ApplyAdjustment simply places a fresh
// bracket without a preceding cancel.
type noopProtectiveLookup struct{}

func (noopProtectiveLookup) ProtectiveBracket(domain.Symbol) (domain.BracketResponse, bool) {
	return domain.BracketResponse{}, false
}

package app

import (
	"context"
	"testing"
	"time"

	"github.com/azhar/cerebro/internal/agent/tools"
	"github.com/azhar/cerebro/internal/config"
	"github.com/azhar/cerebro/internal/domain"
	"github.com/azhar/cerebro/internal/execution"
	"github.com/azhar/cerebro/internal/marketdata"
	"github.com/azhar/cerebro/internal/port"
	"github.com/azhar/cerebro/internal/risk"
	"github.com/shopspring/decimal"
)

// noopCache satisfies port.Cache with no-op methods for testing.
type noopCache struct{}

func (noopCache) Set(_ context.Context, _ string, _ []byte, _ time.Duration) error { return nil }
func (noopCache) Get(_ context.Context, _ string) ([]byte, error)                  { return nil, nil }
func (noopCache) Delete(_ context.Context, _ string) error                         { return nil }
func (noopCache) IncrBy(_ context.Context, _ string, _ int64, _ time.Duration) (int64, error) {
	return 0, nil
}
func (noopCache) Keys(_ context.Context, _ string) ([]string, error) { return nil, nil }
func (noopCache) Exists(_ context.Context, _ string) (bool, error)   { return false, nil }
func (noopCache) SetNX(_ context.Context, _ string, _ []byte, _ time.Duration) (bool, error) { return true, nil }

// stubBroker implements port.Broker for testing the route function.
type stubBroker struct {
	venue domain.Venue
}

func (s *stubBroker) Venue() domain.Venue { return s.venue }

func (s *stubBroker) Connect(ctx context.Context) error { return nil }

func (s *stubBroker) StreamQuotes(ctx context.Context, symbols []domain.Symbol) (<-chan domain.Quote, error) {
	return nil, nil
}

func (s *stubBroker) PlaceOrder(ctx context.Context, intent domain.OrderIntent) (string, error) {
	return "stub-order-id", nil
}

func (s *stubBroker) PlaceBracket(ctx context.Context, req domain.BracketRequest) (domain.BracketResponse, error) {
	return domain.BracketResponse{
		StopOrderID:       "stub-sl-id",
		TakeProfitOrderID: "stub-tp-id",
		Symbol:            req.Symbol,
	}, nil
}

func (s *stubBroker) CancelOrder(ctx context.Context, req domain.CancelRequest) error {
	return nil
}

func (s *stubBroker) CancelBracket(ctx context.Context, resp domain.BracketResponse) error {
	return nil
}

func (s *stubBroker) Positions(ctx context.Context) ([]domain.Position, error) {
	return nil, nil
}

func (s *stubBroker) Balance(ctx context.Context) (port.AccountBalance, error) {
	return port.AccountBalance{}, nil
}

func TestBuildRouteOrderFn_SafetyBracket(t *testing.T) {
	ctx := context.Background()
	brokers := map[domain.Venue]port.Broker{
		domain.VenueBinanceFutures: &stubBroker{venue: domain.VenueBinanceFutures},
	}

	hub := marketdata.NewHub()

	// Publish a quote for BTC-USDT at 100,000
	hub.PublishQuote(domain.Quote{
		Symbol: "BTC-USDT",
		Last:   decimal.NewFromFloat(100000.0),
	})

	riskGate := risk.NewGate(config.RiskConfig{
		MaxExposurePct:            10.0,
		MaxOpenPositionsPerSymbol: 3,
		MaxOpenPositions:          10,
	}, noopCache{}, risk.NewCalendarBlackout())

	router := execution.NewRouter([]domain.Venue{domain.VenueBinanceFutures})
	defer router.Close()

	symbolCfg := &config.SymbolConfig{
		LotSize:  decimal.NewFromFloat(0.001),
		Leverage: 1,
	}

	symbolMeta := map[domain.Symbol]symbolMeta{
		"BTC-USDT": {
			venue: domain.VenueBinanceFutures,
			cfg:   symbolCfg,
		},
	}

	riskAgentCfg := config.RiskAgentConfig{
		DefaultSLPct: 0.5, // 0.5% SL
		DefaultTPPct: 0.8, // 0.8% TP
	}

	routeFn := buildRouteOrderFn(
		router,
		riskGate,
		brokers,
		symbolMeta,
		domain.EnvironmentLive,
		nil, // multiSink can be nil
		riskAgentCfg,
		hub,
	)

	t.Run("fallback applies when StopLoss is zero", func(t *testing.T) {
		ch, ok := router.Channel(domain.VenueBinanceFutures)
		if !ok {
			t.Fatal("no worker channel for futures venue")
		}

		var routedIntent domain.OrderIntent
		done := make(chan struct{})
		go func() {
			defer close(done)
			for req := range ch {
				routedIntent = req.Intent
				req.RespCh <- execution.OrderResponse{BrokerOrderID: "test-order"}
				break
			}
		}()

		// Route a BUY order without StopLoss (should apply fallback)
		req := tools.AgentOrderRequest{
			Symbol:    "BTC-USDT",
			Side:      domain.SideBuy,
			Size:      0.001,
			OrderType: domain.OrderTypeMarket,
		}
		err := routeFn(ctx, req)
		if err != nil {
			t.Fatalf("routeFn failed: %v", err)
		}

		<-done

		// Verify: StopLoss should be BELOW entry for BUY (100000 * 0.995 = 99500)
		expectedSL := decimal.NewFromFloat(99500.0)
		if !routedIntent.StopLoss.Equal(expectedSL) {
			t.Errorf("StopLoss = %s, want %s (buy side SL should be below entry)", routedIntent.StopLoss, expectedSL)
		}

		// Verify: TakeProfit should be ABOVE entry for BUY (100000 * 1.008 = 100800)
		expectedTP := decimal.NewFromFloat(100800.0)
		if !routedIntent.TakeProfit1.Equal(expectedTP) {
			t.Errorf("TakeProfit1 = %s, want %s (buy side TP should be above entry)", routedIntent.TakeProfit1, expectedTP)
		}
	})

	t.Run("explicit StopLoss is preserved", func(t *testing.T) {
		ch, ok := router.Channel(domain.VenueBinanceFutures)
		if !ok {
			t.Fatal("no worker channel for futures venue")
		}

		var routedIntent domain.OrderIntent
		done := make(chan struct{})
		go func() {
			defer close(done)
			for req := range ch {
				routedIntent = req.Intent
				req.RespCh <- execution.OrderResponse{BrokerOrderID: "test-order"}
				break
			}
		}()

		// Route a BUY order WITH explicit StopLoss (should NOT apply fallback)
		explicitSL := decimal.NewFromFloat(98000.0)
		explicitTP := decimal.NewFromFloat(105000.0)
		req := tools.AgentOrderRequest{
			Symbol:      "BTC-USDT",
			Side:        domain.SideBuy,
			Size:        0.001,
			OrderType:   domain.OrderTypeMarket,
			StopLoss:    explicitSL,
			TakeProfit1: explicitTP,
		}
		err := routeFn(ctx, req)
		if err != nil {
			t.Fatalf("routeFn failed: %v", err)
		}

		<-done

		// Verify: explicit values are preserved
		if !routedIntent.StopLoss.Equal(explicitSL) {
			t.Errorf("StopLoss = %s, want %s (explicit SL should be preserved)", routedIntent.StopLoss, explicitSL)
		}

		if !routedIntent.TakeProfit1.Equal(explicitTP) {
			t.Errorf("TakeProfit1 = %s, want %s (explicit TP should be preserved)", routedIntent.TakeProfit1, explicitTP)
		}
	})

	t.Run("fallback skipped when hub has no quote", func(t *testing.T) {
		emptyHub := marketdata.NewHub() // no quotes

		emptyRouteFn := buildRouteOrderFn(
			router,
			riskGate,
			brokers,
			symbolMeta,
			domain.EnvironmentLive,
			nil,
			riskAgentCfg,
			emptyHub,
		)

		ch, ok := router.Channel(domain.VenueBinanceFutures)
		if !ok {
			t.Fatal("no worker channel for futures venue")
		}

		var routedIntent domain.OrderIntent
		done := make(chan struct{})
		go func() {
			defer close(done)
			for req := range ch {
				routedIntent = req.Intent
				req.RespCh <- execution.OrderResponse{BrokerOrderID: "test-order"}
				break
			}
		}()

		// Route a BUY order without StopLoss (hub has no quote, should NOT apply fallback)
		req := tools.AgentOrderRequest{
			Symbol:    "BTC-USDT",
			Side:      domain.SideBuy,
			Size:      0.001,
			OrderType: domain.OrderTypeMarket,
		}
		err := emptyRouteFn(ctx, req)
		if err != nil {
			t.Fatalf("routeFn failed: %v", err)
		}

		<-done

		// Verify: StopLoss should remain zero (no quote available)
		if !routedIntent.StopLoss.IsZero() {
			t.Errorf("StopLoss = %s, want zero (fallback should not apply when hub has no quote)", routedIntent.StopLoss)
		}
	})

	t.Run("sell side bracket is inverted", func(t *testing.T) {
		ch, ok := router.Channel(domain.VenueBinanceFutures)
		if !ok {
			t.Fatal("no worker channel for futures venue")
		}

		var routedIntent domain.OrderIntent
		done := make(chan struct{})
		go func() {
			defer close(done)
			for req := range ch {
				routedIntent = req.Intent
				req.RespCh <- execution.OrderResponse{BrokerOrderID: "test-order"}
				break
			}
		}()

		// Route a SELL order without StopLoss (should apply inverted fallback)
		req := tools.AgentOrderRequest{
			Symbol:    "BTC-USDT",
			Side:      domain.SideSell,
			Size:      0.001,
			OrderType: domain.OrderTypeMarket,
		}
		err := routeFn(ctx, req)
		if err != nil {
			t.Fatalf("routeFn failed: %v", err)
		}

		<-done

		// Verify: StopLoss should be ABOVE entry for SELL (100000 * 1.005 = 100500)
		expectedSL := decimal.NewFromFloat(100500.0)
		if !routedIntent.StopLoss.Equal(expectedSL) {
			t.Errorf("StopLoss = %s, want %s (sell side SL should be above entry)", routedIntent.StopLoss, expectedSL)
		}

		// Verify: TakeProfit should be BELOW entry for SELL (100000 * 0.992 = 99200)
		expectedTP := decimal.NewFromFloat(99200.0)
		if !routedIntent.TakeProfit1.Equal(expectedTP) {
			t.Errorf("TakeProfit1 = %s, want %s (sell side TP should be below entry)", routedIntent.TakeProfit1, expectedTP)
		}
	})
}

func TestBuildRouteOrderFn_SpotReduceOnlyRejected(t *testing.T) {
	ctx := context.Background()
	brokers := map[domain.Venue]port.Broker{
		domain.VenueBinanceSpot: &stubBroker{venue: domain.VenueBinanceSpot},
	}
	hub := marketdata.NewHub()
	riskGate := risk.NewGate(config.RiskConfig{}, noopCache{}, risk.NewCalendarBlackout())
	router := execution.NewRouter([]domain.Venue{domain.VenueBinanceSpot})
	defer router.Close()

	symbolMeta := map[domain.Symbol]symbolMeta{
		"BTC-USDT": {
			venue: domain.VenueBinanceSpot,
			cfg:   &config.SymbolConfig{LotSize: decimal.NewFromFloat(0.001)},
		},
	}

	routeFn := buildRouteOrderFn(
		router,
		riskGate,
		brokers,
		symbolMeta,
		domain.EnvironmentLive,
		nil,
		config.RiskAgentConfig{},
		hub,
	)

	req := tools.AgentOrderRequest{
		Symbol:     "BTC-USDT",
		Side:       domain.SideSell,
		Size:       0.001,
		ReduceOnly: true,
	}
	err := routeFn(ctx, req)
	if err == nil {
		t.Fatal("expected error when ReduceOnly=true on Spot, got nil")
	}
}

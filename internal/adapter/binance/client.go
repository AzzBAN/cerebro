package binance

import (
	"github.com/adshao/go-binance/v2"
	"github.com/adshao/go-binance/v2/futures"
)

// Spot Demo REST endpoint (launched 2026-01-29 per Binance changelog).
// Orders are routed to a virtual matching engine that executes at real market
// prices. API keys are provisioned at https://demo.binance.com.
//
// Futures Demo: Binance does not publish a separate USDT-M demo endpoint
// analogous to demo-api.binance.com. The library (go-binance v2) maps
// futures "demo" to the testnet (https://testnet.binancefuture.com), which
// has its own virtual balances and simulated (not live) prices. Use the
// testnet URL below when targeting futures in demo mode; accept that futures
// prices will not mirror mainnet. Real futures order execution requires the
// live environment.
//
// WebSocket market data is intentionally NOT overridden for either venue —
// kline streams always connect to mainnet (stream.binance.com /
// fstream.binance.com/market) so candle data reflects live prices.
// Note: go-binance v2.8.11 already uses the new /market/stream WS path for
// futures, which is required after the 2026-04-23 legacy-URL decommission.
const (
	// DemoSpotBaseURL is the Spot Demo REST endpoint launched 2026-01-29.
	DemoSpotBaseURL = "https://demo-api.binance.com"

	// DemoFuturesBaseURL points to the Binance Futures testnet.
	// Binance has no public USDT-M demo endpoint analogous to the Spot demo;
	// the testnet is the closest sandbox available. Prices are simulated, not
	// live — futures demo execution will not reflect real market conditions.
	DemoFuturesBaseURL = "https://testnet.binancefuture.com"
)

// NewSpotClient builds a Binance Spot REST client.
// When testnet=true it points to testnet.binance.vision.
func NewSpotClient(apiKey, secret string, testnet bool) *binance.Client {
	if testnet {
		binance.UseTestnet = true
	}
	return binance.NewClient(apiKey, secret)
}

// NewFuturesClient builds a Binance USDT-M Futures REST client.
// When testnet=true it points to testnet.binancefuture.com.
func NewFuturesClient(apiKey, secret string, testnet bool) *futures.Client {
	if testnet {
		futures.UseTestnet = true
	}
	return futures.NewClient(apiKey, secret)
}

// NewDemoSpotClient builds a Binance Spot REST client pointed at the Spot Demo
// Trading endpoint (demo-api.binance.com). We deliberately do NOT set the
// global binance.UseDemo flag to avoid redirecting WS kline streams to
// demo-stream.binance.com — we want real mainnet candle data for strategy
// evaluation.
func NewDemoSpotClient(apiKey, secret string) *binance.Client {
	c := binance.NewClient(apiKey, secret)
	c.BaseURL = DemoSpotBaseURL
	return c
}

// NewDemoFuturesClient builds a Binance USDT-M Futures REST client pointed at
// the futures testnet (https://testnet.binancefuture.com), which is the closest
// available sandbox for futures order execution. Prices on the testnet are
// simulated and will differ from mainnet. WS kline streams still use mainnet
// because we do not set futures.UseTestnet globally.
func NewDemoFuturesClient(apiKey, secret string) *futures.Client {
	c := futures.NewClient(apiKey, secret)
	c.BaseURL = DemoFuturesBaseURL
	return c
}

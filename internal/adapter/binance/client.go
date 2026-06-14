package binance

import (
	"github.com/adshao/go-binance/v2"
	"github.com/adshao/go-binance/v2/futures"
)

// Demo Trading REST endpoints (Spot + Futures).
// Orders are routed to a virtual matching engine that executes at real market
// prices. API keys are provisioned at https://demo.binance.com.
//
// Spot  Demo: https://demo-api.binance.com   (launched 2026-01-29)
// Futures Demo: https://demo-fapi.binance.com (USDT-M perpetuals)
//
// WebSocket market data is intentionally NOT overridden for either venue —
// kline streams always connect to mainnet (stream.binance.com /
// fstream.binance.com/market) so candle data reflects live prices.
// Note: go-binance v2.8.11 already uses the new /market/stream WS path for
// futures, which is required after the 2026-04-23 legacy-URL decommission.
const (
	// DemoSpotBaseURL is the Spot Demo REST endpoint launched 2026-01-29.
	DemoSpotBaseURL = "https://demo-api.binance.com"

	// DemoFuturesBaseURL is the USDT-M Futures Demo REST endpoint.
	// Like the Spot demo, orders route to a virtual matching engine with
	// real market prices. API keys are provisioned at https://demo.binance.com.
	DemoFuturesBaseURL = "https://demo-fapi.binance.com"
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
// the Futures Demo endpoint (https://demo-fapi.binance.com). Orders execute
// against real market prices in a virtual matching engine. WS kline streams
// still use mainnet because we do not set futures.UseTestnet globally.
func NewDemoFuturesClient(apiKey, secret string) *futures.Client {
	c := futures.NewClient(apiKey, secret)
	c.BaseURL = DemoFuturesBaseURL
	return c
}

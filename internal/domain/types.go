package domain

// Environment controls paper vs demo vs live trading mode.
type Environment string

const (
	// EnvironmentPaper runs fully offline with synthetic random-walk market data.
	// No API keys required.
	EnvironmentPaper Environment = "paper"

	// EnvironmentDemo connects to Binance Demo Trading (demo.binance.com).
	// Uses real live market prices via mainnet WebSocket, but all order execution
	// goes to a virtual matching engine — no real funds are at risk.
	// Register at https://demo.binance.com to get demo API keys.
	EnvironmentDemo Environment = "demo"

	// EnvironmentLive trades real funds on Binance mainnet.
	EnvironmentLive Environment = "live"
)

// Symbol is an exchange-native trading pair identifier (e.g. "BTCUSDT").
type Symbol string

// StrategyName identifies a configured strategy preset.
type StrategyName string

// Venue identifies a broker endpoint (e.g. "binance_spot", "binance_futures").
type Venue string

const (
	VenueBinanceSpot    Venue = "binance_spot"
	VenueBinanceFutures Venue = "binance_futures"
)

// Side is the direction of a trade.
type Side string

const (
	SideBuy  Side = "buy"
	SideSell Side = "sell"
)

// OrderType controls how an order is placed.
type OrderType string

const (
	OrderTypeMarket    OrderType = "market"
	OrderTypeLimit     OrderType = "limit"
	OrderTypeStopLimit OrderType = "stop_limit"
)

// TimeInForce determines how long an order stays active.
type TimeInForce string

const (
	TIFGTC TimeInForce = "gtc"
	TIFIOC TimeInForce = "ioc"
	TIFFOK TimeInForce = "fok"
)

// OrderStatus tracks the lifecycle of an order intent.
type OrderStatus string

const (
	OrderStatusPending   OrderStatus = "pending"
	OrderStatusSubmitted OrderStatus = "submitted"
	OrderStatusFilled    OrderStatus = "filled"
	OrderStatusRejected  OrderStatus = "rejected"
	OrderStatusCancelled OrderStatus = "cancelled"
)

// ContractType identifies what kind of instrument is being traded.
type ContractType string

const (
	ContractSpot            ContractType = "spot"
	ContractFuturesPerp     ContractType = "futures_perpetual"
	ContractFuturesDelivery ContractType = "futures_delivery"
)

// MarginType applies to futures positions.
type MarginType string

const (
	MarginIsolated MarginType = "isolated"
	MarginCross    MarginType = "cross"
)

// HaltMode controls what happens when a halt is triggered.
type HaltMode string

const (
	HaltModePause          HaltMode = "pause"
	HaltModeFlatten        HaltMode = "flatten"
	HaltModePauseAndNotify HaltMode = "pause_and_notify"
)

// Timeframe represents a candle interval.
type Timeframe string

const (
	TF1m  Timeframe = "1m"
	TF5m  Timeframe = "5m"
	TF15m Timeframe = "15m"
	TF1h  Timeframe = "1h"
	TF4h  Timeframe = "4h"
	TF1d  Timeframe = "1d"
)

// SessionFilter gates entries to UTC time windows.
type SessionFilter string

const (
	SessionAll       SessionFilter = "all"
	SessionNYOpen    SessionFilter = "ny_open"    // 12:00–14:00 UTC
	SessionAsianOpen SessionFilter = "asian_open" // 00:00–02:00 UTC
	SessionOverlap   SessionFilter = "overlap"    // 12:00–16:00 UTC
)

// StopLossType determines how a stop-loss distance is calculated.
type StopLossType string

const (
	SLTypeATR       StopLossType = "atr"
	SLTypeFixedPips StopLossType = "fixed_pips"
	SLTypeFixedPct  StopLossType = "fixed_pct"
)

// AgentRole identifies which agent produced a given log or run record.
type AgentRole string

const (
	AgentScreening AgentRole = "screening"
	AgentRisk      AgentRole = "risk"
	AgentCopilot   AgentRole = "copilot"
	AgentReviewer  AgentRole = "reviewer"
)

# Hexagonal Architecture

## Layer Rules

| Layer | Package | Rule |
|-------|---------|------|
| **Domain** | `internal/domain` | Pure types & constants. **Zero external imports.** No business logic. |
| **Ports** | `internal/port` | Interfaces only. One file per abstraction group (`broker.go`, `store.go`, ...). |
| **Adapters** | `internal/adapter/<system>/` | Implements port. Imports port interface, not sibling adapters. |
| **App** | `internal/app` | Composition root only. Wires ports & adapters. **No business logic here.** |
| **Domain logic** | `internal/risk`, `internal/execution`, `internal/strategy`, `internal/agent`, `internal/screening`, `internal/positionproposal` | Depends on domain & ports; **never** on adapters. |

## Dependency Direction

```
cmd → cli → app → [domain, port, risk, execution, strategy, agent, screening, positionproposal, backtest, marketdata]
                  ↓
            adapter (implements port)
```

Adapters **must not** import each other. If two adapters share behaviour, extract to `pkg/` utility.

## Port Interfaces (13 ports)

All ports defined in `internal/port/`:

| Port | File | Purpose |
|------|------|---------|
| `Broker` | `broker.go` | Order submission (spot/futures), bracket management |
| `TradeStore` | `store.go` | Persist/query trades, order intents, brackets |
| `Cache` | `cache.go` | Get/Set with TTL for transient data |
| `LLM` | `llm.go` | `Complete(ctx, LLMRequest) (LLMResponse, error)` + `ToolHandler` |
| `Notifier` | `notifier.go` | Send alerts to Telegram/Discord |
| `AgentLogStore` | `agentlog.go` | Persist agent runs, messages, tool calls |
| `PositionProposalStore` | `store.go` | CRUD for trade proposals (pending → approved → executed) |
| `ScreeningResultStore` | `store.go` | Persist screening pipeline outputs |
| `AuditStore` | `audit.go` | Audit event logging |
| `CandleProvider` | `candle.go` | Historical candle data for backtesting |
| `MarketDataSource` | `marketdata.go` | Real-time market data feeds |
| `SymbolFilterProvider` | `symbol.go` | Exchange symbol filters (min/max qty, step size) |
| `DiscoveryCache` | `discovery.go` | Cache discovery scan results |

## Adding New Port

1. Define interface in `internal/port/`.
2. Write adapter in `internal/adapter/<system>/`.
3. Register in `internal/app/runtime.go` or other wiring file.

```go
// internal/port/notifier.go
type Notifier interface {
    Notify(ctx context.Context, msg domain.Alert) error
}

// internal/adapter/telegram/notifier.go
type Notifier struct{ bot *tgbotapi.BotAPI; chatID int64 }
func (n *Notifier) Notify(ctx context.Context, msg domain.Alert) error { ... }
```

## Domain Types

### Enums

Enums are **typed string constants**, not `iota`:
```go
type Side string
const (
    SideBuy  Side = "BUY"
    SideSell Side = "SELL"
)
```

### Value Types

- All monetary values use `decimal.Decimal`
- IDs use `uuid.UUID`
- Time fields use `time.Time` (always UTC)

### Key Domain Entities

| Type | Purpose |
|------|---------|
| `OrderIntent` | Pre-execution order proposal |
| `Trade` | Executed trade record |
| `PositionProposal` | Operator-reviewable trade suggestion |
| `ScreeningResult` | Output from screening pipeline |
| `AgentRun` | Single agent execution session |
| `Candle` | OHLCV market data |
| `Symbol` | Trading pair identifier |
| `Alert` | System notification |

### Sentinel Errors (`domain/error.go`)

```go
var (
    ErrOrderRejected     = errors.New("order rejected by risk gate")
    ErrSignalRejected    = errors.New("signal rejected by risk agent")
    ErrAgentTimeout      = errors.New("agent call timed out")
    ErrHaltActive        = errors.New("trading halted by kill switch")
    ErrOrderBelowMinQty  = errors.New("order below minimum quantity")
    ErrOrderAboveMaxQty  = errors.New("order above maximum quantity")
    ErrOrderBelowMinNotional = errors.New("order below minimum notional")
    ErrSymbolFilterUnknown   = errors.New("symbol filter unknown")
    // ... 14 total sentinel errors
)
```

**Fail-closed principle**: On any error, risk gate rejects, agent holds, execution skips.

## What Doesn't Belong Where

- `pgx` / Redis imports inside `internal/domain`, `internal/port`, or any strategy/risk/execution package.
- Business decisions inside adapters — adapters translate, not decide.
- `fmt.Println` or `log.Printf` in production paths — use `slog`.
- `float64` for prices, quantities, or PnL — always `decimal.Decimal`.

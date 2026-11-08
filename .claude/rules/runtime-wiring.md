# Runtime Wiring (`internal/app`)

## Sole Responsibility

`internal/app` is the **composition root only**. It:

- Instantiates adapters (broker, cache, stores, notifiers).
- Wires business services to port interfaces.
- Launches goroutines via `errgroup.WithContext`.
- Handles shutdown via context cancellation.

**No business logic lives here.** If you find yourself adding a trading decision in `runtime.go`, it belongs in `internal/strategy`, `internal/risk`, or `internal/execution`.

## Startup Sequence

1. **Load & validate config** — fail fast on invalid config.
2. **Connect external services** (DB, Redis, exchange WS) — fail fast here.
3. **Build domain services** injected with ports.
4. **Start goroutines** via `errgroup`.
5. **Block** until `ctx` cancelled or fatal error propagates.

```go
// internal/app/runtime.go
func (a *App) Run(ctx context.Context) error {
    // 1. Config already loaded in cli.go
    
    // 2. Connect external services
    pool, err := postgres.Connect(ctx, a.cfg.DatabaseURL)
    if err != nil {
        return fmt.Errorf("database connect: %w", err)
    }
    
    redisClient, err := redis.Connect(a.cfg.RedisURL)
    if err != nil {
        return fmt.Errorf("redis connect: %w", err)
    }
    
    // 3. Build adapters
    tradeStore := postgres.NewTradeStore(pool)
    cache := redis.NewCache(redisClient)
    llm := llm.NewProvider(a.cfg.Agent)  // Fallback chain + circuit breaker
    
    // 4. Build domain services
    riskGate := risk.NewGate(a.cfg.Risk)
    execution := execution.NewEngine(tradeStore, riskGate)
    screening := screening.NewPipeline(llm, a.cfg.Screening)
    
    // 5. Launch goroutines
    g, ctx := errgroup.WithContext(ctx)
    
    g.Go(func() error {
        slog.InfoContext(ctx, "component starting", "name", "market-hub")
        err := hub.Run(ctx)
        if err != nil && !errors.Is(err, context.Canceled) {
            return fmt.Errorf("market hub: %w", err)
        }
        return nil
    })
    
    g.Go(func() error {
        slog.InfoContext(ctx, "component starting", "name", "screening-pipeline")
        err := screening.Run(ctx)
        if err != nil && !errors.Is(err, context.Canceled) {
            return fmt.Errorf("screening: %w", err)
        }
        return nil
    })
    
    // 6. Block until done
    return g.Wait()
}
```

## Goroutine Pattern

All long-running goroutines follow this template:

```go
g.Go(func() error {
    slog.InfoContext(ctx, "component starting", "name", "market-hub")
    err := hub.Run(ctx)
    if err != nil && !errors.Is(err, context.Canceled) {
        return fmt.Errorf("market hub: %w", err)
    }
    return nil
})
```

**Key rules**:
- Check `context.Canceled` / `context.DeadlineExceeded` and return `nil` — these are clean shutdowns, not errors.
- `errgroup` cancels group context on first non-nil error; all goroutines must react promptly.
- Log component name on startup for observability.

## Screening Pipeline Wiring

The 5-phase screening pipeline runs as a goroutine with configurable interval:

```go
g.Go(func() error {
    slog.InfoContext(ctx, "component starting", "name", "screening-pipeline")
    
    ticker := time.NewTicker(a.cfg.Screening.Interval)
    defer ticker.Stop()
    
    for {
        select {
        case <-ctx.Done():
            return nil
        case <-ticker.C:
            if !a.cfg.Screening.Enabled {
                continue
            }
            
            result, err := screening.Run(ctx)
            if err != nil {
                slog.ErrorContext(ctx, "screening failed", "err", err)
                continue  // Don't fail pipeline on single error
            }
            
            if a.cfg.Screening.PublishTelegram {
                publisher.Publish(ctx, result)
            }
        }
    }
})
```

### 5-Phase Screening Flow

1. **Phase 1: Bias Scoring** — Per-symbol market bias (bullish/bearish/neutral)
2. **Phase 2: Symbol Analysis** — Technical indicators, support/resistance
3. **Phase 3: Opportunity Detection** — Identify trade setups (breakouts, reversals)
4. **Phase 4: Summary Generation** — Aggregate findings into summary
5. **Phase 5: Publish** — Format and send to Telegram

Each phase uses `port.LLM` with timeout context.

## Position Proposal Workflow

Operator reviews AI-generated trade proposals:

```go
// internal/positionproposal/manager.go
type Manager struct {
    store port.PositionProposalStore
    risk  *risk.Gate
}

func (m *Manager) Submit(ctx context.Context, proposal domain.PositionProposal) error {
    // Validate proposal
    if err := m.risk.Allow(ctx, proposal.OrderIntent); err != nil {
        return fmt.Errorf("risk rejected: %w", err)
    }
    
    // Store as pending
    proposal.Status = domain.ProposalStatusPending
    return m.store.Create(ctx, proposal)
}

func (m *Manager) Approve(ctx context.Context, id uuid.UUID) error {
    proposal, err := m.store.Get(ctx, id)
    if err != nil {
        return fmt.Errorf("get proposal: %w", err)
    }
    
    // Execute approved proposal
    proposal.Status = domain.ProposalStatusApproved
    if err := m.store.Update(ctx, proposal); err != nil {
        return fmt.Errorf("update proposal: %w", err)
    }
    
    // Trigger execution
    return m.execution.Submit(ctx, proposal.OrderIntent)
}
```

### Proposal States

```go
type ProposalStatus string
const (
    ProposalStatusPending  ProposalStatus = "pending"
    ProposalStatusApproved ProposalStatus = "approved"
    ProposalStatusRejected ProposalStatus = "rejected"
    ProposalStatusExecuted ProposalStatus = "executed"
    ProposalStatusExpired  ProposalStatus = "expired"
)
```

## Paper vs Live Guard

The live broker path **must** remain behind explicit guard until fully implemented and audited:

```go
if a.cfg.Environment == domain.EnvironmentProduction {
    return fmt.Errorf("live broker not implemented — use --paper")
}
```

**Do not remove this guard without**:
1. Complete adapter implementation.
2. Integration tests passing on Binance testnet.
3. Risk gate coverage for live path.

## In-Memory vs Real Adapters

Currently, paper path uses in-memory stores (`newMemoryCache`, `newMemoryTradeStore`, etc.). When wiring real Postgres/Redis adapters:

1. Instantiate adapter (e.g. `postgres.NewTradeStore(pool)`).
2. Assign to port interface variable.
3. **Do not** change any code outside `internal/app` — ports guarantee swap is transparent.

```go
// Paper mode (current)
var tradeStore port.TradeStore = newMemoryTradeStore()

// Live mode (future)
var tradeStore port.TradeStore = postgres.NewTradeStore(pool)

// Same interface, no downstream changes
execution := execution.NewEngine(tradeStore, riskGate)
```

## Market Data Hub

Real-time data ingestion from 4 sources:

```go
g.Go(func() error {
    slog.InfoContext(ctx, "component starting", "name", "market-data-hub")
    
    hub := marketdata.NewHub(
        ingest.NewWebSocketSource(a.cfg.Ingest),
        ingest.NewRESTPollSource(a.cfg.Ingest),
        ingest.NewFileCacheSource(a.cfg.Ingest),
        ingest.NewScraperSource(a.cfg.Ingest),
    )
    
    err := hub.Run(ctx)
    if err != nil && !errors.Is(err, context.Canceled) {
        return fmt.Errorf("market data hub: %w", err)
    }
    return nil
})
```

### Ingest Sources

| Source | Purpose | Latency |
|--------|---------|---------|
| WebSocket | Real-time tick data | <100ms |
| REST Poll | Backup/polling | 60s interval |
| File Cache | Historical data | N/A |
| Scraper | Fallback when APIs fail | Variable |

## ChatOps Bot Wiring

Telegram and Discord bots wired with command handlers:

```go
g.Go(func() error {
    slog.InfoContext(ctx, "component starting", "name", "telegram-bot")
    
    bot := chatops.NewTelegramBot(
        a.cfg.Telegram,
        copilot,        // port.LLM agent
        positionMgr,    // position manager
        screening,      // screening pipeline
    )
    
    err := bot.Run(ctx)
    if err != nil && !errors.Is(err, context.Canceled) {
        return fmt.Errorf("telegram bot: %w", err)
    }
    return nil
})
```

See `chatops.md` for full command list.

## Web Server Wiring

Embedded Next.js frontend served via HTTP:

```go
g.Go(func() error {
    slog.InfoContext(ctx, "component starting", "name", "web-server")
    
    server := web.NewServer(
        a.cfg.Web,
        tradeStore,
        positionMgr,
        screening,
        hub,  // WebSocket connections
    )
    
    err := server.Run(ctx)
    if err != nil && !errors.Is(err, context.Canceled) {
        return fmt.Errorf("web server: %w", err)
    }
    return nil
})
```

## Shutdown Handling

All components must respect context cancellation:

```go
select {
case <-ctx.Done():
    slog.InfoContext(ctx, "shutting down", "component", "screening")
    return nil
case result := <-resultCh:
    // Process result
}
```

**Clean shutdown**:
1. Cancel root context (signal handler or fatal error).
2. `errgroup` propagates cancellation to all goroutines.
3. Each goroutine checks `ctx.Err()` and returns `nil`.
4. Close DB/Redis connections in defer blocks.

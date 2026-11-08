# Runtime Wiring

## Composition Root

`internal/app/runtime.go` is the **composition root** — wires all ports, adapters, and launches goroutines.

## Initialization Order

```
1. Load config (app.yaml + secrets.env)
2. Validate config (triple-agreement check)
3. Connect PostgreSQL (pgx pool)
4. Connect Redis (go-redis)
5. Initialize Binance client (spot + futures)
6. Initialize LLM providers (OpenAI, Anthropic, Gemini)
7. Initialize Telegram bot
8. Initialize Discord bot
9. Create port adapters (store, cache, broker, llm, notifier)
10. Create domain services (risk, execution, strategy, agent)
11. Create screening pipeline
12. Create market data hub
13. Launch goroutines via errgroup
14. Block on signal (SIGINT/SIGTERM)
15. Graceful shutdown
```

## Goroutine Fan-Out

All background goroutines launched via `errgroup.WithContext`:

```go
g, ctx := errgroup.WithContext(ctx)

g.Go(func() error {
    return app.marketDataHub.Run(ctx)
})

g.Go(func() error {
    return app.screeningPipeline.Run(ctx)
})

g.Go(func() error {
    return app.executionEngine.Run(ctx)
})

g.Go(func() error {
    return app.tui.Run(ctx)
})

g.Go(func() error {
    return app.webServer.Run(ctx)
})

// Wait for all or first error
return g.Wait()
```

## Shutdown Sequence

```
1. Receive SIGINT/SIGTERM
2. Cancel root context
3. All goroutines check ctx.Err() and exit
4. Close PostgreSQL pool (drains queries)
5. Close Redis connection
6. Close Telegram/Discord bots
7. Flush logs
8. Exit 0
```

## 5-Phase Screening Pipeline

`internal/screening/pipeline.go`:

| Phase | Purpose | Output |
|-------|---------|--------|
| `bias_scoring` | Score market bias (bull/bear/neutral) | Bias scores per symbol |
| `symbol_analysis` | Deep-dive top symbols | Technical analysis |
| `opportunity_detection` | Identify trade setups | Opportunity list |
| `summary` | Synthesize findings | Executive summary |
| `publish` | Send to Telegram | Notification |

Runs on `screening.interval` (default 4h).

## Position Proposal Workflow

`internal/positionproposal/workflow.go`:

```
Agent generates proposal
  → Store as "pending"
    → Operator reviews (TUI/Web/Telegram)
      → Approve → Create OrderIntent
        → Risk gate validates
          → Execution engine submits
            → Store as "executed"
      → Reject → Mark "rejected"
```

Proposals never auto-execute — operator approval required.

## Market Data Hub

`internal/marketdata/hub.go` aggregates 4 ingest sources:

1. **WebSocket** (real-time) - `internal/ingest/ws/`
2. **REST polling** (fallback) - `internal/ingest/rest/`
3. **File cache** (offline) - `internal/ingest/filecache/`
4. **Scraper** (news) - `internal/ingest/scrape/`

Hub writes to Redis (`candle:{symbol}:{timeframe}`) with TTL.
Candle aggregation: 1m → 5m → 15m → 1h → 4h → 1d.

## Execution Engine

`internal/execution/engine.go`:

```
OrderIntent received
  → Risk gate pre-validation
    → Pass → Submit to broker
      → Broker returns fill
        → Store trade
          → Create bracket (stop-loss + take-profit)
            → Monitor for OCO trigger
    → Fail → Log rejection, skip
```

Bracket orders:
- `stop_loss_pct`: Default 1%
- `take_profit_pct`: Default 2%
- OCO (one-cancels-other): When one fills, cancel other

## Risk Gate

`internal/risk/gate.go` — **fail-closed** on any error:

```go
func (g *Gate) Validate(ctx context.Context, intent domain.OrderIntent) error {
    // Check kill switch
    if g.cfg.Engine.KillSwitch {
        return domain.ErrHaltActive
    }
    
    // Check position limits
    openPositions, err := g.store.CountOpenPositions(ctx)
    if err != nil {
        return fmt.Errorf("count positions: %w", err) // fail-closed
    }
    if openPositions >= g.cfg.Risk.MaxOpenPositions {
        return domain.ErrPositionLimitReached
    }
    
    // Check position size
    if intent.Qty.Mul(intent.Price).Cmp(g.accountBalance.Mul(g.cfg.Risk.MaxPositionPct)) > 0 {
        return domain.ErrOrderRejected
    }
    
    // Check risk/reward ratio
    if intent.TakeProfit.Sub(intent.Entry).Div(intent.Entry.Sub(intent.StopLoss)).LessThan(g.cfg.Risk.MinRRRatio) {
        return domain.ErrOrderRejected
    }
    
    return nil
}
```

Never bypass this gate — all execution paths must validate.

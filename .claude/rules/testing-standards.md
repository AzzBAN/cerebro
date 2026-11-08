# Testing Standards

## Test Types

| Tag | Purpose | Command |
|-----|---------|---------|
| _(none)_ | Unit tests — no external deps, pure in-memory | `make test` |
| `integration` | Requires Binance testnet, Postgres, Redis | `make test-int` |

Use build tags to separate them:

```go
//go:build integration
```

## Test Distribution (85 test files)

| Package | Files | Focus |
|---------|-------|-------|
| `internal/agent/` | 11 | Planner, risk agent, copilot, screening, discovery |
| `internal/execution/` | 9 | Action queue, triggers, monitor, reconciler, paper trading |
| `internal/domain/` | 9 | Position, candle, ticker, symbol, types |
| `internal/adapter/` | 8 | LLM, binance, web, telegram |
| `internal/config/` | 3 | Validation, parsing |
| `internal/tui/` | 5 | Terminal UI interactions |
| `internal/strategy/` | 5 | Indicators, signals |
| `internal/risk/` | 3 | Safety-critical risk gate |
| `internal/app/` | 5 | Wiring, lifecycle |
| Other packages | 27 | Various |

## Table-Driven Tests

All unit tests use table-driven style with `t.Run`:

```go
func TestRiskGate_Allow(t *testing.T) {
    tests := []struct {
        name    string
        order   domain.OrderIntent
        wantErr bool
    }{
        {"within limits", validOrder, false},
        {"exceeds max notional", bigOrder, true},
    }
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            gate := risk.NewGate(testConfig)
            err := gate.Allow(context.Background(), tt.order)
            if (err != nil) != tt.wantErr {
                t.Errorf("Allow() err = %v, wantErr %v", err, tt.wantErr)
            }
        })
    }
}
```

## Mocking Ports

**Never mock concrete adapters.** Test against port interface using hand-written stubs or `testify/mock`:

```go
type stubBroker struct{ submitted []domain.OrderIntent }

func (s *stubBroker) Submit(ctx context.Context, o domain.OrderIntent) error {
    s.submitted = append(s.submitted, o)
    return nil
}
```

Stub lives in `internal/<package>/testhelpers_test.go` (unexported, test-only).

### Test Double Patterns

| Pattern | Use Case |
|---------|----------|
| **Stub** | Returns fixed values, records calls |
| **Fake** | In-memory implementation (e.g., `newMemoryTradeStore`) |
| **Spy** | Records method calls for assertion |
| **Mock** | Verify expected call sequence |

### Inline Test Doubles

Cerebro uses **hand-written test doubles** over generated mocks:
- Zero external test dependencies (no testify, gomock, mockery)
- Custom mocks/stubs/fakes per test file
- Interface-based design enables easy test double injection
- Compile-time interface compliance checks

## Assertions

Use stdlib `testing` comparison + `decimal.Equal` for money:

```go
// Direct comparison
if got != want {
    t.Errorf("got %v, want %v", got, want)
}

// Decimal comparison for money
if !got.Equal(want) {
    t.Errorf("got %s, want %s", got, want)
}

// Fatal errors for setup failures
if err != nil {
    t.Fatalf("unexpected error: %v", err)
}

// Error containment
if !strings.Contains(err.Error(), expected) {
    t.Errorf("error should contain %q", expected)
}
```

Avoid assertion libraries unless they're already in `go.mod`.

## Test Data (`testdata/`)

| File | Purpose |
|------|---------|
| CSV files | Market data fixtures for backtesting |
| JSON files | API response fixtures, config samples |
| WebSocket replays | Recorded WS messages for adapter tests |

### Fixture-Based Testing

```go
func TestStrategy_Signal(t *testing.T) {
    data := loadCandlesFromCSV(t, "testdata/btc_1h.csv")
    signals := strategy.Generate(data)
    // Assert signals match expected
}
```

### Replay Testing

```go
func TestWebSocket_Reconnect(t *testing.T) {
    messages := loadWSReplay(t, "testdata/ws_reconnect.json")
    // Simulate disconnect and replay
}
```

## Coverage & Race

```bash
go test -race ./...          # Run race detector
go test -cover ./...         # Check coverage
go test -coverprofile=cover.out ./... && go tool cover -func=cover.out
```

## What Must Be Tested

- Every function in `internal/risk/` — risk gate logic is safety-critical.
- Every `port` implementation — adapter unit tests with fake/in-memory upstream.
- Every strategy signal generator.
- CLI flag parsing and config validation paths.
- Agent lifecycle (thinking → tool → observing → complete/error).
- Position proposal state machine (pending → approved → executed).
- Screening pipeline phases.
- Error handling (sentinel errors, fail-closed behavior).

## Testing Conventions

1. **No external test frameworks** — pure stdlib `testing` package.
2. **`t.Helper()`** in helper functions for better failure reporting.
3. **Descriptive subtest names** — clear test naming explains intent.
4. **Inline test doubles** — avoid external mock generation.
5. **Regression tests document "why"** — explain the bug being prevented.
6. **Good error messages** — include context in assertions.
7. **Cleanup patterns** — use `defer` and `t.Cleanup` for resource management.

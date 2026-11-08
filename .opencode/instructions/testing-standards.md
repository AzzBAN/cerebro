# Testing Standards

## Test Distribution

85 test files across 21 internal packages:

| Package | Test Files | Focus |
|---------|-----------|-------|
| `internal/agent` | 12 | Agent logic, tool execution, cost tracking |
| `internal/execution` | 8 | Order submission, bracket management |
| `internal/risk` | 6 | Risk validation, fail-closed behavior |
| `internal/screening` | 5 | Pipeline phases, output generation |
| `internal/strategy` | 7 | Signal generation, indicator calculations |
| `internal/backtest` | 4 | Historical simulation, data caching |
| `internal/marketdata` | 6 | Data aggregation, source failover |
| `internal/config` | 3 | Config parsing, validation |
| `internal/domain` | 2 | Type validation, enum completeness |
| `internal/adapter/*` | 32 | Port implementations (mock + integration) |

## Running Tests

```bash
# All tests
make test

# Specific package
go test ./internal/risk/...

# With coverage
go test -cover ./internal/agent/...

# With race detector
go test -race ./internal/execution/...

# Benchmark
go test -bench=. ./internal/strategy/...
```

## Test Patterns

### Table-Driven Tests

**Always** use table-driven tests with `t.Run`:

```go
func TestRiskGate_Validate(t *testing.T) {
    tests := []struct {
        name    string
        intent  domain.OrderIntent
        wantErr error
    }{
        {
            name: "valid order",
            intent: domain.OrderIntent{
                Symbol: "BTC/USDT",
                Side:   domain.SideBuy,
                Qty:    decimal.NewFromFloat(0.01),
            },
            wantErr: nil,
        },
        {
            name: "exceeds position limit",
            intent: domain.OrderIntent{
                Symbol: "ETH/USDT",
                Side:   domain.SideSell,
                Qty:    decimal.NewFromFloat(100),
            },
            wantErr: domain.ErrPositionLimitReached,
        },
    }
    
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            gate := risk.NewGate(mockStore, cfg)
            err := gate.Validate(context.Background(), tt.intent)
            if !errors.Is(err, tt.wantErr) {
                t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
            }
        })
    }
}
```

### Fixture/Replay Testing

For market data and LLM responses, use fixtures:

```go
func TestCandleAnalysis_DetectPattern(t *testing.T) {
    // Load fixture
    candles := loadFixture(t, "testdata/btc_bullish_engulfing.json")
    
    analyzer := analysis.New()
    pattern := analyzer.DetectPattern(candles)
    
    if pattern != analysis.PatternBullishEngulfing {
        t.Errorf("expected bullish engulfing, got %v", pattern)
    }
}

func loadFixture(t *testing.T, path string) []domain.Candle {
    t.Helper()
    data, err := os.ReadFile(path)
    if err != nil {
        t.Fatalf("read fixture: %v", err)
    }
    var candles []domain.Candle
    if err := json.Unmarshal(data, &candles); err != nil {
        t.Fatalf("unmarshal fixture: %v", err)
    }
    return candles
}
```

### Mock Ports

Create mock implementations for all ports:

```go
// internal/port/mock/store.go
type MockStore struct {
    trades []domain.Trade
    err    error
}

func (m *MockStore) GetOpenTrades(ctx context.Context) ([]domain.Trade, error) {
    if m.err != nil {
        return nil, m.err
    }
    return m.trades, nil
}
```

Use `go.uber.org/mock/mockgen` for complex mocks:

```go
//go:generate mockgen -destination=mock_broker.go -package=mock . Broker
```

## Test Coverage Goals

| Package | Target Coverage |
|---------|----------------|
| `internal/risk` | 100% (safety-critical) |
| `internal/execution` | 95% |
| `internal/strategy` | 90% |
| `internal/agent` | 85% |
| `internal/adapter/*` | 80% |

Check coverage:

```bash
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## Benchmarking

Benchmark hot paths (indicator calculations, candle aggregation):

```go
func BenchmarkRSI_Calculate(b *testing.B) {
    candles := generateCandles(1000)
    rsi := indicators.NewRSI(14)
    
    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        rsi.Calculate(candles)
    }
}
```

Run benchmarks:

```bash
go test -bench=BenchmarkRSI -benchmem ./internal/strategy/...
```

## Integration Tests

Tag integration tests with `//go:build integration`:

```go
//go:build integration

package postgres_test

import (
    "testing"
    "github.com/azhar/cerebro/internal/adapter/postgres"
)

func TestStore_Integration(t *testing.T) {
    // Requires running PostgreSQL
    store := postgres.NewStore("postgresql://localhost:5432/cerebro_test")
    // ...
}
```

Run with:

```bash
go test -tags=integration ./internal/adapter/postgres/...
```

## What Doesn't Belong

- Network calls in unit tests (use mocks)
- Hardcoded API keys or secrets
- `time.Sleep` for synchronization (use channels or `testify/require.Eventually`)
- `float64` for monetary assertions (use `decimal.Equal`)

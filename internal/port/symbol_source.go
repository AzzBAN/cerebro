package port

import (
	"context"

	"github.com/azhar/cerebro/internal/domain"
)

// SymbolSource is the per-cycle set of symbols the screening pipeline
// iterates over. Implementations may be static (backed by markets.yaml),
// dynamic (backed by the discovery cache), or a union of both. Calls are
// made on every screening cycle, so implementations must be cheap — no
// network I/O. Heavy work belongs in a separate scheduler that publishes
// to the cache behind this port.
type SymbolSource interface {
	Symbols(ctx context.Context) []domain.Symbol
}

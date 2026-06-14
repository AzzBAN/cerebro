package futures

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	gobinancefutures "github.com/adshao/go-binance/v2/futures"
	"github.com/azhar/cerebro/internal/domain"
	"github.com/shopspring/decimal"
)

// FuturesExchangeInfo caches the USDT-M perpetual symbol filters loaded from
// the /fapi/v1/exchangeInfo endpoint. Analogous to SpotExchangeInfo.
type FuturesExchangeInfo struct {
	client *gobinancefutures.Client

	mu      sync.RWMutex
	filters map[domain.Symbol]domain.SymbolFilter
}

// NewFuturesExchangeInfo creates an empty cache. Call Refresh before use.
func NewFuturesExchangeInfo(client *gobinancefutures.Client) *FuturesExchangeInfo {
	return &FuturesExchangeInfo{
		client:  client,
		filters: make(map[domain.Symbol]domain.SymbolFilter),
	}
}

// Venue identifies this store.
func (f *FuturesExchangeInfo) Venue() domain.Venue { return domain.VenueBinanceFutures }

// Filter returns the cached filter or ErrSymbolFilterUnknown.
func (f *FuturesExchangeInfo) Filter(symbol domain.Symbol) (domain.SymbolFilter, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	out, ok := f.filters[symbol]
	if !ok {
		return domain.SymbolFilter{}, fmt.Errorf("%w: %s", domain.ErrSymbolFilterUnknown, symbol)
	}
	return out, nil
}

// Refresh reloads the full exchange info and replaces the cache atomically.
// Only perpetual contracts are captured; delivery / quarterly symbols are
// skipped by their contract type.
func (f *FuturesExchangeInfo) Refresh(ctx context.Context) error {
	info, err := f.client.NewExchangeInfoService().Do(ctx)
	if err != nil {
		return fmt.Errorf("futures exchange info: %w", err)
	}

	next := make(map[domain.Symbol]domain.SymbolFilter, len(info.Symbols))
	for _, sym := range info.Symbols {
		// Only capture perpetuals — our execution layer doesn't trade
		// delivery contracts, and their symbol naming conflicts would
		// pollute the cache.
		if sym.ContractType != "PERPETUAL" {
			continue
		}
		canonical, nerr := domain.NormalizeExchangeSymbol(sym.Symbol, domain.ContractFuturesPerp)
		if nerr != nil {
			continue
		}
		filter := domain.SymbolFilter{
			Symbol:     canonical,
			Venue:      domain.VenueBinanceFutures,
			BaseAsset:  sym.BaseAsset,
			QuoteAsset: sym.QuoteAsset,
		}
		if pf := sym.PriceFilter(); pf != nil {
			if d, perr := decimal.NewFromString(pf.TickSize); perr == nil {
				filter.TickSize = d
			}
		}
		if lf := sym.LotSizeFilter(); lf != nil {
			if d, perr := decimal.NewFromString(lf.StepSize); perr == nil {
				filter.StepSize = d
			}
			if d, perr := decimal.NewFromString(lf.MinQuantity); perr == nil {
				filter.MinQty = d
			}
			if d, perr := decimal.NewFromString(lf.MaxQuantity); perr == nil {
				filter.MaxQty = d
			}
		}
		if mn := sym.MinNotionalFilter(); mn != nil {
			if d, perr := decimal.NewFromString(mn.Notional); perr == nil {
				filter.MinNotional = d
			}
		}
		next[canonical] = filter
	}

	f.mu.Lock()
	f.filters = next
	f.mu.Unlock()

	slog.Info("futures exchange info loaded", "symbols", len(next))
	return nil
}

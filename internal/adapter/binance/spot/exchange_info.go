package spot

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	gobinance "github.com/adshao/go-binance/v2"
	"github.com/azhar/cerebro/internal/domain"
	"github.com/shopspring/decimal"
)

// SpotExchangeInfo is an in-memory cache of Binance spot symbol filters.
// It loads once at startup via REST and serves filters from memory with an
// RWMutex; Refresh re-fetches from the exchange.
type SpotExchangeInfo struct {
	client *gobinance.Client

	mu      sync.RWMutex
	filters map[domain.Symbol]domain.SymbolFilter
}

// NewSpotExchangeInfo creates an empty cache. Call Refresh before use.
func NewSpotExchangeInfo(client *gobinance.Client) *SpotExchangeInfo {
	return &SpotExchangeInfo{
		client:  client,
		filters: make(map[domain.Symbol]domain.SymbolFilter),
	}
}

// Venue identifies this store.
func (s *SpotExchangeInfo) Venue() domain.Venue { return domain.VenueBinanceSpot }

// Filter returns the cached filter for symbol or ErrSymbolFilterUnknown.
func (s *SpotExchangeInfo) Filter(symbol domain.Symbol) (domain.SymbolFilter, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	f, ok := s.filters[symbol]
	if !ok {
		return domain.SymbolFilter{}, fmt.Errorf("%w: %s", domain.ErrSymbolFilterUnknown, symbol)
	}
	return f, nil
}

// Refresh fetches the full exchange info and replaces the cache atomically.
//
// Only spot symbols (contract type == SPOT and quote USDT/BUSD/etc) are
// captured — the universe of tradeable pairs is large but fetching the full
// list once is cheap compared to per-order round-trips.
func (s *SpotExchangeInfo) Refresh(ctx context.Context) error {
	info, err := s.client.NewExchangeInfoService().Do(ctx)
	if err != nil {
		return fmt.Errorf("spot exchange info: %w", err)
	}

	next := make(map[domain.Symbol]domain.SymbolFilter, len(info.Symbols))
	for _, sym := range info.Symbols {
		canonical, nerr := domain.NormalizeExchangeSymbol(sym.Symbol, domain.ContractSpot)
		if nerr != nil {
			continue
		}
		f := domain.SymbolFilter{
			Symbol:     canonical,
			Venue:      domain.VenueBinanceSpot,
			BaseAsset:  sym.BaseAsset,
			QuoteAsset: sym.QuoteAsset,
		}
		if pf := sym.PriceFilter(); pf != nil {
			if d, perr := decimal.NewFromString(pf.TickSize); perr == nil {
				f.TickSize = d
			}
		}
		if lf := sym.LotSizeFilter(); lf != nil {
			if d, perr := decimal.NewFromString(lf.StepSize); perr == nil {
				f.StepSize = d
			}
			if d, perr := decimal.NewFromString(lf.MinQuantity); perr == nil {
				f.MinQty = d
			}
			if d, perr := decimal.NewFromString(lf.MaxQuantity); perr == nil {
				f.MaxQty = d
			}
		}
		if nf := sym.NotionalFilter(); nf != nil {
			if d, perr := decimal.NewFromString(nf.MinNotional); perr == nil {
				f.MinNotional = d
			}
		}
		next[canonical] = f
	}

	s.mu.Lock()
	s.filters = next
	s.mu.Unlock()

	slog.Info("spot exchange info loaded", "symbols", len(next))
	return nil
}

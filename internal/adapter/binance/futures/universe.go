package futures

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	gobinancefutures "github.com/adshao/go-binance/v2/futures"
	"github.com/azhar/cerebro/internal/domain"
	"github.com/shopspring/decimal"
)

// UniverseFeed implements port.UniverseFeed against Binance USDT-M Futures.
// It relies only on public REST endpoints (no authentication), so a
// throw-away client constructed without credentials is sufficient.
//
// exchangeInfo is expensive and rarely changes, so it is fetched once and
// cached for exchangeInfoTTL. AllTickers joins ticker rows with the cached
// exchangeInfo map to resolve quote asset + onboard date.
type UniverseFeed struct {
	client *gobinancefutures.Client

	mu               sync.Mutex
	infoCache        map[string]symbolInfo // key = exchange symbol (e.g. BTCUSDT)
	infoFetchedAt    time.Time
	exchangeInfoTTL  time.Duration
}

type symbolInfo struct {
	QuoteAsset   string
	ContractType string // PERPETUAL, CURRENT_QUARTER, …
	Status       string // TRADING, BREAK, …
	OnboardDate  time.Time
}

// NewUniverseFeed builds a public-only futures UniverseFeed. Pass nil to
// use a default TTL of 1h for the exchangeInfo cache.
func NewUniverseFeed() *UniverseFeed {
	return &UniverseFeed{
		client:          gobinancefutures.NewClient("", ""),
		exchangeInfoTTL: time.Hour,
	}
}

// Venue returns domain.VenueBinanceFutures.
func (f *UniverseFeed) Venue() domain.Venue { return domain.VenueBinanceFutures }

// AllTickers returns a TickerSummary for every TRADING perpetual USDT-M
// symbol on Binance Futures. Non-perpetual contracts (quarterly, delivery)
// and non-TRADING symbols are filtered out.
func (f *UniverseFeed) AllTickers(ctx context.Context) ([]domain.TickerSummary, error) {
	info, err := f.getExchangeInfo(ctx)
	if err != nil {
		return nil, fmt.Errorf("binance futures universe: exchange info: %w", err)
	}

	stats, err := f.client.NewListPriceChangeStatsService().Do(ctx)
	if err != nil {
		return nil, fmt.Errorf("binance futures universe: 24h stats: %w", err)
	}

	now := time.Now().UTC()
	out := make([]domain.TickerSummary, 0, len(stats))
	for _, s := range stats {
		meta, ok := info[s.Symbol]
		if !ok {
			continue
		}
		if meta.Status != "TRADING" {
			continue
		}
		if meta.ContractType != "PERPETUAL" {
			continue
		}

		sym, err := domain.NormalizeExchangeSymbol(s.Symbol, domain.ContractFuturesPerp)
		if err != nil {
			continue
		}

		last, _ := decimal.NewFromString(s.LastPrice)
		vol, _ := decimal.NewFromString(s.Volume)
		qvol, _ := decimal.NewFromString(s.QuoteVolume)
		pct := parseFloat(s.PriceChangePercent)

		out = append(out, domain.TickerSummary{
			Symbol:           sym,
			Venue:            domain.VenueBinanceFutures,
			ContractType:     domain.ContractFuturesPerp,
			QuoteAsset:       meta.QuoteAsset,
			LastPrice:        last,
			PriceChangePct24: pct,
			Volume24h:        vol,
			QuoteVolume24h:   qvol,
			ListedAt:         meta.OnboardDate,
			FetchedAt:        now,
		})
	}
	return out, nil
}

// NewListings returns the subset of AllTickers with OnboardDate within maxAge.
func (f *UniverseFeed) NewListings(ctx context.Context, maxAge time.Duration) ([]domain.TickerSummary, error) {
	if maxAge <= 0 {
		return nil, nil
	}
	all, err := f.AllTickers(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]domain.TickerSummary, 0, len(all))
	for _, t := range all {
		if t.IsNewListing(maxAge) {
			out = append(out, t)
		}
	}
	return out, nil
}

func (f *UniverseFeed) getExchangeInfo(ctx context.Context) (map[string]symbolInfo, error) {
	f.mu.Lock()
	if f.infoCache != nil && time.Since(f.infoFetchedAt) < f.exchangeInfoTTL {
		out := f.infoCache
		f.mu.Unlock()
		return out, nil
	}
	f.mu.Unlock()

	resp, err := f.client.NewExchangeInfoService().Do(ctx)
	if err != nil {
		return nil, err
	}

	next := make(map[string]symbolInfo, len(resp.Symbols))
	for _, s := range resp.Symbols {
		sym := strings.ToUpper(strings.TrimSpace(s.Symbol))
		var onboard time.Time
		if s.OnboardDate > 0 {
			onboard = time.UnixMilli(s.OnboardDate).UTC()
		}
		next[sym] = symbolInfo{
			QuoteAsset:   strings.ToUpper(s.QuoteAsset),
			ContractType: string(s.ContractType),
			Status:       s.Status,
			OnboardDate:  onboard,
		}
	}

	f.mu.Lock()
	f.infoCache = next
	f.infoFetchedAt = time.Now().UTC()
	f.mu.Unlock()

	return next, nil
}

func parseFloat(s string) float64 {
	d, err := decimal.NewFromString(strings.TrimSpace(s))
	if err != nil {
		return 0
	}
	f, _ := d.Float64()
	return f
}

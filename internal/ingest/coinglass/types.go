package coinglass

import (
	"encoding/json"
	"time"

	"github.com/azhar/cerebro/internal/domain"
	"github.com/shopspring/decimal"
)

// apiResponse wraps all CoinGlass v4 API responses.
type apiResponse struct {
	Code    string          `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

// FundingRateData is the raw CoinGlass funding rate response.
type FundingRateData struct {
	Symbol          string  `json:"symbol"`
	Rate            float64 `json:"rate"`
	NextFundingTime int64   `json:"nextFundingTime"`
}

// OpenInterestData is the raw CoinGlass OI response.
type OpenInterestData struct {
	Symbol       string  `json:"symbol"`
	OpenInterest float64 `json:"openInterest"`
	Change1h     float64 `json:"change1h"`
	Change4h     float64 `json:"change4h"`
	Change24h    float64 `json:"change24h"`
}

// LiquidationData is the raw liquidation event from CoinGlass.
type LiquidationData struct {
	Symbol    string  `json:"symbol"`
	Side      string  `json:"side"`
	Amount    float64 `json:"amount"`
	Price     float64 `json:"price"`
	Timestamp int64   `json:"timestamp"`
}

// FearGreedData is the raw Fear & Greed index response.
type FearGreedData struct {
	Value    int    `json:"value"`
	Category string `json:"classification"`
}

// toFundingRate converts raw API data to the domain type.
func toFundingRate(sym domain.Symbol, d FundingRateData) *domain.FundingRate {
	return &domain.FundingRate{
		Symbol:          sym,
		Rate:            d.Rate,
		NextFundingTime: time.Unix(d.NextFundingTime/1000, 0).UTC(),
		FetchedAt:       time.Now().UTC(),
	}
}

// toOpenInterest converts raw API data to the domain type.
func toOpenInterest(sym domain.Symbol, d OpenInterestData) *domain.OpenInterest {
	return &domain.OpenInterest{
		Symbol:    sym,
		TotalUSD:  decimal.NewFromFloat(d.OpenInterest),
		Change1h:  d.Change1h,
		Change4h:  d.Change4h,
		Change24h: d.Change24h,
		FetchedAt: time.Now().UTC(),
	}
}

// toFearGreed converts raw API data to the domain type.
func toFearGreed(d FearGreedData) *domain.FearGreedIndex {
	return &domain.FearGreedIndex{
		Value:     d.Value,
		Category:  d.Category,
		FetchedAt: time.Now().UTC(),
	}
}

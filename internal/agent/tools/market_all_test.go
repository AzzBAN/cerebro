package tools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/azhar/cerebro/internal/domain"
	"github.com/shopspring/decimal"
)

func TestGetAllMarketData(t *testing.T) {
	symBTC := domain.Symbol("BTCUSDT")
	symETH := domain.Symbol("ETHUSDT")
	symbols := []domain.Symbol{symBTC, symETH, "XRPUSDT"}

	lookup := func(sym domain.Symbol) (domain.Quote, bool) {
		if sym == symBTC {
			return domain.Quote{
				Symbol:             symBTC,
				Last:               decimal.NewFromInt(50000),
				Bid:                decimal.NewFromInt(49999),
				Ask:                decimal.NewFromInt(50001),
				Mid:                decimal.NewFromInt(50000),
				PriceChangePercent: decimal.RequireFromString("2.5"),
				Volume24h:          decimal.NewFromInt(1000000),
			}, true
		}
		if sym == symETH {
			return domain.Quote{
				Symbol:             symETH,
				Last:               decimal.NewFromInt(3000),
				Bid:                decimal.NewFromInt(2999),
				Ask:                decimal.NewFromInt(3001),
				Mid:                decimal.NewFromInt(3000),
				PriceChangePercent: decimal.RequireFromString("-1.2"),
				Volume24h:          decimal.NewFromInt(500000),
			}, true
		}
		return domain.Quote{}, false
	}

	tool := GetAllMarketData(lookup, symbols)
	result, err := tool.Handler(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var parsed struct {
		Symbols []struct {
			Symbol    string `json:"symbol"`
			Available bool   `json:"available"`
		} `json:"symbols"`
		Count int `json:"count"`
	}
	if err := json.Unmarshal(result, &parsed); err != nil {
		t.Fatalf("parse result: %v", err)
	}

	if parsed.Count != 3 {
		t.Errorf("count = %d, want 3", parsed.Count)
	}
	if len(parsed.Symbols) != 3 {
		t.Fatalf("symbols length = %d, want 3", len(parsed.Symbols))
	}

	available := 0
	for _, s := range parsed.Symbols {
		if s.Available {
			available++
		}
	}
	if available != 2 {
		t.Errorf("available symbols = %d, want 2", available)
	}

	// Verify tool definition.
	if tool.Definition.Name != "get_all_market_data" {
		t.Errorf("tool name = %q, want %q", tool.Definition.Name, "get_all_market_data")
	}
}

func TestGetAllMarketData_EmptySymbols(t *testing.T) {
	lookup := func(sym domain.Symbol) (domain.Quote, bool) {
		return domain.Quote{}, false
	}

	tool := GetAllMarketData(lookup, nil)
	result, err := tool.Handler(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var parsed struct {
		Count int `json:"count"`
	}
	if err := json.Unmarshal(result, &parsed); err != nil {
		t.Fatalf("parse result: %v", err)
	}
	if parsed.Count != 0 {
		t.Errorf("count = %d, want 0", parsed.Count)
	}
}

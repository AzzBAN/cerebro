package domain

import (
	"fmt"
	"strings"
)

// NormalizeConfigSymbol converts a configured symbol into the canonical
// application format for the given contract type.
func NormalizeConfigSymbol(raw string, contractType ContractType) (Symbol, error) {
	s := strings.ToUpper(strings.TrimSpace(raw))
	s = strings.ReplaceAll(s, " ", "")
	if s == "" {
		return "", fmt.Errorf("empty symbol")
	}

	isPerp := strings.HasSuffix(s, "-PERP")
	base := strings.TrimSuffix(s, "-PERP")
	base = strings.ReplaceAll(base, "/", "")
	if len(base) < 6 {
		return "", fmt.Errorf("invalid symbol %q", raw)
	}

	quote := "USDT"
	if !strings.HasSuffix(base, quote) {
		return "", fmt.Errorf("unsupported quote asset in %q", raw)
	}
	baseAsset := strings.TrimSuffix(base, quote)
	if baseAsset == "" {
		return "", fmt.Errorf("missing base asset in %q", raw)
	}

	canonical := baseAsset + "/" + quote
	switch contractType {
	case ContractSpot:
		if isPerp {
			return "", fmt.Errorf("spot symbol %q must not use -PERP suffix", raw)
		}
		return Symbol(canonical), nil
	case ContractFuturesPerp:
		if !isPerp && strings.Contains(s, "/") {
			return "", fmt.Errorf("futures perpetual symbol %q must use -PERP suffix", raw)
		}
		return Symbol(canonical + "-PERP"), nil
	default:
		if isPerp {
			return Symbol(canonical + "-PERP"), nil
		}
		return Symbol(canonical), nil
	}
}

// NormalizeExchangeSymbol converts an exchange-native symbol into the
// canonical application format for the given venue/contract type.
func NormalizeExchangeSymbol(raw string, contractType ContractType) (Symbol, error) {
	s := strings.ToUpper(strings.TrimSpace(raw))
	s = strings.ReplaceAll(s, "/", "")
	if s == "" {
		return "", fmt.Errorf("empty symbol")
	}
	return NormalizeConfigSymbol(s, contractType)
}

// ToExchangeSymbol converts a canonical application symbol into the Binance
// exchange-native string used by REST and WS APIs.
func ToExchangeSymbol(sym Symbol) string {
	s := strings.ToUpper(strings.TrimSpace(string(sym)))
	s = strings.TrimSuffix(s, "-PERP")
	s = strings.ReplaceAll(s, "/", "")
	return s
}

package tools

import (
	"strings"

	"github.com/azhar/cerebro/internal/domain"
)

// normalizeToolSymbol converts various symbol formats to the canonical
// application format. Handles: BTCUSDT → BTC/USDT,
// BTC/USDT-PERP → BTC/USDT-PERP, BTC → BTC/USDT.
func normalizeToolSymbol(raw string) domain.Symbol {
	s := strings.ToUpper(strings.TrimSpace(raw))

	// Already canonical: BTC/USDT or BTC/USDT-PERP
	if strings.Contains(s, "/") {
		return domain.Symbol(s)
	}

	// Strip exchange-style suffix: BTCUSDT → BTC/USDT
	if base, ok := strings.CutSuffix(s, "USDT"); ok && base != "" {
		return domain.Symbol(base + "/USDT")
	}
	if base, ok := strings.CutSuffix(s, "BUSD"); ok && base != "" {
		return domain.Symbol(base + "/BUSD")
	}

	// Bare ticker: BTC → BTC/USDT
	return domain.Symbol(s + "/USDT")
}

package binance

import (
	"errors"

	"github.com/adshao/go-binance/v2/common"
)

// Binance REST error codes for "this order is not on the book".
//
//   - -2011 ("Unknown order sent.") is returned by the cancel endpoints when
//     the referenced order ID is not a live order — it was already filled,
//     expired, or cancelled.
//   - -2013 ("Order does not exist.") is the query-side equivalent.
//
// See https://binance-docs.github.io/apidocs/spot/en/#error-codes and the
// USDT-M futures error-code table (same numeric values).
const (
	codeUnknownOrder     int64 = -2011
	codeOrderDoesntExist int64 = -2013
)

// IsOrderNotFound reports whether err is a Binance API error indicating the
// order no longer exists on the exchange. Cancelling an already-gone order is
// idempotent — the desired end state (no live order) is already true — so
// callers should treat this as success rather than a hard failure.
func IsOrderNotFound(err error) bool {
	if err == nil {
		return false
	}
	var apiErr *common.APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	return apiErr.Code == codeUnknownOrder || apiErr.Code == codeOrderDoesntExist
}

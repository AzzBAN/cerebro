package observability

import (
	"net/url"
	"regexp"
	"strings"
)

// sensitiveQueryParams are query-string keys whose values must never be logged
// in clear-text. Match is case-insensitive against the parameter name.
var sensitiveQueryParams = map[string]struct{}{
	"token":         {},
	"apikey":        {},
	"api_key":       {},
	"access_token":  {},
	"refresh_token": {},
	"key":           {},
	"secret":        {},
	"password":      {},
	"auth":          {},
	"signature":     {},
	"sig":           {},
}

// urlInErrRe finds URL-shaped substrings inside arbitrary error messages
// (e.g. wrapped Go *url.Error strings like `Get "https://x?token=foo": ...`).
var urlInErrRe = regexp.MustCompile(`https?://[^\s"'<>]+`)

// RedactURL returns rawURL with the values of any sensitive query parameters
// replaced by "REDACTED". If the input cannot be parsed as a URL it is
// returned unchanged.
//
// This is the single source of truth for URL sanitisation across logging and
// error wrapping. Use it whenever a URL might cross the log/notification
// boundary — token leaks have shown up before in third-party HTTP error
// strings (see Finnhub).
func RedactURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme == "" {
		return rawURL
	}
	if !redactQueryInPlace(u) {
		return rawURL
	}
	return u.String()
}

// RedactErrorString scrubs every URL embedded in s (handles the common
// Go-stdlib pattern of `Get "https://...": context deadline exceeded` where
// the raw URL with its query string ends up inside the error message).
//
// It is intentionally lenient: anything that doesn't parse as a URL is left
// untouched so that the rest of the message is preserved verbatim.
func RedactErrorString(s string) string {
	if s == "" {
		return s
	}
	return urlInErrRe.ReplaceAllStringFunc(s, RedactURL)
}

// RedactErr is a convenience wrapper for slog attribute use.
//
//	slog.Error("scrape job failed",
//	    "name", name,
//	    "error", observability.RedactErr(err))
//
// Returns "" on a nil error.
func RedactErr(err error) string {
	if err == nil {
		return ""
	}
	return RedactErrorString(err.Error())
}

func redactQueryInPlace(u *url.URL) bool {
	if u.RawQuery == "" {
		return false
	}
	q := u.Query()
	changed := false
	for k, vs := range q {
		if !isSensitiveParam(k) {
			continue
		}
		for i := range vs {
			vs[i] = "REDACTED"
		}
		q[k] = vs
		changed = true
	}
	if !changed {
		return false
	}
	u.RawQuery = q.Encode()
	return true
}

func isSensitiveParam(key string) bool {
	_, ok := sensitiveQueryParams[strings.ToLower(key)]
	return ok
}

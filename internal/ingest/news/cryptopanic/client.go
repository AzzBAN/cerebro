package cryptopanic

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	baseURL     = "https://cryptopanic.com"
	homePath    = "/"
	postsPath   = "/web-api/posts/"
	defaultUA   = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"
	csrfCookie  = "csrftoken"
	csrfTTL     = 1 * time.Hour
	minInterval = 1 * time.Second // minimum spacing between successful calls

	// defaultRateLimitCooldown is applied after a 429 when the upstream
	// does not include a usable Retry-After header. Chosen deliberately
	// longer than the combined news runner's tick (5m) so repeated ticks
	// during a rate-limit window are suppressed at the client level and
	// don't provoke Cloudflare further.
	defaultRateLimitCooldown = 10 * time.Minute
	// maxRateLimitCooldown caps Retry-After values so a hostile upstream
	// can't park us for hours. The runner will try again on the next tick.
	maxRateLimitCooldown = 30 * time.Minute
)

// ErrRateLimited is returned by FetchPosts when the client is inside a
// rate-limit cooldown window (triggered by a prior 429 from upstream).
// Callers can use errors.Is to distinguish this from genuine failures
// and skip related calls (e.g. per-asset fetches) for the rest of the
// tick without logging warnings.
var ErrRateLimited = errors.New("cryptopanic: rate-limited (cooldown active)")

// ErrAntiBot is returned when CryptoPanic's edge (Cloudflare) blocks the
// pure-Go client with a 403 — either on the CSRF-bootstrap GET / or on the
// /web-api/posts/ POST. The block keys off the request's TLS fingerprint and
// the absence of JS execution, so retrying or refreshing the CSRF token with
// the same HTTP client cannot clear it. FetchPosts surfaces this immediately
// (no retry storm) and the FallbackFeed routes the call to the headless-
// Chromium tier, which presents a real browser fingerprint and can pass the
// challenge. This is distinct from ErrBadPayload (AES key rotation): the RE
// client is structurally fine here, it's just being denied at the edge.
var ErrAntiBot = errors.New("cryptopanic: blocked by anti-bot edge (403)")

// Client is a reverse-engineered HTTP client for CryptoPanic's internal
// /web-api/posts/ endpoint. It emulates the Vue SPA's CSRF dance and
// decrypts the AES-CBC+zlib-wrapped response locally.
type Client struct {
	http      *http.Client
	userAgent string
	baseURL   string // set for tests; defaults to production baseURL

	mu            sync.Mutex
	csrfExpiry    time.Time
	lastCall      time.Time
	cooldownUntil time.Time
	lastBlockCode int
}

// NewClient constructs a Client with a sensible default timeout.
func NewClient(timeout time.Duration) (*Client, error) {
	if timeout == 0 {
		timeout = 15 * time.Second
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, fmt.Errorf("cryptopanic: cookie jar: %w", err)
	}
	return &Client{
		http: &http.Client{
			Timeout: timeout,
			Jar:     jar,
		},
		userAgent: defaultUA,
		baseURL:   baseURL,
	}, nil
}

// base returns c.baseURL or the package default. Kept as a method so the
// rest of the client can stay oblivious to the test hook.
func (c *Client) base() string {
	if c.baseURL != "" {
		return c.baseURL
	}
	return baseURL
}

// Query controls what posts to return from /web-api/posts/. Zero values
// map to sensible defaults (global feed, filter=hot, kind=news).
type Query struct {
	// Currencies filters to posts that reference any of these tickers (BTC,
	// ETH, ...). Empty returns the global feed.
	Currencies []string
	// Filter is one of: hot, rising, bullish, bearish, important, saved, lol.
	// Empty defaults to "hot".
	Filter string
	// Kind is "news" or "media". Empty defaults to "news".
	Kind string
	// Regions is a list of language codes (e.g. ["en"]). Empty defaults to "en".
	Regions []string
	// Public forces public=true which unlocks anonymous access to the
	// response payload. We always send it.
	Public bool
}

// FetchPosts retrieves and decrypts the latest posts matching q. The
// returned slice is already normalised (column-oriented response expanded
// into per-post objects).
//
// Rate-limit handling: when upstream returns 429, the client enters a
// cooldown window honouring Retry-After (default 10m, capped 30m) and
// subsequent calls short-circuit with ErrRateLimited until the window
// expires. This avoids burning retry budget and log noise while
// Cloudflare is throttling us.
//
// 5xx / network errors are retried up to maxAttempts with exponential
// backoff. A 403 is treated as a stale-CSRF signal: we invalidate and
// refresh the token before the next attempt.
func (c *Client) FetchPosts(ctx context.Context, q Query) ([]rawPost, error) {
	// Fast-path: if a prior 429 parked us in cooldown, bail out before
	// touching the network.
	if remain, ok := c.cooldownRemaining(time.Now()); ok {
		return nil, fmt.Errorf("%w (remaining %s)", ErrRateLimited, remain.Truncate(time.Second))
	}

	const maxAttempts = 3
	formBody := buildFormBody(q)

	var lastErr error
	backoff := 500 * time.Millisecond
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(backoff):
			}
			backoff *= 2
		}

		body, csrf, err := c.fetchPostsOnce(ctx, formBody)
		if err == nil {
			posts, err := decodeEnvelope(body, csrf)
			if err != nil {
				return nil, err
			}
			c.markCalled()
			return posts, nil
		}

		// Retry on transient errors; surface everything else immediately.
		var te *transientError
		if !errors.As(err, &te) {
			return nil, err
		}

		// 429 — stop retrying, arm cooldown, and surface ErrRateLimited
		// so callers can skip related per-asset fetches for this tick.
		// Exponential backoff doesn't help against Cloudflare rate
		// limiting; we'd just exhaust attempts and produce noisy logs.
		if te.status == http.StatusTooManyRequests {
			c.applyCooldown(te.retryAfter, te.status)
			return nil, fmt.Errorf("%w: upstream 429 %s", ErrRateLimited, http.StatusText(te.status))
		}

		// 403 — anti-bot edge block. Retrying the same HTTP client can't
		// clear a fingerprint challenge, so stop immediately and surface
		// ErrAntiBot. The FallbackFeed routes this to the headless browser,
		// which presents a real fingerprint and can pass the challenge.
		// (The POST-path 403 already invalidated the CSRF token; the
		// GET-path 403 has no token to refresh.)
		if te.status == http.StatusForbidden {
			return nil, fmt.Errorf("%w: %s", ErrAntiBot, te.Error())
		}

		lastErr = err
		if te.retryAfter > backoff {
			backoff = te.retryAfter
		}
		// Cap retry-after at 10s so a 60s Cloudflare directive doesn't
		// freeze the whole ingest tick. The runner will try again next
		// interval anyway.
		if backoff > 10*time.Second {
			backoff = 10 * time.Second
		}
	}
	return nil, fmt.Errorf("cryptopanic: exhausted %d attempts: %w", maxAttempts, lastErr)
}

// transientError marks an error as retryable. errors.As lets callers
// distinguish transient failures from permanent ones (ErrBadPayload).
type transientError struct {
	status     int
	retryAfter time.Duration
	inner      error
}

func (t *transientError) Error() string { return t.inner.Error() }
func (t *transientError) Unwrap() error { return t.inner }

// fetchPostsOnce performs a single POST /web-api/posts/ attempt and
// returns the raw response body (still encrypted) together with the
// CSRF token used to sign it — the token is needed by the decrypter
// because the IV is derived from (prefix + csrftoken). On 5xx/429 it
// returns a *transientError so the caller can retry.
func (c *Client) fetchPostsOnce(ctx context.Context, formBody string) ([]byte, string, error) {
	if err := c.ensureCSRF(ctx); err != nil {
		// ensureCSRF may already return a *transientError carrying a status
		// (e.g. a 403 anti-bot block on the bootstrap GET). Preserve it so
		// FetchPosts can route on the status rather than re-wrapping it as
		// a status-0 generic transient (which would retry into exhaustion).
		var te *transientError
		if errors.As(err, &te) {
			return nil, "", err
		}
		return nil, "", &transientError{inner: err}
	}
	c.respectRate()

	csrf := c.csrfToken()
	if csrf == "" {
		c.invalidateCSRF()
		return nil, "", &transientError{inner: fmt.Errorf("cryptopanic: no csrftoken in jar after GET /")}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base()+postsPath, strings.NewReader(formBody))
	if err != nil {
		return nil, "", fmt.Errorf("cryptopanic: build POST: %w", err)
	}
	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=UTF-8")
	req.Header.Set("X-CSRFToken", csrf)
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	req.Header.Set("Referer", c.base()+"/")
	req.Header.Set("Origin", c.base())

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, "", &transientError{inner: fmt.Errorf("cryptopanic: POST %s: %w", postsPath, err)}
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusForbidden {
		// CSRF rotation or anti-bot: invalidate so the next attempt
		// triggers a GET / to refresh the cookie.
		c.invalidateCSRF()
		return nil, "", &transientError{
			status: resp.StatusCode,
			inner:  fmt.Errorf("cryptopanic: 403 forbidden (csrf likely stale)"),
		}
	}
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
		// Drain + close so the connection is reusable, but we do not
		// include the error body in the returned message — Cloudflare's
		// 502 JSON is ~2KB and floods the log. The status code is what
		// matters.
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 2048))
		retry := parseRetryAfter(resp.Header.Get("Retry-After"))
		return nil, "", &transientError{
			status:     resp.StatusCode,
			retryAfter: retry,
			inner:      fmt.Errorf("cryptopanic: upstream %d %s", resp.StatusCode, http.StatusText(resp.StatusCode)),
		}
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return nil, "", fmt.Errorf("cryptopanic: status %d: %s", resp.StatusCode, bytes.TrimSpace(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", &transientError{inner: fmt.Errorf("cryptopanic: read body: %w", err)}
	}
	return body, csrf, nil
}

// decodeEnvelope parses + decrypts the response body into []rawPost.
// csrf is the token that was sent with the POST; we use it to derive the
// IV exactly the way CryptoPanic's JS does.
func decodeEnvelope(body []byte, csrf string) ([]rawPost, error) {
	var env encryptedResponse
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, fmt.Errorf("cryptopanic: decode envelope: %w", err)
	}
	if env.S == "" {
		// Some error responses return status:false with a plaintext code.
		if env.Code != "" {
			return nil, fmt.Errorf("cryptopanic: server code %q", env.Code)
		}
		return nil, fmt.Errorf("cryptopanic: empty encrypted payload")
	}

	iv := buildIV(ivPrefixPosts, csrf)
	plaintext, err := decryptPosts(iv, env.S)
	if err != nil {
		return nil, err
	}
	return decodeDictList(plaintext)
}

// ensureCSRF performs a GET / when we have no fresh csrftoken cookie.
func (c *Client) ensureCSRF(ctx context.Context) error {
	c.mu.Lock()
	fresh := time.Now().Before(c.csrfExpiry) && c.csrfToken() != ""
	c.mu.Unlock()
	if fresh {
		return nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base()+homePath, nil)
	if err != nil {
		return fmt.Errorf("cryptopanic: build GET /: %w", err)
	}
	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("cryptopanic: GET /: %w", err)
	}
	// Drain and close to let the connection be reused.
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	if resp.StatusCode == http.StatusForbidden {
		// Cloudflare anti-bot block on the bootstrap GET. There is no CSRF
		// token to refresh and retrying the same HTTP client can't clear a
		// fingerprint challenge — tag the status so FetchPosts surfaces
		// ErrAntiBot and the FallbackFeed routes to the headless browser.
		return &transientError{
			status: resp.StatusCode,
			inner:  fmt.Errorf("cryptopanic: home GET status %d (anti-bot)", resp.StatusCode),
		}
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("cryptopanic: home GET status %d", resp.StatusCode)
	}

	c.mu.Lock()
	c.csrfExpiry = time.Now().Add(csrfTTL)
	c.mu.Unlock()
	return nil
}

func (c *Client) csrfToken() string {
	u, err := url.Parse(c.base())
	if err != nil {
		return ""
	}
	for _, cookie := range c.http.Jar.Cookies(u) {
		if cookie.Name == csrfCookie {
			return cookie.Value
		}
	}
	return ""
}

func (c *Client) invalidateCSRF() {
	c.mu.Lock()
	c.csrfExpiry = time.Time{}
	c.mu.Unlock()
}

// cooldownRemaining reports whether a rate-limit cooldown is active as
// of now, and if so how much of the window is left. Safe for concurrent
// callers — FetchPosts reads it on every entry and applyCooldown writes
// it on every 429.
func (c *Client) cooldownRemaining(now time.Time) (time.Duration, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cooldownUntil.IsZero() || !now.Before(c.cooldownUntil) {
		return 0, false
	}
	return c.cooldownUntil.Sub(now), true
}

// applyCooldown arms the rate-limit window. retryAfter comes from the
// Retry-After header (zero when absent/invalid) — in that case we use
// defaultRateLimitCooldown. The window is capped at maxRateLimitCooldown
// so a hostile upstream can't freeze us for hours.
func (c *Client) applyCooldown(retryAfter time.Duration, status int) {
	d := retryAfter
	if d <= 0 {
		d = defaultRateLimitCooldown
	}
	if d > maxRateLimitCooldown {
		d = maxRateLimitCooldown
	}
	c.mu.Lock()
	c.cooldownUntil = time.Now().Add(d)
	c.lastBlockCode = status
	c.mu.Unlock()
}

func (c *Client) respectRate() {
	c.mu.Lock()
	wait := time.Until(c.lastCall.Add(minInterval))
	c.mu.Unlock()
	if wait > 0 {
		time.Sleep(wait)
	}
}

func (c *Client) markCalled() {
	c.mu.Lock()
	c.lastCall = time.Now()
	c.mu.Unlock()
}

// buildFormBody constructs the application/x-www-form-urlencoded body the
// SPA sends. Filters are JSON-encoded inside the `filters` form field —
// exactly what the JS does via this.getFormData({filters: JSON.stringify(t)}).
func buildFormBody(q Query) string {
	filters := map[string]any{"public": true}
	if q.Filter != "" {
		filters["filter"] = q.Filter
	} else {
		filters["filter"] = "hot"
	}
	if q.Kind != "" {
		filters["kind"] = q.Kind
	} else {
		filters["kind"] = "news"
	}
	if len(q.Currencies) > 0 {
		filters["currencies"] = strings.Join(q.Currencies, ",")
	}
	if len(q.Regions) > 0 {
		filters["regions"] = strings.Join(q.Regions, ",")
	}

	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(filters)
	// json.Encoder adds a trailing newline; trim it.
	fj := strings.TrimRight(b.String(), "\n")

	form := url.Values{}
	form.Set("filters", fj)
	return form.Encode()
}

// parseRetryAfter interprets the Retry-After header as either a delta-seconds
// integer or an HTTP-date. Returns 0 when the header is absent or malformed.
func parseRetryAfter(h string) time.Duration {
	if h == "" {
		return 0
	}
	if secs, err := strconv.Atoi(strings.TrimSpace(h)); err == nil && secs >= 0 {
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(h); err == nil {
		if d := time.Until(t); d > 0 {
			return d
		}
	}
	return 0
}

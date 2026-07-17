package search

import (
	"errors"
	"fmt"
	stdhttp "net/http"
	"strconv"
	"strings"
	"time"
)

// ErrRateLimited is the sentinel for a tracker rate-limiting harbrr (HTTP 429 or
// 503). The registry classifies it into a `rate_limited` health event. It is
// minted both at the doRequest non-2xx boundary (a plain Doer returning 429/503)
// and by the registry's paced client after it exhausts its bounded 429/503 retry.
var ErrRateLimited = errors.New("tracker rate-limited the request")

// RateLimitedError carries the status and the honored Retry-After so callers can
// surface it without re-parsing. It deliberately carries no URL — the caller
// wraps it with a redacted URL — so it can never leak a passkey.
type RateLimitedError struct {
	StatusCode int
	RetryAfter time.Duration
}

func (e *RateLimitedError) Error() string {
	if e.RetryAfter > 0 {
		return fmt.Sprintf("tracker rate-limited (HTTP %d, retry after %s)", e.StatusCode, e.RetryAfter)
	}
	return fmt.Sprintf("tracker rate-limited (HTTP %d)", e.StatusCode)
}

// Unwrap lets errors.Is(err, ErrRateLimited) match a *RateLimitedError anywhere
// in a wrapped chain, so the registry classifier needs only the sentinel.
func (e *RateLimitedError) Unwrap() error { return ErrRateLimited }

// IsRateLimitStatus reports whether a status code is one harbrr backs off on
// (429 Too Many Requests / 503 Service Unavailable). Other non-2xx codes are
// not retried — they are genuine failures, not pacing signals.
func IsRateLimitStatus(code int) bool {
	return code == stdhttp.StatusTooManyRequests || code == stdhttp.StatusServiceUnavailable
}

// ErrGatewayStatus is the sentinel for a reverse-proxy/CDN reporting the origin
// unreachable (502 Bad Gateway, 504 Gateway Timeout, 522 Connection Timed Out —
// the last a Cloudflare-specific extension many trackers sit behind). The
// registry classifies it as a TRANSPORT health event (autobrr/harbrr#247): the
// tracker itself never answered, which is the same "down" signal as a refused
// connection, just observed one hop closer via the gateway's own error page.
// Deliberately narrow — 429/503 are rate-limit codes (already handled above,
// never reach here), 401/403 are auth, and other 4xx/5xx (404/500...) are the
// tracker itself answering, not a gateway outage, so they stay unclassified.
var ErrGatewayStatus = errors.New("gateway reported the origin unreachable")

// IsGatewayStatus reports whether code is one of the gateway/bad-upstream
// statuses ErrGatewayStatus covers.
func IsGatewayStatus(code int) bool {
	return code == stdhttp.StatusBadGateway || code == stdhttp.StatusGatewayTimeout || code == 522
}

// maxRetryAfter caps how long a Retry-After can hold a request, so a hostile or
// misconfigured tracker can't park harbrr for hours.
const maxRetryAfter = 5 * time.Minute

// ParseRetryAfter parses an HTTP Retry-After header value (delta-seconds or an
// HTTP-date), clamped to [0, maxRetryAfter]. An empty/unparseable value returns
// 0 (the caller falls back to its own backoff). now supplies the reference time
// for the HTTP-date form (injectable for deterministic tests); nil uses time.Now.
func ParseRetryAfter(value string, now func() time.Time) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	if d, ok := parseDeltaSeconds(value); ok {
		return d
	}
	if t, err := stdhttp.ParseTime(value); err == nil {
		if now == nil {
			now = time.Now
		}
		return clampRetryAfter(t.Sub(now()))
	}
	return 0
}

// parseDeltaSeconds parses the delta-seconds form of Retry-After, clamped to
// [0, maxRetryAfter]. The bool reports whether value WAS the numeric form, so a
// numeric value never falls through to the caller's HTTP-date branch. A value made
// entirely of digits but too large for int (strconv.ErrRange) is clamped to the cap
// rather than treated as 0 — a 0 would mean "retry immediately" against a tracker
// that explicitly asked for a long backoff (fail safe toward MORE backoff).
func parseDeltaSeconds(value string) (time.Duration, bool) {
	secs, err := strconv.Atoi(value)
	if err != nil {
		if errors.Is(err, strconv.ErrRange) {
			// Valid digits, out of int range: a huge positive magnitude → the cap;
			// a huge negative is meaningless for a delay → 0. Either way it was numeric.
			if strings.HasPrefix(value, "-") {
				return 0, true
			}
			return maxRetryAfter, true
		}
		return 0, false // not a number — let the caller try the HTTP-date form
	}
	if secs <= 0 {
		return 0, true
	}
	// Clamp before the multiply: a large-but-in-int secs would overflow
	// time.Duration (int64 ns) and wrap to a bogus/negative value.
	if secs >= int(maxRetryAfter/time.Second) {
		return maxRetryAfter, true
	}
	return clampRetryAfter(time.Duration(secs) * time.Second), true
}

func clampRetryAfter(d time.Duration) time.Duration {
	switch {
	case d <= 0:
		return 0
	case d > maxRetryAfter:
		return maxRetryAfter
	default:
		return d
	}
}

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
	if secs, err := strconv.Atoi(value); err == nil {
		// Clamp before the multiply: a huge delta-seconds would overflow
		// time.Duration (int64 ns) and wrap to a bogus/negative value.
		if secs <= 0 {
			return 0
		}
		if secs >= int(maxRetryAfter/time.Second) {
			return maxRetryAfter
		}
		return clampRetryAfter(time.Duration(secs) * time.Second)
	}
	if t, err := stdhttp.ParseTime(value); err == nil {
		if now == nil {
			now = time.Now
		}
		return clampRetryAfter(t.Sub(now()))
	}
	return 0
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

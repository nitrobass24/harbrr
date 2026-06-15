package registry

import (
	"errors"
	"fmt"
	"io"
	stdhttp "net/http"
	"net/url"
	"sync"
	"time"

	retry "github.com/avast/retry-go/v4"
	"golang.org/x/time/rate"

	apphttp "github.com/autobrr/harbrr/internal/http"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
)

// defaultRateInterval is the per-host minimum spacing between outbound requests
// when a definition declares no requestDelay. It bounds blast radius against a
// tracker's anti-abuse without being noticeable for a typical single-query search.
const defaultRateInterval = 1 * time.Second

// maxRetryAttempts bounds the 429/503 retry so a persistently rate-limited tracker
// surfaces a typed error instead of looping.
const maxRetryAttempts = 3

// retryBackoff is the base delay between 429/503 retries when the response carries
// no usable Retry-After.
const retryBackoff = 500 * time.Millisecond

// hostLimiters holds one rate.Limiter per tracker host, process-wide. The key
// space is bounded by the set of configured tracker hosts, so the map cannot grow
// unboundedly and needs no eviction (eviction would also race a concurrent Wait).
var hostLimiters sync.Map // map[string]*rate.Limiter

// limiterFor returns the shared limiter for host, creating it (interval spacing,
// burst 1) on first use. LoadOrStore makes concurrent first-creation safe.
func limiterFor(host string, interval time.Duration) *rate.Limiter {
	if interval <= 0 {
		interval = defaultRateInterval
	}
	if v, ok := hostLimiters.Load(host); ok {
		if lim, ok := v.(*rate.Limiter); ok {
			return lim
		}
	}
	v, _ := hostLimiters.LoadOrStore(host, rate.NewLimiter(rate.Every(interval), 1))
	lim, _ := v.(*rate.Limiter)
	return lim
}

// pacedDoer wraps a base Doer with per-host rate limiting and bounded 429/503
// backoff. Pacing + backoff both honor the request's context (threaded in PR #1):
// the per-host token Wait and each backoff sleep abort promptly on cancellation,
// and the per-request deadline bounds the SUM of waits + sleeps + attempts.
type pacedDoer struct {
	base     search.Doer
	interval time.Duration
	attempts uint
	backoff  time.Duration
	now      func() time.Time
	// limiter is the per-host limiter lookup, injectable in tests (defaults to the
	// process-wide map).
	limiter func(host string) *rate.Limiter
	// timer is retry-go's sleep seam, injectable in tests for deterministic backoff;
	// nil uses retry-go's real-time default.
	timer retry.Timer
}

// newPacedDoer wraps base so every request is per-host paced and 429/503-backed-off.
func newPacedDoer(base search.Doer, interval time.Duration) *pacedDoer {
	d := &pacedDoer{
		base:     base,
		interval: interval,
		attempts: maxRetryAttempts,
		backoff:  retryBackoff,
		now:      time.Now,
	}
	d.limiter = func(host string) *rate.Limiter { return limiterFor(host, d.interval) }
	return d
}

// rateLimitSignalError is the internal retry trigger carrying the parsed
// Retry-After so the delay function can honor it. It never escapes Do.
type rateLimitSignalError struct {
	status int
	after  time.Duration
}

func (e *rateLimitSignalError) Error() string { return "rate limited" }

func isRateLimitSignal(err error) bool {
	var e *rateLimitSignalError
	return errors.As(err, &e)
}

// Do paces by host, issues the request, and retries 429/503 (bounded, honoring
// Retry-After) before surfacing a typed search.RateLimitedError.
func (d *pacedDoer) Do(req *stdhttp.Request) (*stdhttp.Response, error) {
	lim := d.limiter(req.URL.Hostname())
	var out *stdhttp.Response

	opts := []retry.Option{
		retry.Attempts(d.attempts),
		retry.Context(req.Context()),
		retry.RetryIf(isRateLimitSignal),
		retry.DelayType(d.delay),
		retry.LastErrorOnly(true),
	}
	if d.timer != nil {
		opts = append(opts, retry.WithTimer(d.timer))
	}

	rerr := retry.Do(func() error {
		// Re-acquire a token every attempt (never retry token-free, or we defeat the
		// rate limit). A cancelled ctx aborts the Wait promptly.
		if err := lim.Wait(req.Context()); err != nil {
			return fmt.Errorf("rate limiter wait: %w", err)
		}
		if err := resetBody(req); err != nil {
			return err
		}
		resp, err := d.base.Do(req)
		if err != nil {
			return redactDoErr(err)
		}
		if search.IsRateLimitStatus(resp.StatusCode) {
			after := search.ParseRetryAfter(resp.Header.Get("Retry-After"), d.now)
			drainClose(resp.Body)
			return &rateLimitSignalError{status: resp.StatusCode, after: after}
		}
		out = resp
		return nil
	}, opts...)

	if rerr != nil {
		// A cancelled/expired ctx wins, with its identity preserved for callers.
		if cerr := req.Context().Err(); cerr != nil {
			return nil, fmt.Errorf("registry: request aborted: %w", cerr)
		}
		var s *rateLimitSignalError
		if errors.As(rerr, &s) {
			// Bounded retry exhausted on 429/503 — surface the typed error the
			// registry classifies as rate_limited.
			return nil, &search.RateLimitedError{StatusCode: s.status, RetryAfter: s.after}
		}
		return nil, fmt.Errorf("registry: %w", rerr) // transport error (not retried)
	}
	return out, nil
}

// delay honors a server Retry-After when present, else a fixed base backoff.
func (d *pacedDoer) delay(_ uint, err error, _ *retry.Config) time.Duration {
	var s *rateLimitSignalError
	if errors.As(err, &s) && s.after > 0 {
		return s.after
	}
	return d.backoff
}

// resetBody restores a consumed request body before a retry (GetBody is set by
// the stdlib for the *strings.Reader bodies login/search build). A bodyless GET
// is a no-op.
func resetBody(req *stdhttp.Request) error {
	if req.Body == nil || req.GetBody == nil {
		return nil
	}
	body, err := req.GetBody()
	if err != nil {
		return fmt.Errorf("reset request body: %w", err)
	}
	req.Body = body
	return nil
}

// redactDoErr scrubs a transport error of any embedded URL secret. The stdlib
// *url.Error stringifies the full request URL (query and all) into its message, so
// rebuild a redacted form rather than risk leaking a passkey through a wrapped
// "Get \"...?passkey=...\"" error.
func redactDoErr(err error) error {
	var uerr *url.Error
	if errors.As(err, &uerr) {
		return fmt.Errorf("%s %s: %w", uerr.Op, apphttp.RedactURL(uerr.URL), uerr.Err)
	}
	return fmt.Errorf("request failed: %w", err)
}

// drainClose discards (bounded) and closes a retried response body so the
// connection can be reused; the body is never results, so dropping it is safe.
func drainClose(rc io.ReadCloser) {
	if rc == nil {
		return
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(rc, 1<<16))
	_ = rc.Close()
}

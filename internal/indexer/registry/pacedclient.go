package registry

import (
	"context"
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

// maxPacingBudget caps the CUMULATIVE wall-clock a single Do may spend across all
// per-host rate waits and 429/503 backoff sleeps, even when the inbound context
// carries no deadline. Without it a hostile tracker could pin a goroutine for an
// attacker-chosen Retry-After (× attempts). A shorter inbound deadline still wins —
// context.WithTimeout takes the minimum of the two — so this only adds a ceiling.
const maxPacingBudget = 60 * time.Second

// hostLimiters holds one rate.Limiter per tracker host, process-wide. The key
// space is bounded by the set of configured tracker hosts, so the map cannot grow
// unboundedly and needs no eviction (eviction would also race a concurrent Wait).
var hostLimiters sync.Map // map[string]*rate.Limiter

// limiterFor returns the shared limiter for host, creating it (interval spacing,
// burst 1) on first use. LoadOrStore makes concurrent first-creation safe. When a
// limiter already exists for the host, the STRICTEST (slowest) interval wins: a
// later instance on the same host that wants slower pacing tightens the shared
// limiter; we never speed an existing one up (the host is the anti-blacklist unit).
func limiterFor(host string, interval time.Duration) *rate.Limiter {
	if interval <= 0 {
		interval = defaultRateInterval
	}
	want := rate.Every(interval)
	v, loaded := hostLimiters.LoadOrStore(host, rate.NewLimiter(want, 1))
	lim, _ := v.(*rate.Limiter)
	if loaded && want < lim.Limit() {
		// rate.Limit is events/sec, so a smaller value is a slower (stricter) rate.
		// SetLimit is concurrency-safe (no race with a concurrent Wait).
		lim.SetLimit(want)
	}
	return lim
}

// pacedDoer wraps a base Doer with per-host rate limiting and bounded 429/503
// backoff. Pacing + backoff both honor the request's context (threaded in PR #1):
// the per-host token Wait and each backoff sleep abort promptly on cancellation.
// Do derives a budget-bounded context (min of any inbound deadline and budget) and
// uses it for the rate Wait and the retry's inter-attempt sleeps, so the SUM of
// waits + sleeps is bounded even for a deadline-less request — a hostile Retry-After
// can never pin the goroutine. The budget bounds ONLY the waits/sleeps, not the live
// HTTP call, whose response body must outlive Do (so base.Do keeps the inbound ctx).
type pacedDoer struct {
	base     search.Doer
	interval time.Duration
	attempts uint
	backoff  time.Duration
	// budget caps the cumulative pacing waits + 429/503 backoff sleeps for one Do;
	// defaults to maxPacingBudget, shrinkable in tests.
	budget time.Duration
	now    func() time.Time
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
		budget:   maxPacingBudget,
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
	// Bound the CUMULATIVE rate waits + 429/503 backoff sleeps. context.WithTimeout
	// takes the minimum of any inbound deadline and the budget, so a caller deadline
	// still wins while a deadline-less request can't be pinned by a hostile Retry-After.
	// This bounds ONLY the waits/sleeps — base.Do keeps the inbound request context so
	// a successful response body still outlives Do (cancelling waitCtx on return is safe).
	waitCtx, cancel := context.WithTimeout(req.Context(), d.budget)
	defer cancel()

	lim := d.limiter(req.URL.Hostname())
	var out *stdhttp.Response

	opts := []retry.Option{
		retry.Attempts(d.attempts),
		retry.Context(waitCtx),
		retry.RetryIf(isRateLimitSignal),
		retry.DelayType(d.delay),
		retry.LastErrorOnly(true),
	}
	if d.timer != nil {
		opts = append(opts, retry.WithTimer(d.timer))
	}

	attempt := 0
	rerr := retry.Do(func() error {
		retrying := attempt > 0
		attempt++
		// Re-acquire a token every attempt (never retry token-free, or we defeat the
		// rate limit). A cancelled/expired waitCtx aborts the Wait promptly.
		if err := lim.Wait(waitCtx); err != nil {
			return fmt.Errorf("rate limiter wait: %w", err)
		}
		// Restore the (consumed) body only when actually retrying; a non-replayable
		// body fails loud there rather than silently re-sending an empty one.
		if retrying {
			if err := resetBody(req); err != nil {
				return err
			}
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
		// A cancelled/expired ctx wins, with its identity preserved for callers. waitCtx
		// reflects both an inbound cancel (context.Canceled) and a budget/deadline expiry
		// (context.DeadlineExceeded), so the bounded sum surfaces a typed abort either way.
		if cerr := waitCtx.Err(); cerr != nil {
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
	if req.Body == nil {
		return nil // bodyless (e.g. GET) — nothing to restore
	}
	if req.GetBody == nil {
		// The stdlib sets GetBody for the *strings.Reader bodies login/search build,
		// so this is defensive: a body without GetBody cannot be replayed for a retry.
		return errors.New("registry: request body is not replayable for a retry (no GetBody)")
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

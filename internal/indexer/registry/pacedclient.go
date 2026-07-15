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

	"github.com/rs/zerolog"
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

// limiterTightenMu serializes the read-compare-set in limiterFor so the strictest
// (slowest) interval always wins a race between concurrent creators on one host.
var limiterTightenMu sync.Mutex

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
		// Serialize the read-compare-set: two racing creators could each read the
		// same pre-tighten Limit and the LOOSER SetLimit land last, losing the
		// strictest value. Re-check under the lock so only the strictest survives.
		// SetLimit is itself safe vs a concurrent Wait, so this guards only the
		// compare-and-set — never the hot pacing path.
		limiterTightenMu.Lock()
		if want < lim.Limit() {
			lim.SetLimit(want)
		}
		limiterTightenMu.Unlock()
	}
	return lim
}

// pacedDoer wraps a base Doer with per-host rate limiting and bounded 429/503
// backoff. The pacing budget bounds ONLY the cumulative rate-limiter waits + backoff
// sleeps (debited per wait/sleep), so a hostile Retry-After can never pin a goroutine.
// The live base.Do runs on the INBOUND request context (with the client's own
// timeout), NOT the budget — so a slow 429/503 response never consumes the budget
// before pacing starts. Every wait/sleep also honors the caller context, so a caller
// cancel aborts promptly (and only a caller cancel is a "request aborted").
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
	// timer is the backoff sleep seam, injectable in tests for deterministic backoff;
	// nil uses the real time.After.
	timer backoffTimer
	// log traces each outbound request (method/redacted-URL/status/duration) at debug.
	// A Nop logger (the registry default) makes Debug()/Trace() zero-cost no-ops.
	log zerolog.Logger
}

// newPacedDoer wraps base so every request is per-host paced and 429/503-backed-off.
func newPacedDoer(base search.Doer, interval time.Duration, log zerolog.Logger) *pacedDoer {
	d := &pacedDoer{
		base:     base,
		interval: interval,
		attempts: maxRetryAttempts,
		backoff:  retryBackoff,
		budget:   maxPacingBudget,
		now:      time.Now,
		log:      log,
	}
	d.limiter = func(host string) *rate.Limiter { return limiterFor(host, d.interval) }
	return d
}

// backoffTimer is the injectable sleep seam for deterministic backoff in tests;
// nil uses the real time.After.
type backoffTimer interface {
	After(time.Duration) <-chan time.Time
}

// rateLimitInfo remembers a 429/503 status + parsed Retry-After so Do can surface a
// typed RateLimitedError once the bounded retry (or a budget-capped backoff) ends.
type rateLimitInfo struct {
	status int
	after  time.Duration
}

// Do paces by host, issues the request, and retries 429/503 (bounded, honoring
// Retry-After) before surfacing a typed search.RateLimitedError. The pacing budget
// bounds ONLY the cumulative rate-limiter waits + backoff sleeps (debited per
// wait/sleep); the live base.Do runs on the inbound request context (with the client's
// own timeout), so a slow 429/503 response never consumes the budget before pacing
// starts. "request aborted" is reserved for a genuine caller cancel/deadline.
// CookieJar reports the wrapped *http.Client's cookie jar (search.JarOwner), so
// the engine seeds login cookies into the SAME jar the transport applies —
// keeping a single jar on the wire. Nil when the base is not an *http.Client
// (test fakes), meaning no jar is managed.
func (d *pacedDoer) CookieJar() stdhttp.CookieJar {
	if c, ok := d.base.(*stdhttp.Client); ok {
		return c.Jar
	}
	return nil
}

func (d *pacedDoer) Do(req *stdhttp.Request) (*stdhttp.Response, error) {
	ctx := req.Context() // caller ctx: bounds base.Do + is the ONLY source of "request aborted"
	lim := d.limiter(req.URL.Hostname())
	remaining := d.budget // caps ONLY cumulative lim.Wait + backoff sleeps, never base.Do

	var lastRL *rateLimitInfo
	for attempt := uint(0); attempt < d.attempts; attempt++ {
		r := d.issue(ctx, req, lim, attempt, &remaining, lastRL)
		if r.err != nil {
			return nil, r.err
		}
		if r.resp != nil {
			return r.resp, nil
		}
		lastRL = r.rl // a 429/503 was seen
		if attempt+1 >= d.attempts {
			break // attempts exhausted
		}
		if err := d.backoffSleep(ctx, r.rl, &remaining); err != nil {
			if ctx.Err() != nil {
				return nil, fmt.Errorf("registry: request aborted: %w", ctx.Err())
			}
			break // budget capped the backoff — surface the rate-limited error below
		}
	}

	if lastRL == nil {
		return nil, errors.New("registry: paced doer made no attempts") // only with attempts == 0
	}
	return nil, &search.RateLimitedError{StatusCode: lastRL.status, RetryAfter: lastRL.after}
}

// attemptResult is the outcome of one issue(): exactly one field is set — resp (a
// terminal success/passthrough), rl (a 429/503 to maybe retry), or err (a terminal
// error, already fully wrapped for the caller to return as-is).
type attemptResult struct {
	resp *stdhttp.Response
	rl   *rateLimitInfo
	err  error
}

// issue restores the body (on a retry), waits for a rate-limiter token from the
// remaining budget, sends the request on the caller context, and classifies the
// outcome. It debits *remaining by the time spent waiting.
func (d *pacedDoer) issue(ctx context.Context, req *stdhttp.Request, lim *rate.Limiter, attempt uint, remaining *time.Duration, lastRL *rateLimitInfo) attemptResult {
	// Restore the (consumed) body only when actually retrying; a non-replayable body
	// fails loud rather than silently re-sending an empty one.
	if attempt > 0 {
		if err := resetBody(req); err != nil {
			return attemptResult{err: err}
		}
	}
	// Re-acquire a token every attempt (never retry token-free, or we defeat the rate
	// limit), drawn from the remaining pacing budget.
	spent, err := d.pacedWait(ctx, lim, *remaining)
	*remaining -= spent
	if err != nil {
		return attemptResult{err: d.classifyWaitErr(ctx, err, lastRL)}
	}
	start := d.now()
	resp, derr := d.base.Do(req) //nolint:bodyclose // resp is returned to the caller (attemptResult.resp), which closes the body; the rate-limit path drainCloses it below

	dur := d.now().Sub(start)
	// Log only scheme://host, never the path/query: a native driver can hide its download
	// secret in a PATH segment (beyond-hd's api_key/rsskey, animebytes' passkey) that
	// RedactURL's length heuristic misses, so logging the full RedactURL'd URL here would
	// leak it. SchemeHost drops the path/query entirely — safe for every driver.
	hostURL := apphttp.SchemeHost(req.URL.String())
	if derr != nil {
		// Trace the failing host + a host-only redacted cause (SafeTransportDetail drops the
		// secret-bearing path/query) so "turn on trace" reveals what failed without leaking.
		d.log.Trace().
			Str("method", req.Method).
			Str("url", hostURL).
			Dur("duration", dur).
			Str("error", transportErrText(derr)).
			Msg("registry: outbound request failed")
		// A transport failure has no HTTP status; still surface the redacted query so a
		// failing request can be debugged from what it asked for, like the success path.
		d.traceQuery(req, 0, dur)
		return attemptResult{err: fmt.Errorf("registry: %w", redactDoErr(derr))}
	}
	ev := d.log.Debug().
		Str("method", req.Method).
		Str("url", hostURL).
		Int("status", resp.StatusCode).
		Dur("duration", dur)
	if ra := resp.Header.Get("Retry-After"); ra != "" {
		ev = ev.Str("retry_after", ra)
	}
	ev.Msg("registry: outbound request")
	d.traceQuery(req, resp.StatusCode, dur)
	if !search.IsRateLimitStatus(resp.StatusCode) {
		return attemptResult{resp: resp}
	}
	after := search.ParseRetryAfter(resp.Header.Get("Retry-After"), d.now)
	drainClose(resp.Body)
	return attemptResult{rl: &rateLimitInfo{status: resp.StatusCode, after: after}}
}

// traceQuery emits, one level below the DEBUG request log, the outbound request's
// redacted query — the search diagnostics (keywords, categories, sort, paging) the
// host-only DEBUG line omits — so a tracker definition can be debugged from what it
// actually asked for. HostAndRedactedQuery drops the path (preserving the DEBUG
// line's scheme://host-only safety against a passkey in a path segment) and masks
// secret-named params. A status <= 0 (a transport failure, no HTTP status) omits the
// status field. Guarded so the redaction cost is paid only when trace is on.
func (d *pacedDoer) traceQuery(req *stdhttp.Request, status int, dur time.Duration) {
	if d.log.GetLevel() > zerolog.TraceLevel {
		return
	}
	ev := d.log.Trace().
		Str("method", req.Method).
		Str("url", apphttp.HostAndRedactedQuery(req.URL.String())).
		Dur("duration", dur)
	if status > 0 {
		ev = ev.Int("status", status)
	}
	ev.Msg("registry: outbound request query")
}

// classifyWaitErr turns a pacing-wait failure into the surfaced error: a genuine caller
// cancel/deadline is a "request aborted"; a budget expiry with a pending 429/503
// surfaces the rate-limited error; a bare budget expiry is a pacing timeout.
func (d *pacedDoer) classifyWaitErr(ctx context.Context, err error, lastRL *rateLimitInfo) error {
	if ctx.Err() != nil {
		return fmt.Errorf("registry: request aborted: %w", ctx.Err())
	}
	if lastRL != nil {
		return &search.RateLimitedError{StatusCode: lastRL.status, RetryAfter: lastRL.after}
	}
	return fmt.Errorf("registry: pacing budget exhausted: %w", err)
}

// backoffSleep waits out the retry backoff (Retry-After when present, else the base
// delay), drawn from the remaining pacing budget; it debits *remaining by the time
// slept and returns an error on a caller cancel or a budget cap.
func (d *pacedDoer) backoffSleep(ctx context.Context, rl *rateLimitInfo, remaining *time.Duration) error {
	delay := d.backoff
	if rl.after > 0 {
		delay = rl.after
	}
	slept, err := d.pacedSleep(ctx, delay, *remaining)
	*remaining -= slept
	return err
}

// pacedWait blocks for a rate-limiter token, bounded by BOTH the caller ctx and the
// remaining pacing budget. It returns how long it waited (to debit the budget) and any
// error; the caller reads ctx.Err() to tell a budget expiry from a caller cancel.
func (d *pacedDoer) pacedWait(ctx context.Context, lim *rate.Limiter, remaining time.Duration) (time.Duration, error) {
	if remaining <= 0 {
		return 0, context.DeadlineExceeded
	}
	wctx, cancel := context.WithTimeout(ctx, remaining)
	defer cancel()
	start := d.now()
	err := lim.Wait(wctx)
	return d.now().Sub(start), err
}

// pacedSleep waits out a backoff delay via the injectable timer (real time.After when
// nil), bounded by BOTH the caller ctx and the remaining pacing budget. It returns how
// long it slept (to debit the budget) and any error (a budget cap or a caller cancel).
func (d *pacedDoer) pacedSleep(ctx context.Context, delay, remaining time.Duration) (time.Duration, error) {
	if remaining <= 0 {
		return 0, context.DeadlineExceeded
	}
	after := time.After
	if d.timer != nil {
		after = d.timer.After
	}
	budget := time.NewTimer(remaining)
	defer budget.Stop()
	start := d.now()
	select {
	case <-after(delay):
		return d.now().Sub(start), nil
	case <-budget.C:
		return remaining, context.DeadlineExceeded
	case <-ctx.Done():
		return d.now().Sub(start), ctx.Err()
	}
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

// transportErrText renders a host-only, secret-safe cause for the trace log. A transport
// failure is a *url.Error, which SafeTransportDetail reduces to "<op> <scheme>://<host>:
// <cause>" (dropping the secret-bearing path/query); a non-*url.Error yields the empty
// string, for which a fixed label stands in so the field is never a raw error.
func transportErrText(err error) string {
	if detail := apphttp.SafeTransportDetail(err); detail != "" {
		return detail
	}
	return "transport error"
}

// redactDoErr scrubs a transport error of any embedded URL secret before it is returned
// (an upstream log.Error().Err(err) must not be able to leak it). The stdlib *url.Error
// stringifies the full request URL (path AND query) into its message, so rebuild a
// host-only form: only scheme://host is kept. RedactURL alone is not enough here — it
// misses a secret hidden in a URL PATH segment (a native driver's api_key/rsskey/passkey)
// that its length heuristic does not match — so, like the trace log, the path/query are
// dropped entirely. The inner cause (uerr.Err) carries no URL and is preserved via %w.
//
// The rebuilt host-only error is marked via apphttp.MarkHostRedacted: cardigann's
// search layer wraps whatever the Doer returns with its own method+SchemeHost prefix
// as a fallback for a plain, non-registry Doer (see request.go's wrapDoErr) — without
// the marker that wrap would re-prepend the same host this function already printed,
// logging it twice (autobrr/harbrr#181). The "request failed" fallback below adds no
// host prefix of its own, so it is left unmarked.
func redactDoErr(err error) error {
	var uerr *url.Error
	if errors.As(err, &uerr) {
		hostOnly := fmt.Errorf("%s %s: %w", uerr.Op, apphttp.SchemeHost(uerr.URL), uerr.Err)
		return apphttp.MarkHostRedacted(hostOnly) //nolint:wrapcheck // hostOnly is already the fully-shaped, host-redacted transport error; the marker is a transparent Error()/Unwrap() passthrough, and re-wrapping would only add noise
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

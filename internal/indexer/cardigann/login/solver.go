package login

import (
	"context"
	"errors"
	"fmt"
	stdhttp "net/http"
	"net/url"
)

// Solver is the pluggable anti-bot solver seam. Given the URL of a page guarded
// by an interstitial (e.g. a Cloudflare challenge), it returns the cookies — and
// optionally a User-Agent — that let a subsequent request through. This is the
// fetch/auth-matrix extension point: NoopSolver solves nothing (the default),
// ManualCookieSolver replays a user-supplied cookie, and FlareSolverrSolver clears
// a Cloudflare-style challenge via a FlareSolverr instance.
type Solver interface {
	Solve(ctx context.Context, targetURL string) (SolveResult, error)
}

// SolveResult carries what a solver produced for the target page.
type SolveResult struct {
	Cookies   []*stdhttp.Cookie
	UserAgent string
}

// ErrNoSolverConfigured reports that no anti-bot solver is configured (or the
// configured one had nothing to contribute), so an interstitial cannot be solved
// automatically. The login flow surfaces this as ErrSolverRequired.
var ErrNoSolverConfigured = errors.New("login: no anti-bot solver configured")

// NoopSolver is the default solver: it solves nothing, preserving the fail-loud
// ErrSolverRequired behaviour for indexers without a configured solver.
type NoopSolver struct{}

// Solve always declines.
func (NoopSolver) Solve(context.Context, string) (SolveResult, error) {
	return SolveResult{}, ErrNoSolverConfigured
}

// ManualCookieSolver replays a user-supplied Cookie-header value (e.g. a
// cf_clearance cookie pasted after solving a challenge in a browser, or a
// 2FA-gated session cookie). It needs no external service, so it covers the
// manual half of the fetch/auth matrix in environments without FlareSolverr.
type ManualCookieSolver struct {
	// Cookie is a raw Cookie-header string ("a=1; b=2"), already decrypted by the
	// registry from the instance's encrypted "cookie" setting.
	Cookie string
}

// Solve parses the configured cookie header into cookies for the target. An
// empty/blank cookie declines (ErrNoSolverConfigured), so a mis-configured
// instance fails loud rather than silently sending no cookies.
func (m ManualCookieSolver) Solve(context.Context, string) (SolveResult, error) {
	cookies := parseCookieHeader(m.Cookie)
	if len(cookies) == 0 {
		return SolveResult{}, ErrNoSolverConfigured
	}
	return SolveResult{Cookies: cookies}, nil
}

// solver returns the configured solver, defaulting to NoopSolver when unset so
// callers never need a nil check.
func (e *Executor) solver() Solver {
	if e.Solver == nil {
		return NoopSolver{}
	}
	return e.Solver
}

// fetchLandingPastAntiBot GETs rawURL and, when the response is an anti-bot
// interstitial, asks the configured solver for cookies ONCE, seeds them, and
// retries the GET. With the default NoopSolver it preserves the original
// fail-loud ErrSolverRequired behaviour. A page that is still challenged after a
// solve also fails loud — never a loop.
func (e *Executor) fetchLandingPastAntiBot(ctx context.Context, rawURL string, headers map[string][]string) ([]byte, error) {
	body, _, err := e.get(ctx, rawURL, headers)
	if err != nil {
		return nil, err
	}
	if detectAntiBot(body) == nil {
		return body, nil
	}
	res, serr := e.solver().Solve(ctx, rawURL)
	if serr != nil {
		// No usable solver: preserve the existing anti-bot signal (names the
		// detector only, never page bytes).
		return nil, fmt.Errorf("%w: detected an anti-bot challenge page", ErrSolverRequired)
	}
	e.applySolveResult(rawURL, res)
	// Anti-bot clearance is UA-coupled (cf_clearance is bound to the solver's
	// User-Agent) AND a gzip-only header set is a known 403 trigger, so the replay
	// carries the solver's UA plus a browser-realistic Accept/Accept-Encoding set.
	body, _, err = e.get(ctx, rawURL, withSolverReplayHeaders(headers, res.UserAgent))
	if err != nil {
		return nil, err
	}
	if err := detectAntiBot(body); err != nil {
		return nil, err
	}
	return body, nil
}

// withSolverReplayHeaders returns headers augmented for the post-solve replay,
// without mutating the caller's map: the solver's User-Agent (cf_clearance is
// UA-bound) plus a browser-realistic Accept / Accept-Language / Accept-Encoding
// set. A gzip-only Accept-Encoding is a known anti-bot 403 trigger, so a
// browser-like "gzip, deflate" is sent and the response is decompressed in do().
// Existing values are preserved (a definition's own headers win).
func withSolverReplayHeaders(headers map[string][]string, ua string) map[string][]string {
	out := make(map[string][]string, len(headers)+4)
	for k, v := range headers {
		out[k] = v
	}
	if ua != "" {
		out["User-Agent"] = []string{ua}
	}
	setHeaderIfAbsent(out, "Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,*/*;q=0.8")
	setHeaderIfAbsent(out, "Accept-Language", "en-US,en;q=0.9")
	setHeaderIfAbsent(out, "Accept-Encoding", "gzip, deflate")
	return out
}

// setHeaderIfAbsent sets key to val only when absent, so a definition's own header
// is never overridden by the replay defaults.
func setHeaderIfAbsent(h map[string][]string, key, val string) {
	if _, ok := h[key]; !ok {
		h[key] = []string{val}
	}
}

// SolveHost clears an anti-bot interstitial for rawURL's host by asking the
// configured solver and seeding what it returns (cookies + the bound User-Agent)
// into the session, so a SUBSEQUENT login and search reuse a fresh host-scoped
// cf_clearance. The engine calls it during logged-out search recovery: a
// CF-plus-form-login tracker whose challenge markers detectAntiBot does not match
// would otherwise never trigger a solve. cf_clearance is a host cookie, so
// clearing any URL on the host (the base URL) suffices. With the default
// NoopSolver (or a solver that declines) it returns ErrNoSolverConfigured and the
// caller proceeds to a plain re-login. It never fetches; it only seeds.
func (e *Executor) SolveHost(ctx context.Context, rawURL string) error {
	res, err := e.solver().Solve(ctx, rawURL)
	if err != nil {
		return err //nolint:wrapcheck // sentinel (ErrNoSolverConfigured) the caller branches on.
	}
	e.applySolveResult(rawURL, res)
	return nil
}

// applySolveResult seeds a solver's cookies into the jar (scoped to the target
// URL's host) and persists its User-Agent for the rest of the session, so every
// later request can replay the UA the UA-bound cf_clearance was issued for.
func (e *Executor) applySolveResult(rawURL string, res SolveResult) {
	if res.UserAgent != "" {
		e.SolverUserAgent = res.UserAgent
	}
	if e.Jar == nil || len(res.Cookies) == 0 {
		return
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return
	}
	e.Jar.SetCookies(u, res.Cookies)
}

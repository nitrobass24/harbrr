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
// ManualCookieSolver replays a user-supplied cookie, and a FlareSolverr client is
// the Phase 6 implementation. [Tracked: Phase 6 — FlareSolverr solver]
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
	e.seedSolverCookies(rawURL, res)
	// Anti-bot clearance is often UA-coupled (the cf_clearance cookie is bound to
	// the User-Agent the solver used), so the retry must send the solver's UA.
	body, _, err = e.get(ctx, rawURL, withUserAgent(headers, res.UserAgent))
	if err != nil {
		return nil, err
	}
	if err := detectAntiBot(body); err != nil {
		return nil, err
	}
	return body, nil
}

// withUserAgent returns headers with User-Agent set to ua, without mutating the
// caller's map. An empty ua returns headers unchanged.
func withUserAgent(headers map[string][]string, ua string) map[string][]string {
	if ua == "" {
		return headers
	}
	out := make(map[string][]string, len(headers)+1)
	for k, v := range headers {
		out[k] = v
	}
	out["User-Agent"] = []string{ua}
	return out
}

// seedSolverCookies installs a solver's cookies into the jar, scoped to the
// target URL's host, so the retried request carries them.
func (e *Executor) seedSolverCookies(rawURL string, res SolveResult) {
	if e.Jar == nil || len(res.Cookies) == 0 {
		return
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return
	}
	e.Jar.SetCookies(u, res.Cookies)
}

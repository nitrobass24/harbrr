package login

import (
	stdhttp "net/http"
	"net/http/cookiejar"
	"sync"

	"golang.org/x/net/publicsuffix"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/selector"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/template"
)

// Doer is the narrow HTTP seam the executor drives. It is satisfied by
// *http.Client in production and by a replay transport in tests, so NO live
// network call ever happens in this package's code or tests. Keeping the seam to
// a single method (mirroring http.Client.Do) lets the production client own
// cookie-jar persistence and redirect following while the executor stays
// transport-agnostic.
type Doer interface {
	Do(*stdhttp.Request) (*stdhttp.Response, error)
}

// Session carries the cookie state established by a login, handed to the search
// stage so authenticated requests reuse the same jar. It is a thin
// wrapper; it exists so the search stage depends on a stable type rather
// than reaching into Executor internals. Obtain one via Executor.Session after a
// successful Login/EnsureLoggedIn.
type Session struct {
	// Jar holds every cookie captured during login (Set-Cookie from the login
	// round-trip, or the seeded cookie for the manual-cookie method).
	Jar stdhttp.CookieJar
	// UserAgent is the anti-bot solver's User-Agent, set once a solve occurred this
	// session. A Cloudflare cf_clearance cookie is bound to the User-Agent that
	// earned it, so the search stage must replay the SAME UA the solver used or the
	// clearance is rejected and the page reverts to the challenge/login form. Empty
	// when no solve happened (the common case), leaving the default UA untouched.
	UserAgent string
}

// Session returns the cookie state established by the executor's login, for the
// search stage to reuse the authenticated jar (and the solver UA bound to any
// cf_clearance in it). It is meaningful only after a successful
// Login/EnsureLoggedIn; before that the jar is empty.
func (e *Executor) Session() *Session {
	return &Session{Jar: e.Jar, UserAgent: e.solverUA()}
}

// solverUA returns the persisted solver User-Agent under the read lock. The
// search/grab path calls Session (and thus this) without holding the engine's
// loginMu, so it can race a relogin's setSolverUA without this guard.
func (e *Executor) solverUA() string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.SolverUserAgent
}

// setSolverUA persists the solver User-Agent under the write lock. Called from
// applySolveResult during a login (loginMu held), concurrent with search-path
// readers that do not hold loginMu.
func (e *Executor) setSolverUA(ua string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.SolverUserAgent = ua
}

// Executor performs a definition's login sequence against the injected Doer and
// owns the cookie jar. It owns the jar (rather than receiving cookies per call)
// because the cookie method must SEED the jar and re-login must INSPECT it.
type Executor struct {
	// Client is the HTTP seam. Required.
	Client Doer
	// Jar holds cookie state across the login round-trip and into search.
	Jar stdhttp.CookieJar
	// BaseURL is the tracker site link, used to resolve relative login/test
	// paths and to scope seeded cookies. Trailing-slash-insensitive.
	BaseURL string
	// Config supplies template variables (.Config.username, .Config.cookie, ...).
	// Passed in as a map by the engine; this stage never touches the secrets store.
	Config map[string]string
	// Selector extracts CSRF inputs, error messages, and test-page selectors.
	Selector *selector.Engine
	// Solver is the optional anti-bot solver consulted when a login landing page
	// is an interstitial (Cloudflare etc.). Nil defaults to NoopSolver (fail loud).
	Solver Solver
	// SolverUserAgent is the User-Agent the solver reported on its most recent
	// solve this session. Once set, do() replays it on every subsequent request
	// (login POST, login.test) so a UA-bound cf_clearance keeps working; Session
	// hands it to the search stage for the same reason. Empty until a solve occurs.
	//
	// Guarded by mu: the write (applySolveResult, during a loginMu-held login) races
	// the read on the search/grab path (Session), which does NOT hold loginMu. Access
	// it through setSolverUA/solverUA off the hot path, never the field directly.
	SolverUserAgent string
	// mu guards SolverUserAgent. A dedicated RWMutex keeps the fix local to the
	// executor instead of relying on the engine's loginMu (the search read path does
	// not hold it).
	mu sync.RWMutex
}

// Option configures an Executor in New.
type Option func(*Executor)

// WithClient sets the HTTP Doer seam.
func WithClient(c Doer) Option { return func(e *Executor) { e.Client = c } }

// WithJar sets the cookie jar. When unset, New installs a publicsuffix-backed
// jar so production callers get correct cross-subdomain cookie scoping for free.
func WithJar(j stdhttp.CookieJar) Option { return func(e *Executor) { e.Jar = j } }

// WithBaseURL sets the tracker site link used to resolve relative paths.
func WithBaseURL(u string) Option { return func(e *Executor) { e.BaseURL = u } }

// WithConfig sets the template-variable config map.
func WithConfig(c map[string]string) Option { return func(e *Executor) { e.Config = c } }

// WithSolver sets the anti-bot solver consulted on a login interstitial. Unset
// leaves the default NoopSolver (fail loud).
func WithSolver(s Solver) Option { return func(e *Executor) { e.Solver = s } }

// New constructs an Executor. It installs a publicsuffix-backed cookie jar and a
// selector engine bound to the template context unless overridden, so the only
// strictly required option for production is WithClient. cookiejar.New with the
// publicsuffix list never returns an error, so the fallback is unconditional.
func New(opts ...Option) *Executor {
	e := &Executor{Config: map[string]string{}}
	for _, opt := range opts {
		opt(e)
	}
	if e.Jar == nil {
		jar, _ := cookiejar.New(&cookiejar.Options{PublicSuffixList: publicsuffix.List})
		e.Jar = jar
	}
	if e.Selector == nil {
		e.Selector = selector.New()
	}
	// Bind the selector's template seam to evaluate against this executor's
	// config, so SelectorInputs/error-message templates resolve the same way the
	// rest of the pipeline does.
	e.Selector.EvalTemplate = func(s string) (string, error) {
		return template.Eval(s, e.templateContext())
	}
	return e
}

// templateContext builds a fresh template.Context seeded with the executor's
// Config (and .Config.sitelink) on each call. A fresh context per call is
// required because template.Eval mutates the context (whitespace normalization).
func (e *Executor) templateContext() *template.Context {
	ctx := template.NewContext()
	for k, v := range e.Config {
		ctx.Config[k] = v
	}
	if _, ok := ctx.Config["sitelink"]; !ok {
		ctx.Config["sitelink"] = e.BaseURL
	}
	return ctx
}

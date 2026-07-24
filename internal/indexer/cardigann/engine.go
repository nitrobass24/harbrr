package cardigann

import (
	"context"
	"errors"
	"fmt"
	"maps"
	stdhttp "net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/dateparse"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/internal/httpx"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/internal/selector"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/login"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/mapper"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/normalizer"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
)

// Query and Release re-export the search/normalizer types so engine callers
// depend only on this package.
type (
	// Query is the parsed search request the engine drives a definition with.
	Query = search.Query
	// Release is the canonical normalized release the engine produces.
	Release = normalizer.Release
)

// Doer is the narrow HTTP seam the engine drives for login and search. It is
// satisfied by *http.Client in production and a replay transport in tests, so no
// live network call ever happens in the engine or its tests.
//
// Aliased to httpx.Doer, the one definition shared with the login and search
// stages (see httpx.Doer's doc); nothing outside this package references
// cardigann.Doer today, but the alias keeps the exported symbol stable.
type Doer = httpx.Doer

// Engine assembles every pipeline stage for one definition and runs them
// end-to-end. NewEngine wires the per-def seams the stages left open (the mapper
// category map into the normalizer; the dateparse parser into the filter
// registry; the def language/type/base URL throughout) so a search is a single
// Search call. The Engine is built once per definition and is safe to reuse
// across concurrent queries: the selector engine holds no per-call state, and
// the search stage takes eval closures and the row/document data as explicit
// parameters rather than mutating shared fields.
type Engine struct {
	def  *loader.Definition
	caps *mapper.Capabilities
	deps search.Deps
	// selector extracts row/field values from parsed HTML/JSON documents. It
	// holds no per-call state, so this single instance is shared across every
	// search this Engine runs, including concurrent ones.
	selector *selector.Engine
	login    *login.Executor
	doer     search.Doer
	baseURL  string

	// loginMu guards the once-per-Engine login memoization (ensureSession).
	loginMu  sync.Mutex
	loggedIn bool
}

// options collects the configurable construction inputs before NewEngine wires
// the stages, so a stage that depends on several (e.g. the parser needs the
// clock + language) reads a single resolved struct.
type options struct {
	doer    search.Doer
	config  map[string]string
	clock   func() time.Time
	baseURL string
	solver  login.Solver
}

// Option configures the Engine at construction.
type Option func(*options)

// WithDoer injects the HTTP seam used for login and search. Required for any
// Search call; ParseResponse needs no Doer (offline extraction).
func WithDoer(d search.Doer) Option {
	return func(o *options) { o.doer = d }
}

// WithConfig sets the resolved .Config template namespace (tracker settings).
func WithConfig(config map[string]string) Option {
	return func(o *options) { o.config = config }
}

// WithClock injects the reference clock for deterministic date parsing. Defaults
// to time.Now.
func WithClock(fn func() time.Time) Option {
	return func(o *options) { o.clock = fn }
}

// WithBaseURL overrides the tracker base URL used to resolve relative request
// paths and release URLs. Defaults to the definition's first link.
func WithBaseURL(u string) Option {
	return func(o *options) { o.baseURL = u }
}

// SolverOption returns the engine option wiring an anti-bot solver from an
// instance's resolved .Config (the "solver_type" setting):
//   - "manual_cookie" replays the encrypted "cookie" setting (ManualCookieSolver);
//   - anything else (including unset) leaves the default NoopSolver.
//
// Keeping the mapping here lets the registry
// wire a solver from config without importing the login package directly.
func SolverOption(cfg map[string]string) Option {
	switch cfg["solver_type"] {
	case "manual_cookie":
		s := login.ManualCookieSolver{Cookie: cfg["cookie"]}
		return func(o *options) { o.solver = s }
	case "flaresolverr":
		s := login.NewFlareSolverrSolver(cfg["flaresolverr_url"], flareMaxTimeout(cfg["flaresolverr_max_timeout"]))
		return func(o *options) { o.solver = s }
	default:
		return func(*options) {}
	}
}

// flareMaxTimeout parses the optional per-instance FlareSolverr maxTimeout (in
// seconds); an empty/invalid value lets the solver use its default.
func flareMaxTimeout(s string) time.Duration {
	if secs, err := strconv.Atoi(strings.TrimSpace(s)); err == nil && secs > 0 {
		return time.Duration(secs) * time.Second
	}
	return 0
}

// NewEngine builds an Engine for def, wiring all nine stage seams. It fails loud
// (never silently) when the mapper rejects the definition's categories — that is
// the one stage that can reject a structurally valid def, and a silent failure
// would corrupt category parity.
func NewEngine(def *loader.Definition, opts ...Option) (*Engine, error) {
	if def == nil {
		return nil, errors.New("cardigann: nil definition")
	}
	o := resolveOptions(def, opts)

	caps, err := mapper.Build(def)
	if err != nil {
		return nil, fmt.Errorf("cardigann: building capabilities for %q: %w", def.ID, err)
	}

	deps, err := buildDeps(def, caps, o)
	if err != nil {
		return nil, fmt.Errorf("cardigann: %w", err)
	}

	return &Engine{
		def:      def,
		caps:     caps,
		deps:     deps,
		selector: selector.New(),
		login:    buildLogin(o),
		doer:     o.doer,
		baseURL:  o.baseURL,
	}, nil
}

// resolveOptions applies the option funcs and fills defaults: the clock defaults
// to time.Now, the base URL to the definition's first link, the config to an
// empty map.
func resolveOptions(def *loader.Definition, opts []Option) options {
	o := options{config: map[string]string{}, clock: time.Now}
	for _, opt := range opts {
		opt(&o)
	}
	if o.clock == nil {
		o.clock = time.Now
	}
	// Seed .Config from the definition's settings defaults, then overlay the
	// caller's explicit config so user-supplied values win — matching Jackett,
	// where a request template reads the setting Default until the user sets it.
	cfg := maps.Clone(DefaultConfig(def))
	maps.Copy(cfg, o.config)
	o.config = cfg
	if o.baseURL == "" {
		o.baseURL = firstLink(def)
	}
	return o
}

// buildDeps wires the extraction-half stages: the dateparse parser (def language
// + injected clock) feeds the search filter registry's date seams; the registry's
// language is the def language so regex filters route correctly; the normalizer
// carries the base URL, def type, and category map. The selector engine is
// built separately in NewEngine (Engine.selector) and passed into the search
// stage's calls explicitly, since it is not part of Deps.
func buildDeps(def *loader.Definition, caps *mapper.Capabilities, o options) (search.Deps, error) {
	parser := dateparse.New(
		dateparse.WithLanguage(def.Language),
		dateparse.WithClock(o.clock),
	)

	registry := search.NewFilterRegistry(parser.ParseDate, parser.ParseRelTime, def.Language)

	norm := &normalizer.Normalizer{
		BaseURL:    o.baseURL,
		Type:       def.Type,
		Categories: caps.CategoryMap,
	}

	// Resolve the def's declared charset once. A non-UTF-8 def with an
	// unresolvable encoding fails loud here rather than silently emitting mojibake
	// titles and mis-encoded searches.
	enc, err := search.ResolveEncoding(def.Encoding)
	if err != nil {
		return search.Deps{}, fmt.Errorf("resolving encoding for %q: %w", def.ID, err)
	}

	return search.Deps{
		Filters:    registry,
		Normalizer: norm,
		Config:     o.config,
		BaseURL:    o.baseURL,
		Clock:      o.clock,
		Encoding:   enc,
	}, nil
}

// buildLogin constructs the login executor with the HTTP seam, base URL, and
// config. It owns its own selector engine (login.New), separate from
// Engine.selector used by the search stage; login.Executor evaluates its
// selector templates against its own config via Executor.eval. When the Doer
// owns a cookie jar (production and the parity harness both drive an
// *http.Client with one), that SAME jar is handed to the executor so login
// seeding and the transport's cookie handling share a single jar — a second jar
// would put duplicate (and, after a login-time session rotation, stale-first)
// Cookie pairs on the wire.
func buildLogin(o options) *login.Executor {
	opts := []login.Option{
		login.WithClient(o.doer),
		login.WithBaseURL(o.baseURL),
		login.WithConfig(o.config),
		login.WithSolver(o.solver),
	}
	if jar := doerJar(o.doer); jar != nil {
		opts = append(opts, login.WithJar(jar))
	}
	return login.New(opts...)
}

// doerJar returns the cookie jar the Doer applies to outgoing requests: an
// *http.Client's own Jar, or the jar a wrapper reports via search.JarOwner
// (the registry's paced client). Nil when the Doer manages no jar — then no
// cookies flow on the wire, which only offline/parse-only Doers accept.
func doerJar(d search.Doer) stdhttp.CookieJar {
	switch c := d.(type) {
	case *stdhttp.Client:
		return c.Jar
	case search.JarOwner:
		return c.CookieJar()
	default:
		return nil
	}
}

// firstLink returns the definition's first declared site link, the default base
// URL. Definitions always declare at least one link (schema-required).
func firstLink(def *loader.Definition) string {
	if len(def.Links) > 0 {
		return def.Links[0]
	}
	return ""
}

// Capabilities returns the typed capabilities model the mapper produced.
func (e *Engine) Capabilities() *mapper.Capabilities { return e.caps }

// Search runs the full online search: ensure the session is logged in (re-login
// when the test page fails), then execute the search request(s) and parse the
// response into normalized releases. The Doer must have been supplied via
// WithDoer.
func (e *Engine) Search(ctx context.Context, query Query) ([]*Release, error) {
	if e.doer == nil {
		return nil, fmt.Errorf("cardigann: Search for %q requires WithDoer (use ParseResponse for offline extraction)", e.def.ID)
	}
	if err := e.ensureSession(ctx); err != nil {
		return nil, fmt.Errorf("cardigann: login for %q: %w", e.def.ID, err)
	}
	releases, err := search.Execute(ctx, e.def, query, e.login.Session(), e.doer, e.selector, e.deps)
	if errors.Is(err, search.ErrSearchLoggedOut) {
		// Lazy login: the session expired since the eager first login. Re-login
		// once and retry the search a single time (Jackett's
		// CheckIfLoginIsNeeded -> DoLogin -> re-request). The retry is bounded to
		// one attempt: a second logged-out result is returned as the error below,
		// never looped. The relogin re-runs the full login sequence, which clears a
		// CF-gated login itself (both the form and post methods route a challenged
		// submit through login.solveAndRetryLoginPost), so the search retry
		// inherits a fresh authenticated session + cf_clearance.
		if rerr := e.relogin(ctx); rerr != nil {
			return nil, fmt.Errorf("cardigann: re-login for %q after session expiry: %w", e.def.ID, rerr)
		}
		releases, err = search.Execute(ctx, e.def, query, e.login.Session(), e.doer, e.selector, e.deps)
	}
	if err != nil {
		return nil, fmt.Errorf("cardigann: search for %q: %w", e.def.ID, err)
	}
	return releases, nil
}

// ensureSession logs in at most once per Engine for the FIRST search. Jackett
// logs in at configure time and reuses the session across searches; harbrr defers
// login to the first search and memoizes it, so a reused Engine does not re-run
// the login sequence on every query (which, for the many private defs without a
// login.test block, would mean a full login POST per search — a live login-rate
// hazard). A session that later expires is handled lazily by relogin (below),
// triggered when a search response looks logged-out.
func (e *Engine) ensureSession(ctx context.Context) error {
	e.loginMu.Lock()
	defer e.loginMu.Unlock()
	if e.loggedIn {
		return nil
	}
	if err := e.login.EnsureLoggedIn(ctx, e.def); err != nil {
		return err //nolint:wrapcheck // Search wraps with the def id + "login for".
	}
	e.loggedIn = true
	return nil
}

// relogin forces a fresh login after a search response looked logged-out, then
// refreshes the memoized flag. The mutex serializes concurrent relogins on a
// reused Engine and prevents racing loggedIn; harbrr is single-user, so the brief
// serialization is fine. Login (not EnsureLoggedIn) is used because the search
// response already told us the session is gone — re-probing via login.test would
// only add a redundant request. The retry in Search is bounded to one attempt,
// so a def whose login.test selector is legitimately absent from a search page
// (e.g. an AJAX fragment) causes one wasted relogin and a surfaced error, never a
// loop.
func (e *Engine) relogin(ctx context.Context) error {
	e.loginMu.Lock()
	defer e.loginMu.Unlock()
	e.loggedIn = false
	if err := e.login.Login(ctx, e.def); err != nil {
		return err //nolint:wrapcheck // Search wraps with the def id + "re-login for".
	}
	e.loggedIn = true
	return nil
}

// Test validates the configured credentials by running the definition's login
// probe (CheckTest, then Login if needed), returning the real outcome: nil on
// success, or ErrLoginFailed / ErrSolverRequired / ErrCaptchaRequired / a
// transport error. Unlike Search's memoized ensureSession it always exercises
// login and caches nothing, so the management "Test" action gets a fresh,
// truthful result. Requires WithDoer.
func (e *Engine) Test(ctx context.Context) error {
	if e.doer == nil {
		return fmt.Errorf("cardigann: Test for %q requires WithDoer", e.def.ID)
	}
	return e.login.EnsureLoggedIn(ctx, e.def) //nolint:wrapcheck // the API layer sanitizes/maps this error.
}

// NeedsResolver reports whether the definition declares a download block, so a
// served release link must be resolved (via ResolveDownload) before a grab. A
// direct-link tracker (no download block) reports false.
func (e *Engine) NeedsResolver() bool {
	return e.def.Download != nil
}

// DownloadNeedsAuth reports whether the download authenticates out-of-band rather
// than via a passkey in the URL: a def with a login block authenticates its grab by
// session cookie or request header, so a bare served link would fail (login page /
// 401) for *arr. Such links are routed through /dl and fetched with the session.
func (e *Engine) DownloadNeedsAuth() bool {
	return e.def.Login != nil
}

// SupportsOffsetPaging is always false: the declarative Cardigann engine has no
// per-def offset/limit request shape, so every def's driver fetches the full result
// set and the shared read pipeline slices the requested page locally. Only the two
// native usenet drivers (newznab, nzbindex) forward offset/limit upstream.
func (e *Engine) SupportsOffsetPaging() bool {
	return false
}

// ConsumesSearchMode is always false: the declarative Cardigann engine never
// templates search.Query.Mode into a request — every def's search block is driven
// by keywords/categories/ids, not by a Torznab t= namespace switch — so an RSS poll
// under a different Mode is byte-identical outbound and safe to collapse onto one
// cache key (#341).
func (e *Engine) ConsumesSearchMode() bool {
	return false
}

// ResolveDownload turns a release's download link into the real torrent URL when
// the definition declares a download block (the full Jackett download algorithm:
// before pre-request, infohash->magnet, selectors). A def with no download block
// returns the link unchanged. It ensures the session first (the download page is
// usually behind login) and drives the same Doer as Search.
//
// validate runs the grab-time testlinktorrent check: pass true only when resolving
// for an actual grab (the /dl proxy) or simulating one (the parity harness). Feed-
// time pre-resolution passes false so it never fetches a torrent per served release.
func (e *Engine) ResolveDownload(ctx context.Context, link string, validate bool) (string, error) {
	if e.def.Download == nil {
		return link, nil
	}
	if e.doer == nil {
		return "", fmt.Errorf("cardigann: ResolveDownload for %q requires WithDoer", e.def.ID)
	}
	if err := e.ensureSession(ctx); err != nil {
		return "", fmt.Errorf("cardigann: login for %q: %w", e.def.ID, err)
	}
	resolved, err := search.ResolveDownload(ctx, e.def, link, e.login.Session(), e.doer, e.deps, validate)
	if err != nil {
		return "", fmt.Errorf("cardigann: resolving download for %q: %w", e.def.ID, err)
	}
	return resolved, nil
}

// Grab performs the full grab-time download for a release link: resolve it (with
// testlinktorrent validation) and fetch the torrent through the session honouring
// download.method/headers. A magnet is returned as a redirect target. This is what
// the /dl proxy drives so a passkey-bearing link is resolved and fetched
// server-side, never exposed in the served feed.
func (e *Engine) Grab(ctx context.Context, link string) (*search.GrabResult, error) {
	if e.doer == nil {
		return nil, fmt.Errorf("cardigann: Grab for %q requires WithDoer", e.def.ID)
	}
	if err := e.ensureSession(ctx); err != nil {
		return nil, fmt.Errorf("cardigann: login for %q: %w", e.def.ID, err)
	}
	result, err := search.Grab(ctx, e.def, link, e.login.Session(), e.doer, e.deps)
	if err != nil {
		return nil, fmt.Errorf("cardigann: grabbing download for %q: %w", e.def.ID, err)
	}
	return result, nil
}

// ParseResponse is the offline extraction half: parse a saved response body into
// normalized releases without any HTTP, for the parity harness and regression
// snapshots. responseType selects the JSON parser when "json"; anything else
// parses as HTML. A zero Query is used (raw RSS), which the row-filter stage
// treats as "no andmatch keyword constraint".
func (e *Engine) ParseResponse(body []byte, responseType string) ([]*Release, error) {
	return e.ParseResponseQuery(body, responseType, Query{})
}

// ParseResponseQuery is ParseResponse with an explicit query, so the andmatch row
// filter and any .Query.* field templates see the real search terms when
// replaying a saved response. responseType overrides the definition's response
// type when non-empty; when empty, the def's leading path type applies (a saved
// single body carries no path identity, so the offline replay defaults to the
// first declared type — the live search parses each response per-path instead).
func (e *Engine) ParseResponseQuery(body []byte, responseType string, query Query) ([]*Release, error) {
	if responseType == "" {
		responseType = search.DefaultResponseType(e.def)
	}
	releases, err := search.ParseResults(e.def, body, responseType, query, e.selector, e.deps)
	if err != nil {
		return nil, fmt.Errorf("cardigann: parsing response for %q: %w", e.def.ID, err)
	}
	return releases, nil
}

// ResultsJSON marshals releases to canonical, deterministic JSON via the
// normalizer, the byte-comparable form the parity/regression snapshots assert on.
func (e *Engine) ResultsJSON(releases []*Release) ([]byte, error) {
	out, err := normalizer.Marshal(releases)
	if err != nil {
		return nil, fmt.Errorf("cardigann: marshaling results for %q: %w", e.def.ID, err)
	}
	return out, nil
}

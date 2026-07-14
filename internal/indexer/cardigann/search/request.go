package search

import (
	"context"
	"fmt"
	"io"
	stdhttp "net/http"
	"net/url"
	"strings"
	"time"

	apphttp "github.com/autobrr/harbrr/internal/http"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/internal/encode"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/internal/template"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/login"
)

// builtRequest is one fully resolved search request: its method, absolute URL,
// optional form body (POST), and rendered headers. The URL is built but never
// logged raw — a definition can embed a secret ANYWHERE in it (a passkey in the
// path, a config value under an arbitrary query name), so every error site
// surfaces host-only detail via apphttp.SchemeHost; RedactURL's name/length
// heuristics are not trusted for def-driven URLs. Request-level diagnosis
// stays available at the paced-client debug/trace logs.
type builtRequest struct {
	method  string
	url     string
	body    string
	headers map[string][]string
	// followRedirect mirrors the PATH-level `followredirect` opt-in, gating the
	// manual follow of a 3xx search response (Jackett reads only
	// SearchPath.Followredirect here — the definition-level flag applies to
	// login/landing flows and never to search, so there is no fallback).
	followRedirect bool
	// respType is this path's Response.Type ("" parses as HTML). Carried per
	// request so a mixed HTML+JSON multi-path def parses each body under its own
	// path's type, matching Jackett's per-SearchPath response handling.
	respType string
	// noResultsMessage is this path's Response.NoResultsMessage; nil when the
	// def doesn't declare one. Consumed by noResultsMatch (search.go).
	noResultsMessage *string
}

// buildRequests renders each search path the definition declares (Search.Path
// or Search.Paths[]) against the query, producing one builtRequest per
// surviving path. Mirrors Jackett PerformQuery's per-SearchPath loop: apply the
// path's category gate (narrowToPathCategories), render the path template,
// resolve it against BaseURL, assemble the inputs (inherited Search.Inputs then
// path Inputs) into a GET query string or POST body, and attach Search.Headers.
func buildRequests(def *loader.Definition, query Query, deps Deps) ([]builtRequest, error) {
	query, err := applyKeywordsFilters(def, query, deps)
	if err != nil {
		return nil, err
	}
	paths := searchPaths(def)
	out := make([]builtRequest, 0, len(paths))
	for i := range paths {
		pathQuery, ok := narrowToPathCategories(query, paths[i].Categories)
		if !ok {
			continue
		}
		req, err := buildOneRequest(def, paths[i], pathQuery, deps)
		if err != nil {
			return nil, err
		}
		out = append(out, req)
	}
	return out, nil
}

// narrowToPathCategories applies a path's category gate, mirroring Jackett's
// SearchPaths loop: a path with categories runs only when they intersect the
// query's mapped categories — a leading "!" element inverts the test — and the
// surviving path sees {{ .Categories }} narrowed to that intersection (empty
// for a matching inverted path, exactly as Jackett's Intersect yields there).
// A non-matching path is skipped. Paths without categories always run with the
// full list. The query's Categories already carry the DefaultCategories
// fallback (torznabhttp buildQuery), so like Jackett a query that still has no
// categories skips every non-inverted category-gated path.
func narrowToPathCategories(query Query, pathCats []loader.Scalar) (Query, bool) {
	if len(pathCats) == 0 {
		return query, true
	}
	cats := make([]string, len(pathCats))
	for i := range pathCats {
		cats[i] = pathCats[i].String()
	}
	intersection := intersect(query.Categories, cats)
	matched := len(intersection) > 0
	if cats[0] == "!" {
		matched = !matched
	}
	if !matched {
		return Query{}, false
	}
	query.Categories = intersection
	return query, true
}

// intersect returns the distinct elements of a that also appear in b, in a's
// order — .NET Enumerable.Intersect semantics, which the narrowed
// {{ .Categories }} request bytes depend on.
func intersect(a, b []string) []string {
	inB := make(map[string]struct{}, len(b))
	for _, v := range b {
		inB[v] = struct{}{}
	}
	var out []string
	seen := make(map[string]struct{}, len(a))
	for _, v := range a {
		if _, ok := inB[v]; !ok {
			continue
		}
		if _, dup := seen[v]; dup {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}

// searchPaths normalizes the Search.Path / Search.Paths oneOf into a single
// ordered slice of path blocks. A bare Search.Path becomes a single GET path
// that inherits Search.Inputs (Jackett's default).
func searchPaths(def *loader.Definition) []loader.SearchPathBlock {
	if len(def.Search.Paths) > 0 {
		return def.Search.Paths
	}
	inherit := true
	return []loader.SearchPathBlock{{Path: def.Search.Path, InheritInputs: &inherit}}
}

// buildOneRequest renders one search path into a builtRequest.
func buildOneRequest(def *loader.Definition, path loader.SearchPathBlock, query Query, deps Deps) (builtRequest, error) {
	// The path is rendered with URL-encoded variable values: Jackett renders
	// SearchPath.Path with WebUtility.UrlEncode (then +->%20), so a keyword or
	// passkey inlined into the path (e.g. `?filename={{ .Keywords }}`, as teamos
	// and other defs do) becomes a valid, parity-matching URL rather than carrying
	// a literal space. Inputs and headers use the un-encoded context (inputs are
	// encoded later by encodeOrdered; headers are not URL values).
	rendered, err := template.Eval(path.Path, requestPathContext(query, deps))
	if err != nil {
		return builtRequest{}, fmt.Errorf("rendering search path: %w", err)
	}
	absURL, err := resolveURL(deps.BaseURL, rendered)
	if err != nil {
		return builtRequest{}, err
	}

	pairs, err := buildInputs(def, path, query, deps)
	if err != nil {
		return builtRequest{}, err
	}
	// Codepage-encode the GET query / POST body values in the def's charset
	// (Jackett GetQueryString(Encoding) / FormUrlEncodedContentWithEncoding). The
	// path template above stays UTF-8 (requestPathContext), matching Jackett's
	// asymmetry: only input pairs are codepage-encoded, never path-substituted
	// values. No-op for UTF-8/no-encoding defs.
	pairs = codepageEncodePairs(deps.Encoding, pairs)

	headers, err := renderHeaders(def.Search.Headers, requestContext(query, deps))
	if err != nil {
		return builtRequest{}, err
	}

	return assembleRequest(path, absURL, pairs, headers)
}

// requestParams builds the template.Params shared by every request-context
// builder: config + query namespace + keywords + categories + today.
// clockOrNow resolves deps.Clock exactly as Deps.Clock's doc promises: nil
// falls back to time.Now so .Today is never silently empty.
func requestParams(query Query, deps Deps) template.Params {
	return template.Params{
		Config:     deps.Config,
		BaseURL:    deps.BaseURL,
		Query:      query.queryMap(),
		Keywords:   query.templateKeywords(),
		Categories: query.Categories,
		Clock:      clockOrNow(deps.Clock),
	}
}

// clockOrNow defaults a nil clock to time.Now.
func clockOrNow(clock func() time.Time) func() time.Time {
	if clock == nil {
		return time.Now
	}
	return clock
}

// requestContext builds the template context for request rendering: config +
// query namespace + keywords + categories + today. See template.NewSeeded for
// the fresh-context-per-call invariant.
func requestContext(query Query, deps Deps) *template.Context {
	return template.NewSeeded(requestParams(query, deps))
}

// requestPathContext is requestContext with every scalar variable value
// URL-encoded for path-template substitution (space -> %20), reproducing
// Jackett's `applyGoTemplateText(SearchPath.Path, ..., WebUtility.UrlEncode)`.
// No vendored def substitutes .Config.sitelink (a URL) into a path, so encoding
// all values — including the sitelink NewSeeded defaulted in — is safe.
func requestPathContext(query Query, deps Deps) *template.Context {
	ctx := requestContext(query, deps)
	ctx.Config = encodeStringValues(ctx.Config)
	ctx.Query = encodeStringValues(ctx.Query)
	ctx.Keywords = pathEscape(ctx.Keywords)
	ctx.Categories = encodeStringSlice(ctx.Categories)
	return ctx
}

// pathEscape URL-encodes a value for inlining into a path/query, with spaces as
// %20, matching Jackett's WebUtility.UrlEncode followed by +->%20 (see the
// encode package for the exact .NET-compatible character set).
func pathEscape(s string) string {
	return encode.PathEscape(s)
}

// encodeStringValues returns a copy of m with each value path-escaped.
func encodeStringValues(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = pathEscape(v)
	}
	return out
}

// encodeStringSlice returns a copy of s with each element path-escaped.
func encodeStringSlice(s []string) []string {
	if s == nil {
		return nil
	}
	out := make([]string, len(s))
	for i, v := range s {
		out[i] = pathEscape(v)
	}
	return out
}

// buildInputs renders the input pairs for a path: Search.Inputs first (when the
// path inherits them, the default), then the path's own Inputs override. Each
// value is template-rendered; an empty rendered value is dropped unless
// AllowEmptyInputs is set, matching Jackett.
func buildInputs(def *loader.Definition, path loader.SearchPathBlock, query Query, deps Deps) ([]kv, error) {
	ctx := requestContext(query, deps)
	allowEmpty := boolVal(def.Search.AllowEmptyInputs)

	var pairs []kv
	if inheritInputs(path) {
		rendered, err := renderInputs(def.Search.Inputs, ctx, allowEmpty)
		if err != nil {
			return nil, err
		}
		pairs = append(pairs, rendered...)
	}
	rendered, err := renderInputs(path.Inputs, ctx, allowEmpty)
	if err != nil {
		return nil, err
	}
	return append(pairs, rendered...), nil
}

// inheritInputs reports whether a path inherits Search.Inputs, defaulting to
// true (Jackett's Inheritinputs defaults to true).
func inheritInputs(path loader.SearchPathBlock) bool {
	return path.InheritInputs == nil || *path.InheritInputs
}

// kv is one ordered input pair. Inputs are emitted in DEFINITION order so the
// rendered GET query / POST body reproduces Jackett's key order.
type kv struct {
	key   string
	value string
}

// renderInputs template-renders each input value in DEFINITION order (Jackett
// appends inputs to an ordered collection as it iterates them). The $raw input
// is special: its rendered value is a literal query fragment ("a=1&b=2") that is
// split into pairs rather than url-encoded as one value (Jackett's $raw handling).
func renderInputs(inputs loader.InputsBlock, ctx *template.Context, allowEmpty bool) ([]kv, error) {
	var out []kv
	for _, in := range inputs.Ordered() {
		rendered, err := template.Eval(in.Value.String(), ctx)
		if err != nil {
			return nil, fmt.Errorf("rendering search input %q: %w", in.Key, err)
		}
		if in.Key == "$raw" {
			out = append(out, splitRaw(rendered)...)
			continue
		}
		if strings.TrimSpace(rendered) == "" && !allowEmpty {
			continue
		}
		out = append(out, kv{key: in.Key, value: rendered})
	}
	return out, nil
}

// splitRaw splits a rendered $raw fragment ("a=1&b=2") into ordered pairs,
// dropping empty keys, mirroring Jackett's $raw input handling.
func splitRaw(raw string) []kv {
	var out []kv
	for _, part := range strings.Split(raw, "&") {
		key, value, _ := strings.Cut(part, "=")
		if key == "" {
			continue
		}
		out = append(out, kv{key: key, value: value})
	}
	return out
}

// assembleRequest finalizes the method, URL, and body for a path. GET appends the
// pairs as a query string (preserving any query already in the resolved path);
// POST form-encodes them into the body with a form Content-Type.
func assembleRequest(path loader.SearchPathBlock, absURL string, pairs []kv, headers map[string][]string) (builtRequest, error) {
	if strings.EqualFold(path.Method, stdhttp.MethodPost) {
		return builtRequest{
			method:           stdhttp.MethodPost,
			url:              absURL,
			body:             encodeOrdered(pairs),
			headers:          withFormContentType(headers),
			followRedirect:   boolVal(path.FollowRedirect),
			respType:         pathResponseType(path),
			noResultsMessage: pathNoResultsMessage(path),
		}, nil
	}

	full, err := appendQuery(absURL, pairs)
	if err != nil {
		return builtRequest{}, err
	}
	return builtRequest{
		method:           stdhttp.MethodGet,
		url:              full,
		headers:          headers,
		followRedirect:   boolVal(path.FollowRedirect),
		respType:         pathResponseType(path),
		noResultsMessage: pathNoResultsMessage(path),
	}, nil
}

// pathResponseType reads a path's own Response.Type; "" (no response block)
// parses as HTML, Jackett's default.
func pathResponseType(path loader.SearchPathBlock) string {
	if path.Response != nil {
		return path.Response.Type
	}
	return ""
}

// pathNoResultsMessage reads a path's own Response.NoResultsMessage; nil (no
// response block or no message) disables the no-results short-circuit.
func pathNoResultsMessage(path loader.SearchPathBlock) *string {
	if path.Response != nil {
		return path.Response.NoResultsMessage
	}
	return nil
}

// encodeOrdered renders pairs as an ordered x-www-form-urlencoded string
// (k=v&k=v) in the GIVEN order, matching Jackett's ordered queryCollection
// (StringUtil.GetQueryString). url.Values.Encode would sort keys and corrupt
// request parity, so we encode by hand with the .NET-compatible WebUtility
// encoder (space -> '+'; see the encode package).
func encodeOrdered(pairs []kv) string {
	return encodeOrderedSep(pairs, "&")
}

// encodeOrderedSep is encodeOrdered with a caller-supplied pair separator, used by
// the download.before GET request whose queryseparator the definition may override
// (Jackett's requestBlock.Queryseparator, default "&"). An empty sep defaults to "&".
func encodeOrderedSep(pairs []kv, sep string) string {
	if sep == "" {
		sep = "&"
	}
	var b strings.Builder
	for _, p := range pairs {
		if b.Len() > 0 {
			b.WriteString(sep)
		}
		b.WriteString(encode.WebUtilityEncode(p.key))
		b.WriteByte('=')
		b.WriteString(encode.WebUtilityEncode(p.value))
	}
	return b.String()
}

// appendQuery appends pairs (in order) to rawURL, preserving the resolved path's
// embedded query string VERBATIM. Jackett keeps the rendered path's query as-is
// and appends inputs to it in definition order (CardigannIndexer.PerformQuery);
// re-encoding via url.Values would re-sort both and break request parity.
func appendQuery(rawURL string, pairs []kv) (string, error) {
	return appendQuerySep(rawURL, pairs, "&")
}

// appendQuerySep is appendQuery with a caller-supplied pair separator (the
// download.before queryseparator). The separator joins the appended pairs and, when
// the path already carries a query, also joins the existing query to the new pairs,
// matching Jackett's GetQueryString(separator) appended to the path.
func appendQuerySep(rawURL string, pairs []kv, sep string) (string, error) {
	if sep == "" {
		sep = "&"
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("parsing request URL %q: %w", apphttp.SchemeHost(rawURL), apphttp.RedactURLError(err))
	}
	appended := encodeOrderedSep(pairs, sep)
	switch {
	case appended == "":
		// No inputs to add: leave the path's query untouched (verbatim).
	case u.RawQuery == "":
		u.RawQuery = appended
	default:
		u.RawQuery = u.RawQuery + sep + appended
	}
	return u.String(), nil
}

// withFormContentType returns headers with a form-urlencoded Content-Type added
// when absent. A copy is returned.
func withFormContentType(in map[string][]string) map[string][]string {
	out := make(map[string][]string, len(in)+1)
	hasCT := false
	for k, v := range in {
		out[k] = v
		if strings.EqualFold(k, "Content-Type") {
			hasCT = true
		}
	}
	if !hasCT {
		out["Content-Type"] = []string{"application/x-www-form-urlencoded"}
	}
	return out
}

// renderHeaders template-renders each header value against ctx. Header names are
// definition-authored; values may template config but are not secrets here.
func renderHeaders(in map[string][]string, ctx *template.Context) (map[string][]string, error) {
	out := make(map[string][]string, len(in))
	for name, vals := range in {
		rendered := make([]string, 0, len(vals))
		for _, v := range vals {
			r, err := template.Eval(v, ctx)
			if err != nil {
				return nil, fmt.Errorf("rendering header %q: %w", name, err)
			}
			rendered = append(rendered, r)
		}
		out[name] = rendered
	}
	return out, nil
}

// newRequest builds the *http.Request for a builtRequest: context, optional form
// body, rendered headers, then the session's solver UA (applySession); cookies
// ride the Doer's jar. Shared by doRequest and doSearchRequest so both issue
// byte-identical requests.
func newRequest(ctx context.Context, br builtRequest, session *login.Session) (*stdhttp.Request, error) {
	var bodyReader io.Reader
	if br.body != "" {
		bodyReader = strings.NewReader(br.body)
	}
	req, err := stdhttp.NewRequestWithContext(ctx, br.method, br.url, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("building %s request to %s: %w", br.method, apphttp.SchemeHost(br.url), apphttp.RedactURLError(err))
	}
	for name, vals := range br.headers {
		for _, v := range vals {
			req.Header.Add(name, v)
		}
	}
	applySession(req, session)
	return req, nil
}

// checkStatus maps a non-2xx, non-redirect status to the loud error doRequest
// has always produced: 429/503 are pacing signals the registry classifies as
// rate_limited (and the paced client backs off on), carried as a typed,
// status-bearing error; anything else fails fast — the tracker errored
// (403/500…) so the body is not results, and silently parsing it would yield a
// misleading empty page. The URL is redacted; RateLimitedError itself holds
// none, so it can't leak a passkey.
//
// This is a DELIBERATE divergence from Jackett on the search path (recorded in
// parity/testdata/README.md, "Non-2xx search-status handling"): Jackett's HTML
// branch parses ANY-status body (only checkForError's 401 gate + its error
// selectors can throw) and its XML branch has no status check at all, so a
// parseable page or a login page served with a 403/404/500 yields
// results/0-releases/a relogin there. harbrr instead fails fast — but the redirect
// half of Jackett's logged-out detection is preserved: an unfollowed 3xx is
// surfaced as data (isRedirectStatus), so a login page served as a 302 still
// relogins. Only a non-redirect non-2xx body hard-fails where Jackett would parse
// it — reloging in if a login.test selector is absent, else yielding 0 rows, but
// never hard-failing. No offline corpus fixture carries a non-2xx search status
// (per CLAUDE.md the parity target is Jackett's output on saved fixtures), so this
// is a live-only difference, gated by TestDoSearchRequest_Non2xxFailsFast.
func checkStatus(resp *stdhttp.Response, br builtRequest) error {
	if IsRateLimitStatus(resp.StatusCode) {
		return fmt.Errorf("%s %s: %w", br.method, apphttp.SchemeHost(br.url),
			&RateLimitedError{StatusCode: resp.StatusCode, RetryAfter: ParseRetryAfter(resp.Header.Get("Retry-After"), time.Now)})
	}
	return fmt.Errorf("%s %s: tracker returned HTTP %d", br.method, apphttp.SchemeHost(br.url), resp.StatusCode)
}

// doRequest issues one builtRequest through the Doer, attaching the rendered
// headers and session UA, and reads the (capped) response body. Any
// non-2xx status fails fast (see checkStatus). Used by the download/grab flows,
// whose requests keep the client's default redirect-following (Jackett's
// download path always follows); search-path requests go through
// doSearchRequest instead, which surfaces 3xx to the executor. Every error site
// redacts the URL.
func doRequest(ctx context.Context, doer Doer, br builtRequest, session *login.Session) ([]byte, error) {
	req, err := newRequest(ctx, br, session)
	if err != nil {
		return nil, err
	}
	resp, err := doer.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", br.method, apphttp.SchemeHost(br.url), apphttp.RedactURLError(err))
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, checkStatus(resp, br)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxSearchBodyBytes))
	if err != nil {
		return nil, fmt.Errorf("reading response from %s: %w", apphttp.SchemeHost(br.url), err)
	}
	return data, nil
}

// searchResponse is one search-path exchange surfaced to the executor: 2xx
// bodies and unfollowed 3xx redirects are both data (Jackett's WebClient never
// auto-follows and hands the raw redirect back); everything else already failed
// loud inside doSearchRequest.
type searchResponse struct {
	status int
	// location is the redirect target resolved against the request URL; "" when
	// the response is not a redirect or the 3xx carried no Location header. It is
	// never logged raw — like the request URL, it can embed a secret.
	location string
	body     []byte
	// contentType is the response's raw Content-Type header (Jackett's
	// WebResult.Headers["Content-Type"]). looksLoggedOut gates the login.test
	// selector check on it exactly as Jackett does — only a text/html response
	// runs the check — so it must be the WIRE header, not the def's declared type.
	contentType string
}

// doSearchRequest issues one search-path request. It stamps the context so the
// production client's RedirectPolicy surfaces a 3xx instead of following it —
// making the no-follow invariant structural rather than a caller obligation.
// The executor then decides: manual follow (path followredirect), logged-out
// signal, or parse-as-is.
func doSearchRequest(ctx context.Context, doer Doer, br builtRequest, session *login.Session) (searchResponse, error) {
	req, err := newRequest(apphttp.WithNoRedirectFollow(ctx), br, session)
	if err != nil {
		return searchResponse{}, err
	}
	resp, err := doer.Do(req)
	if err != nil {
		return searchResponse{}, fmt.Errorf("%s %s: %w", br.method, apphttp.SchemeHost(br.url), apphttp.RedactURLError(err))
	}
	defer func() { _ = resp.Body.Close() }()

	if (resp.StatusCode < 200 || resp.StatusCode >= 300) && !isRedirectStatus(resp.StatusCode) {
		return searchResponse{}, checkStatus(resp, br)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxSearchBodyBytes))
	if err != nil {
		return searchResponse{}, fmt.Errorf("reading response from %s: %w", apphttp.SchemeHost(br.url), err)
	}
	return searchResponse{
		status:      resp.StatusCode,
		location:    redirectTarget(resp, br.url),
		body:        data,
		contentType: resp.Header.Get("Content-Type"),
	}, nil
}

// redirectTarget resolves a 3xx response's Location header against the request
// URL, so a relative Location works regardless of the Doer setting
// resp.Request. Returns "" when there is no Location or it cannot be resolved.
func redirectTarget(resp *stdhttp.Response, reqURL string) string {
	if !isRedirectStatus(resp.StatusCode) {
		return ""
	}
	loc := resp.Header.Get("Location")
	if loc == "" {
		return ""
	}
	abs, err := resolveURL(reqURL, loc)
	if err != nil {
		return ""
	}
	return abs
}

// maxSearchBodyBytes caps how much of a response the executor buffers, guarding
// against a hostile/broken server streaming unbounded bytes.
const maxSearchBodyBytes = 32 << 20 // 32 MiB

// applySession replays the session's anti-bot solver User-Agent: a Cloudflare
// cf_clearance cookie in the client jar is bound to that UA, so the search must
// send it or the clearance is rejected and the tracker returns the
// challenge/login page (a false logged-out). A definition's own User-Agent
// header still wins. Cookies are deliberately NOT touched here — the Doer's jar
// is the single cookie authority (see login.Doer); adding jar cookies here as
// well would duplicate every pair on the wire, and a stale-first duplicate after
// a login-time session rotation presents the logged-out session forever.
func applySession(req *stdhttp.Request, session *login.Session) {
	if session == nil {
		return
	}
	if session.UserAgent != "" && req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", session.UserAgent)
	}
}

// resolveURL resolves a (possibly relative) rendered path against baseURL. An
// absolute rendered URL is returned as-is. Errors redact the path.
func resolveURL(baseURL, rendered string) (string, error) {
	ref, err := url.Parse(rendered)
	if err != nil {
		return "", fmt.Errorf("parsing search path %q: %w", apphttp.SchemeHost(rendered), apphttp.RedactURLError(err))
	}
	if ref.IsAbs() {
		return ref.String(), nil
	}
	base, err := url.Parse(baseURL)
	if err != nil {
		return "", fmt.Errorf("parsing base URL %q: %w", apphttp.SchemeHost(baseURL), apphttp.RedactURLError(err))
	}
	return base.ResolveReference(ref).String(), nil
}

// boolVal dereferences an optional bool, defaulting to false.
func boolVal(p *bool) bool { return p != nil && *p }

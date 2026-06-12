package search

import (
	"context"
	"fmt"
	"io"
	stdhttp "net/http"
	"net/url"
	"sort"
	"strings"

	apphttp "github.com/autobrr/seekbrr/internal/http"
	"github.com/autobrr/seekbrr/internal/indexer/cardigann/loader"
	"github.com/autobrr/seekbrr/internal/indexer/cardigann/login"
	"github.com/autobrr/seekbrr/internal/indexer/cardigann/template"
)

// builtRequest is one fully resolved search request: its method, absolute URL,
// optional form body (POST), and rendered headers. The URL is built but never
// logged raw — it may carry a passkey; every error site routes it through
// apphttp.RedactURL.
type builtRequest struct {
	method  string
	url     string
	body    string
	headers map[string][]string
}

// buildRequests renders every search path the definition declares (Search.Path
// or Search.Paths[]) against the query, producing one builtRequest per path.
// Mirrors Jackett PerformQuery's per-SearchPath loop: render the path template,
// resolve it against BaseURL, assemble the inputs (inherited Search.Inputs then
// path Inputs) into a GET query string or POST body, and attach Search.Headers.
func buildRequests(def *loader.Definition, query Query, deps Deps) ([]builtRequest, error) {
	paths := searchPaths(def)
	out := make([]builtRequest, 0, len(paths))
	for i := range paths {
		req, err := buildOneRequest(def, paths[i], query, deps)
		if err != nil {
			return nil, err
		}
		out = append(out, req)
	}
	return out, nil
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
	ctx := requestContext(query, deps)

	rendered, err := template.Eval(path.Path, ctx)
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

	headers, err := renderHeaders(def.Search.Headers, ctx)
	if err != nil {
		return builtRequest{}, err
	}

	return assembleRequest(path, absURL, pairs, headers)
}

// requestContext builds the template context for request rendering: config +
// query namespace + keywords + categories + today. A fresh context per call (Eval
// mutates it).
func requestContext(query Query, deps Deps) *template.Context {
	config := withSitelink(deps.Config, deps.BaseURL)
	return newContext(config, query.queryMap(), nil, query.keywords(), query.Categories, deps.Clock)
}

// withSitelink returns a copy of config with .Config.sitelink defaulted to the
// base URL, matching Jackett's GetBaseTemplateVariables seeding of sitelink.
func withSitelink(config map[string]string, baseURL string) map[string]string {
	out := make(map[string]string, len(config)+1)
	for k, v := range config {
		out[k] = v
	}
	if _, ok := out["sitelink"]; !ok {
		out["sitelink"] = baseURL
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

// kv is one ordered input pair. Inputs are emitted in sorted key order so the
// rendered GET query / POST body is deterministic (test-assertable).
type kv struct {
	key   string
	value string
}

// renderInputs template-renders each input value in sorted key order. The $raw
// input is special: its rendered value is a literal query fragment ("a=1&b=2")
// that is split into pairs rather than url-encoded as one value (Jackett's $raw
// handling).
func renderInputs(inputs map[string]loader.Scalar, ctx *template.Context, allowEmpty bool) ([]kv, error) {
	var out []kv
	for _, name := range sortedScalarKeys(inputs) {
		rendered, err := template.Eval(inputs[name].String(), ctx)
		if err != nil {
			return nil, fmt.Errorf("rendering search input %q: %w", name, err)
		}
		if name == "$raw" {
			out = append(out, splitRaw(rendered)...)
			continue
		}
		if strings.TrimSpace(rendered) == "" && !allowEmpty {
			continue
		}
		out = append(out, kv{key: name, value: rendered})
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
			method:  stdhttp.MethodPost,
			url:     absURL,
			body:    encodePairs(pairs).Encode(),
			headers: withFormContentType(headers),
		}, nil
	}

	full, err := appendQuery(absURL, pairs)
	if err != nil {
		return builtRequest{}, err
	}
	return builtRequest{method: stdhttp.MethodGet, url: full, headers: headers}, nil
}

// encodePairs converts ordered pairs to url.Values (order is recoverable via the
// caller's sorted keys; url.Values.Encode itself sorts keys, giving a stable
// body).
func encodePairs(pairs []kv) url.Values {
	v := url.Values{}
	for _, p := range pairs {
		v.Add(p.key, p.value)
	}
	return v
}

// appendQuery appends pairs to rawURL's query, preserving any fixed params the
// resolved path already carries.
func appendQuery(rawURL string, pairs []kv) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("parsing search URL %q: %w", apphttp.RedactURL(rawURL), err)
	}
	q := u.Query()
	for _, p := range pairs {
		q.Add(p.key, p.value)
	}
	u.RawQuery = q.Encode()
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

// doRequest issues one builtRequest through the Doer, attaching the session
// cookies and rendered headers, and reads the (capped) response body. A non-2xx
// status fails fast: the tracker errored (403/429/500…) so the body is not
// results, and silently parsing it would yield a misleading empty page. Every
// error site redacts the URL.
func doRequest(doer Doer, br builtRequest, session *login.Session) ([]byte, error) {
	var bodyReader io.Reader
	if br.body != "" {
		bodyReader = strings.NewReader(br.body)
	}
	req, err := stdhttp.NewRequestWithContext(context.Background(), br.method, br.url, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("building %s request to %s: %w", br.method, apphttp.RedactURL(br.url), err)
	}
	for name, vals := range br.headers {
		for _, v := range vals {
			req.Header.Add(name, v)
		}
	}
	applySession(req, session)

	resp, err := doer.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", br.method, apphttp.RedactURL(br.url), err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("%s %s: tracker returned HTTP %d", br.method, apphttp.RedactURL(br.url), resp.StatusCode)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxSearchBodyBytes))
	if err != nil {
		return nil, fmt.Errorf("reading response from %s: %w", apphttp.RedactURL(br.url), err)
	}
	return data, nil
}

// maxSearchBodyBytes caps how much of a response the executor buffers, guarding
// against a hostile/broken server streaming unbounded bytes.
const maxSearchBodyBytes = 32 << 20 // 32 MiB

// applySession attaches the session jar's cookies for the request URL, so the
// offline replay transport (and a jar-less production Doer) sees authenticated
// cookies on the wire.
func applySession(req *stdhttp.Request, session *login.Session) {
	if session == nil || session.Jar == nil {
		return
	}
	for _, c := range session.Jar.Cookies(req.URL) {
		req.AddCookie(c)
	}
}

// resolveURL resolves a (possibly relative) rendered path against baseURL. An
// absolute rendered URL is returned as-is. Errors redact the path.
func resolveURL(baseURL, rendered string) (string, error) {
	ref, err := url.Parse(rendered)
	if err != nil {
		return "", fmt.Errorf("parsing search path %q: %w", apphttp.RedactURL(rendered), err)
	}
	if ref.IsAbs() {
		return ref.String(), nil
	}
	base, err := url.Parse(baseURL)
	if err != nil {
		return "", fmt.Errorf("parsing base URL %q: %w", apphttp.RedactURL(baseURL), err)
	}
	return base.ResolveReference(ref).String(), nil
}

// sortedScalarKeys returns scalar-map keys in deterministic order.
func sortedScalarKeys(m map[string]loader.Scalar) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// boolVal dereferences an optional bool, defaulting to false.
func boolVal(p *bool) bool { return p != nil && *p }

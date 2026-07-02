package search

import (
	"context"
	"errors"
	"fmt"
	stdhttp "net/http"
	"net/url"
	"strings"

	apphttp "github.com/autobrr/harbrr/internal/http"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/login"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/magnet"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/selector"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/template"
)

// ResolveDownload turns a release's download link into the real torrent URL,
// reproducing Jackett's CardigannIndexer.Download(Uri link) for the full download
// block. The fixed order, matching Jackett, is:
//
//  1. populate the .DownloadUri template namespace from the link;
//  2. run the download.before pre-request (its pathselector, when present, GETs the
//     link and REPLACES before.path; then the before request issues with its inputs
//     as a GET query or POST body);
//  3. if download.infohash is present, extract hash+title (from the before response
//     when usebeforeresponse, else a fresh GET of the link) and synthesise a magnet
//     — infohash takes precedence over selectors, and an empty extraction falls back
//     to returning the link unchanged (Jackett's base.Download(link) fallback);
//  4. otherwise try each download.selector in order: read its href, resolve it
//     against the link, and (unless it is a magnet and testlinktorrent is disabled)
//     validate the resolved link is a bencoded torrent — the first valid match wins;
//  5. with no infohash and no selectors, return the link unchanged.
//
// download.method/download.headers govern the FINAL torrent fetch (the /dl proxy),
// not the resolution GETs here; download.headers are still attached to the page
// GETs and the testlinktorrent validation as Jackett does. Every error site redacts
// the (passkey-bearing) link.
//
// validate controls the testlinktorrent gate. It is a GRAB-TIME validation (fetch
// the resolved link and check it is a bencoded torrent), so callers pass true only
// when resolving for an actual grab (the /dl proxy, and the parity harness that
// simulates one). Feed-time pre-resolution passes false: validating every served
// release would fetch one torrent per release per poll — the per-search hammering
// the engine must never do.
func ResolveDownload(ctx context.Context, def *loader.Definition, link string, session *login.Session, doer Doer, deps Deps, validate bool) (string, error) {
	dl := def.Download
	if dl == nil {
		return link, nil
	}

	du, err := parseDownloadURI(link)
	if err != nil {
		return "", err
	}
	headers, err := renderDownloadHeaders(def, du, deps)
	if err != nil {
		return "", err
	}

	var beforeBody []byte
	if dl.Before != nil {
		beforeBody, err = fetchBefore(ctx, dl, link, du, headers, session, doer, deps)
		if err != nil {
			return "", err
		}
	}

	if dl.InfoHash != nil {
		resolved, ok, err := resolveInfoHash(ctx, dl, link, du, beforeBody, headers, session, doer, deps)
		if err != nil {
			return "", err
		}
		if ok {
			return resolved, nil
		}
		// Jackett swallows an infohash miss and falls back to base.Download(link).
		return link, nil
	}

	if len(dl.Selectors) > 0 {
		return resolveSelectors(ctx, def, dl, link, du, beforeBody, headers, session, doer, deps, validate)
	}
	return link, nil
}

// parseDownloadURI parses the link into the .DownloadUri template namespace.
func parseDownloadURI(link string) (*template.DownloadURI, error) {
	u, err := url.Parse(link)
	if err != nil {
		return nil, fmt.Errorf("parsing download link %q: %w", apphttp.RedactURL(link), err)
	}
	return template.NewDownloadURI(u), nil
}

// downloadContext builds a fresh template context for a download/before template:
// the base request context (config + sitelink + today) with the .DownloadUri
// namespace populated. Fresh per call because template.Eval mutates the context.
func downloadContext(du *template.DownloadURI, deps Deps) *template.Context {
	ctx := requestContext(Query{}, deps)
	ctx.DownloadUri = du
	return ctx
}

// renderDownloadHeaders renders download.headers (else search.headers) against the
// download context, mirroring Jackett's ParseCustomHeaders(Download.Headers ??
// Search.Headers, variables) used for the resolution GETs.
func renderDownloadHeaders(def *loader.Definition, du *template.DownloadURI, deps Deps) (map[string][]string, error) {
	// A login-auth def routed through /dl has no download block, so guard the nil:
	// fall back to search.headers (where an auth header like X-API-KEY lives), the
	// same source the search request used.
	var raw map[string][]string
	if def.Download != nil {
		raw = def.Download.Headers
	}
	if raw == nil {
		raw = def.Search.Headers
	}
	// renderHeaders returns an empty (non-nil) map for nil input, so the
	// no-headers case needs no special handling.
	return renderHeaders(raw, downloadContext(du, deps))
}

// fetchBefore issues the download.before pre-request. When before.pathselector is
// set it first GETs the link, runs the selector over that page, and uses the result
// as before.path (Jackett overwrites Before.Path with it). The path is then
// template-rendered, resolved against the base URL, and issued GET (inputs on the
// query, joined by before.queryseparator) or POST (inputs as the form body).
func fetchBefore(ctx context.Context, dl *loader.DownloadBlock, link string, du *template.DownloadURI, headers map[string][]string, session *login.Session, doer Doer, deps Deps) ([]byte, error) {
	before := dl.Before
	path := before.Path
	if before.PathSelector != nil {
		linkBody, err := doRequest(ctx, doer, builtRequest{method: stdhttp.MethodGet, url: link, headers: headers}, session)
		if err != nil {
			return nil, err
		}
		selected, found, err := selectValue(du, linkBody, *before.PathSelector, deps)
		if err != nil {
			return nil, fmt.Errorf("download.before pathselector: %w", err)
		}
		if !found {
			return nil, fmt.Errorf("download.before pathselector matched nothing for %s", apphttp.RedactURL(link))
		}
		path = selected
	}

	rendered, err := template.Eval(path, downloadContext(du, deps))
	if err != nil {
		return nil, fmt.Errorf("rendering download.before path: %w", err)
	}
	absURL, err := resolveURL(deps.BaseURL, rendered)
	if err != nil {
		return nil, err
	}

	pairs, err := renderInputs(before.Inputs, downloadContext(du, deps), true)
	if err != nil {
		return nil, err
	}
	br, err := buildBeforeRequest(before, absURL, pairs, headers)
	if err != nil {
		return nil, err
	}
	return doRequest(ctx, doer, br, session)
}

// buildBeforeRequest finalises the before request: POST form-encodes the inputs
// into the body; GET appends them to the URL joined by before.queryseparator
// (default "&").
func buildBeforeRequest(before *loader.BeforeBlock, absURL string, pairs []kv, headers map[string][]string) (builtRequest, error) {
	if strings.EqualFold(before.Method, stdhttp.MethodPost) {
		return builtRequest{
			method:  stdhttp.MethodPost,
			url:     absURL,
			body:    encodeOrdered(pairs),
			headers: withFormContentType(headers),
		}, nil
	}
	full, err := appendQuerySep(absURL, pairs, before.QuerySeparator)
	if err != nil {
		return builtRequest{}, err
	}
	return builtRequest{method: stdhttp.MethodGet, url: full, headers: headers}, nil
}

// resolveInfoHash extracts the info hash and title (from the before response when
// usebeforeresponse, else a fresh GET of the link) and synthesises a magnet. ok is
// false on an empty extraction (the caller falls back to the link); a transport or
// parse error propagates (a deliberate, safe divergence from Jackett, which swallows
// it — the resolved-artifact golden is unaffected because it only pins successful
// extraction and the empty-extraction fallback).
func resolveInfoHash(ctx context.Context, dl *loader.DownloadBlock, link string, du *template.DownloadURI, beforeBody []byte, headers map[string][]string, session *login.Session, doer Doer, deps Deps) (string, bool, error) {
	ih := dl.InfoHash
	if ih.Hash == nil || ih.Title == nil {
		return "", false, nil
	}
	body := beforeBody
	if !boolVal(ih.UseBeforeResponse) || dl.Before == nil || beforeBody == nil {
		b, err := doRequest(ctx, doer, builtRequest{method: stdhttp.MethodGet, url: link, headers: headers}, session)
		if err != nil {
			return "", false, err
		}
		body = b
	}

	hash, found, err := selectValue(du, body, *ih.Hash, deps)
	if err != nil {
		return "", false, err
	}
	if !found || hash == "" {
		return "", false, nil
	}
	title, found, err := selectValue(du, body, *ih.Title, deps)
	if err != nil {
		return "", false, err
	}
	if !found || title == "" {
		return "", false, nil
	}
	m := magnet.FromInfoHash(hash, title)
	if m == "" {
		return "", false, nil
	}
	return m, true, nil
}

// resolveSelectors tries each download selector in order: read its href (from the
// before response when usebeforeresponse, else a fresh GET of the link), resolve it
// against the link, and validate it via testlinktorrent. The first valid match
// wins; no match is an error (Jackett's "Download selectors didn't match").
func resolveSelectors(ctx context.Context, def *loader.Definition, dl *loader.DownloadBlock, link string, du *template.DownloadURI, beforeBody []byte, headers map[string][]string, session *login.Session, doer Doer, deps Deps, validate bool) (string, error) {
	for i := range dl.Selectors {
		sel := dl.Selectors[i]
		body, err := selectorPageBody(ctx, sel, dl, link, beforeBody, headers, session, doer)
		if err != nil {
			return "", err
		}
		href, found, err := selectValue(du, body, sel, deps)
		if err != nil {
			return "", err
		}
		if !found {
			continue
		}
		resolved, err := resolveURL(link, href)
		if err != nil {
			return "", err
		}
		ok, err := passesTorrentTest(ctx, def, resolved, headers, session, doer, validate)
		if err != nil {
			return "", err
		}
		if ok {
			return resolved, nil
		}
	}
	return "", fmt.Errorf("download: no selector matched for %s", apphttp.RedactURL(link))
}

// selectorPageBody returns the body a selector reads: the before response when the
// selector opts in (and a before response exists), otherwise a fresh GET of the link.
func selectorPageBody(ctx context.Context, sel loader.SelectorField, dl *loader.DownloadBlock, link string, beforeBody []byte, headers map[string][]string, session *login.Session, doer Doer) ([]byte, error) {
	if boolVal(sel.UseBeforeResponse) && dl.Before != nil && beforeBody != nil {
		return beforeBody, nil
	}
	return doRequest(ctx, doer, builtRequest{method: stdhttp.MethodGet, url: link, headers: headers}, session)
}

// passesTorrentTest reproduces Jackett's testlinktorrent gate (definition-level,
// default TRUE): a non-magnet resolved link is fetched and accepted only if its
// first byte is 'd' (the start of a bencoded dict); empty content passes. The check
// is skipped entirely when validate is false (feed-time pre-resolution), for a
// magnet, or for testlinktorrent: false. When it runs it is bounded — one request
// per matched selector, at grab time — through the same stub-able doer.
func passesTorrentTest(ctx context.Context, def *loader.Definition, resolved string, headers map[string][]string, session *login.Session, doer Doer, validate bool) (bool, error) {
	if !validate || isMagnet(resolved) || !testLinkEnabled(def) {
		return true, nil
	}
	body, err := doRequest(ctx, doer, builtRequest{method: stdhttp.MethodGet, url: resolved, headers: headers}, session)
	if err != nil {
		// A rate-limit propagates so the registry backs off; any other fetch failure
		// (unreachable, 4xx/5xx) means this link is not a usable torrent, so advance to
		// the next selector — Jackett's per-selector try/catch continue.
		var rle *RateLimitedError
		if errors.As(err, &rle) {
			return false, err
		}
		return false, nil
	}
	if len(body) >= 1 && body[0] != 'd' {
		return false, nil
	}
	return true, nil
}

// selectValue runs one selector field over a fetched page: template-render the
// selector string (so .DownloadUri/.Config resolve in it), query the whole document
// for the element, read its attribute (or text), then apply the filter chain. found
// is false when the selector matched nothing.
func selectValue(du *template.DownloadURI, body []byte, sel loader.SelectorField, deps Deps) (string, bool, error) {
	rendered, err := template.Eval(sel.Selector, downloadContext(du, deps))
	if err != nil {
		return "", false, fmt.Errorf("rendering download selector %q: %w", sel.Selector, err)
	}

	eng := selector.New()
	doc, err := eng.ParseHTML(body)
	if err != nil {
		return "", false, fmt.Errorf("parsing download page: %w", err)
	}
	value, found, err := eng.Field(doc.Root(), loader.SelectorBlock{Selector: rendered, Attribute: sel.Attribute})
	if err != nil {
		return "", false, fmt.Errorf("download selector %q: %w", rendered, err)
	}
	if !found {
		return "", false, nil
	}
	value, err = deps.Filters.Apply(value, sel.Filters)
	if err != nil {
		return "", false, fmt.Errorf("download selector %q filters: %w", rendered, err)
	}
	return value, true, nil
}

// isMagnet reports whether link is a magnet URI (testlinktorrent skips these).
func isMagnet(link string) bool {
	return strings.HasPrefix(link, "magnet:")
}

// testLinkEnabled reports whether testlinktorrent validation runs, defaulting to
// TRUE (Jackett's Testlinktorrent default) when the definition omits it.
func testLinkEnabled(def *loader.Definition) bool {
	return def.TestLinkTorrent == nil || *def.TestLinkTorrent
}

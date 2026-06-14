package search

import (
	"context"
	"fmt"
	stdhttp "net/http"
	"strings"

	apphttp "github.com/autobrr/harbrr/internal/http"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/login"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/selector"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/template"
)

// ResolveDownload turns a release's download link into the real torrent URL when
// the definition declares a download block, reproducing the selectors/before
// half of Jackett's CardigannIndexer.Download: optionally issue a "before"
// pre-request, then try each download selector over the fetched page (or the
// before response when the selector opts in) and resolve the first matching href
// against the link.
//
// A def with no download block (or no selectors) returns the link unchanged —
// the link is already the torrent. Scope (matching the offline matrix fixture):
// before.path/method and selector selector/attribute/filters/usebeforeresponse.
// Out of scope, and documented as such: before.inputs/pathselector,
// download.infohash, download.method=post, download.headers, the .DownloadUri
// template namespace, and testlinktorrent.
func ResolveDownload(ctx context.Context, def *loader.Definition, link string, session *login.Session, doer Doer, deps Deps) (string, error) {
	dl := def.Download
	if dl == nil || len(dl.Selectors) == 0 {
		return link, nil
	}

	var beforeBody []byte
	if dl.Before != nil {
		body, err := fetchBefore(ctx, dl.Before, session, doer, deps)
		if err != nil {
			return "", err
		}
		beforeBody = body
	}

	for i := range dl.Selectors {
		resolved, ok, err := tryDownloadSelector(ctx, dl, dl.Selectors[i], link, beforeBody, session, doer, deps)
		if err != nil {
			return "", err
		}
		if ok {
			return resolved, nil
		}
	}
	return "", fmt.Errorf("download: no selector matched for %s", apphttp.RedactURL(link))
}

// tryDownloadSelector fetches the page the selector reads (the before response
// when it opts in, otherwise the link page), matches the selector, and resolves
// the href against the link. ok is false when the selector matched nothing.
func tryDownloadSelector(ctx context.Context, dl *loader.DownloadBlock, sel loader.SelectorField, link string, beforeBody []byte, session *login.Session, doer Doer, deps Deps) (string, bool, error) {
	body := beforeBody
	if !boolVal(sel.UseBeforeResponse) || dl.Before == nil || beforeBody == nil {
		b, err := doRequest(ctx, doer, builtRequest{method: stdhttp.MethodGet, url: link}, session)
		if err != nil {
			return "", false, err
		}
		body = b
	}

	href, found, err := matchDownloadHref(body, sel, deps)
	if err != nil {
		return "", false, err
	}
	if !found {
		return "", false, nil
	}
	resolved, err := resolveURL(link, href)
	if err != nil {
		return "", false, err
	}
	return resolved, true, nil
}

// fetchBefore issues the download "before" pre-request: render its path, resolve
// against the base URL, and GET (or POST) it carrying the session cookies.
func fetchBefore(ctx context.Context, before *loader.BeforeBlock, session *login.Session, doer Doer, deps Deps) ([]byte, error) {
	tmplCtx := requestContext(Query{}, deps)
	rendered, err := template.Eval(before.Path, tmplCtx)
	if err != nil {
		return nil, fmt.Errorf("rendering download.before path: %w", err)
	}
	absURL, err := resolveURL(deps.BaseURL, rendered)
	if err != nil {
		return nil, err
	}
	method := stdhttp.MethodGet
	if strings.EqualFold(before.Method, stdhttp.MethodPost) {
		method = stdhttp.MethodPost
	}
	return doRequest(ctx, doer, builtRequest{method: method, url: absURL}, session)
}

// matchDownloadHref runs one download selector over a fetched page: query the
// whole document (not rows) for the element, read its attribute (or text), then
// apply the selector's filter chain. found is false when nothing matched.
func matchDownloadHref(body []byte, sel loader.SelectorField, deps Deps) (string, bool, error) {
	eng := selector.New()
	doc, err := eng.ParseHTML(body)
	if err != nil {
		return "", false, fmt.Errorf("parsing download page: %w", err)
	}

	block := loader.SelectorBlock{Selector: sel.Selector, Attribute: sel.Attribute}
	value, found, err := eng.Field(doc.Root(), block)
	if err != nil {
		return "", false, fmt.Errorf("download selector %q: %w", sel.Selector, err)
	}
	if !found {
		return "", false, nil
	}

	value, err = deps.Filters.Apply(value, sel.Filters)
	if err != nil {
		return "", false, fmt.Errorf("download selector %q filters: %w", sel.Selector, err)
	}
	return value, true, nil
}

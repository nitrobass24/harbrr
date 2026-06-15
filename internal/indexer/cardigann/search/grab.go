package search

import (
	"context"
	"fmt"
	stdhttp "net/http"
	"strings"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/login"
)

// torrentContentType is what the /dl proxy serves a fetched .torrent as.
const torrentContentType = "application/x-bittorrent"

// GrabResult is the outcome of a grab-time download: either a torrent body to serve
// (Body, for an http(s) link) or a Redirect target (for a magnet, which carries no
// secret and needs no proxying). Exactly one of Body / Redirect is set.
type GrabResult struct {
	Body        []byte
	ContentType string
	Redirect    string
}

// Grab performs the full grab-time download for a release link, reproducing the
// tail of Jackett's Download: resolve the link (with testlinktorrent validation),
// then fetch the resolved torrent through the session honouring download.method and
// download.headers — the FINAL base.Download Jackett issues. A resolved magnet is
// returned as a Redirect (it is public, so the /dl proxy can 302 to it). This is
// the grab-time counterpart the /dl proxy drives so the passkey-bearing link is
// resolved and fetched server-side, never in the served feed.
func Grab(ctx context.Context, def *loader.Definition, link string, session *login.Session, doer Doer, deps Deps) (*GrabResult, error) {
	resolved, err := ResolveDownload(ctx, def, link, session, doer, deps, true)
	if err != nil {
		return nil, err
	}
	if isMagnet(resolved) {
		return &GrabResult{Redirect: resolved}, nil
	}

	// .DownloadUri for the header templates comes from the original link, matching
	// Jackett's variables built once from the Download(link) argument.
	du, err := parseDownloadURI(link)
	if err != nil {
		return nil, err
	}
	headers, err := renderDownloadHeaders(def, du, deps)
	if err != nil {
		return nil, err
	}

	method := stdhttp.MethodGet
	if def.Download != nil && strings.EqualFold(def.Download.Method, stdhttp.MethodPost) {
		method = stdhttp.MethodPost
	}
	body, err := doRequest(ctx, doer, builtRequest{method: method, url: resolved, headers: headers}, session)
	if err != nil {
		return nil, fmt.Errorf("fetching torrent: %w", err)
	}
	return &GrabResult{Body: body, ContentType: torrentContentType}, nil
}

// Package announce pushes harbrr's newly-seen releases to cross-seed tools (qui
// cross-seed + cross-seed v6) so a tracker harbrr already polls feeds cross-seed with no
// second poll. harbrr is only the messenger — the cross-seed tools do the matching. The
// .torrent is fetched (qui) or linked (cross-seed v6) only on a confirmed match, so this
// is strictly less tracker load than a consumer polling + grabbing. Secrets — the tool's
// API key and harbrr's apikey-bearing /dl link — are redacted in logs and never echoed in
// errors.
package announce

import (
	"context"
	"fmt"
	"net/http"
	"time"

	apphttp "github.com/autobrr/harbrr/internal/http"
)

// httpClientTimeout bounds a single push so an unresponsive cross-seed tool cannot hang
// the announce worker.
const httpClientTimeout = 30 * time.Second

// apiKeyHeader is the header both tools authenticate the push with (qui's X-API-Key and
// cross-seed v6's x-api-key are the same header, case-insensitive).
const apiKeyHeader = "X-API-Key" //nolint:gosec // G101: an HTTP header name, not a credential.

func defaultHTTPClient() *http.Client {
	return &http.Client{Timeout: httpClientTimeout, CheckRedirect: refuseCrossHostRedirect}
}

// refuseCrossHostRedirect stops the client from following a redirect to a different host
// than the original request. Go strips only Authorization/Cookie/WWW-Authenticate on a
// cross-origin hop, not custom headers, so an open redirect would otherwise carry the
// X-API-Key to the redirect target. A same-host redirect (e.g. an http->https upgrade) is
// still followed. The error names both hosts (hosts are not secrets) but no query string.
func refuseCrossHostRedirect(req *http.Request, via []*http.Request) error {
	if len(via) == 0 || req.URL.Host == via[0].URL.Host {
		return nil
	}
	return fmt.Errorf("announce: refusing redirect from host %q to %q (would leak the api key)",
		via[0].URL.Host, req.URL.Host)
}

// Release is one new release harbrr offers to a cross-seed tool.
type Release struct {
	Name    string // the torrent/release name
	Size    int64  // size in bytes
	Indexer string // the indexer the cross-seed tool keys on (harbrr slug)
	GUID    string // stable release id (cross-seed v6 `guid`)
	Tracker string // tracker identifier (cross-seed v6 `tracker`)
	// DownloadURL is harbrr's /dl proxy URL (apikey-bearing). cross-seed v6 fetches it
	// itself; the qui driver fetches it via a TorrentFetcher and base64-encodes the bytes.
	// SECRET — it carries harbrr's feed apikey; never log it.
	DownloadURL string
}

// Result is the outcome of one announce. Matched is true when the tool accepted the
// release for cross-seeding (qui recommendation=="download"; cross-seed v6 injected it);
// a no-match is Result{Matched:false} with a nil error, not a failure.
type Result struct {
	Matched bool
	Detail  string
}

// TorrentFetcher fetches the .torrent bytes for a release's DownloadURL (through harbrr's
// own /dl, which holds the tracker creds). Only qui's two-step push needs it.
type TorrentFetcher func(ctx context.Context, downloadURL string) ([]byte, error)

// Target pushes one release to a cross-seed tool. A no-match returns Result{Matched:false}
// with nil error; network/auth failures return a scrubbed error.
type Target interface {
	Announce(ctx context.Context, rel Release) (Result, error)
	// Probe checks the tool is reachable — and, where the tool exposes a suitable
	// non-mutating endpoint, that the API key is accepted — WITHOUT injecting anything.
	// The management API's Test action uses it. A nil error is a pass; a scrubbed error
	// means unreachable/unauthorized. Reachability-vs-credentials coverage is per-kind
	// (qui validates the key; cross-seed v6 checks reachability only).
	Probe(ctx context.Context) error
	// AnnounceTimeout is the CEILING on one Announce call for this target — how long
	// harbrr is willing to wait for a verdict, not how long a push is expected to take.
	// It is per-target because the tools differ in what hanging up early costs: qui
	// finishes the work regardless (see quiAnnounceTimeout), cross-seed v6 makes no such
	// promise. pushOne applies it to every release.
	AnnounceTimeout() time.Duration
}

// poster is the announce package's view of the shared authenticated-JSON transport:
// an api-keyed JSON call to the cross-seed tool that never echoes the request URL or
// body (both carry secrets) into an error.
type poster struct {
	c *apphttp.JSONClient
}

// newPoster builds the transport for one cross-seed tool. kind labels it in every
// error ("announce: qui: ..."); apiKey both authenticates the push and is scrubbed by
// value from any error the tool's response can produce.
func newPoster(kind, baseURL, apiKey string, client *http.Client) poster {
	return poster{c: apphttp.NewJSONClient(apphttp.JSONClient{
		Prefix: "announce: " + kind,
		Base:   baseURL,
		Auth:   http.Header{apiKeyHeader: {apiKey}},
		Client: client,
		Secret: apiKey,
	})}
}

// post sends body as JSON to baseURL+path, decoding a 2xx response into out (when
// non-nil). It returns the HTTP status (set even on the error path so a caller can
// branch on, e.g., 404) plus a scrubbed error.
func (p poster) post(ctx context.Context, path string, body, out any) (int, error) {
	return p.c.Do(ctx, http.MethodPost, path, body, out)
}

// get sends an authenticated GET to baseURL+path, decoding a 2xx response into out
// (when non-nil). It is used by non-mutating reachability probes (cross-seed v6's
// /api/ping).
func (p poster) get(ctx context.Context, path string, out any) (int, error) {
	return p.c.Do(ctx, http.MethodGet, path, nil, out)
}

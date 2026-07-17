// Package announce pushes harbrr's newly-seen releases to cross-seed tools (qui
// cross-seed + cross-seed v6) so a tracker harbrr already polls feeds cross-seed with no
// second poll. harbrr is only the messenger — the cross-seed tools do the matching. The
// .torrent is fetched (qui) or linked (cross-seed v6) only on a confirmed match, so this
// is strictly less tracker load than a consumer polling + grabbing. Secrets — the tool's
// API key and harbrr's apikey-bearing /dl link — are redacted in logs and never echoed in
// errors.
package announce

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
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
}

// poster carries the shared HTTP machinery both drivers reuse: an authenticated JSON POST
// that never echoes the request URL or body (both carry secrets) into an error.
type poster struct {
	kind    string
	baseURL string
	apiKey  string
	client  *http.Client
}

// post sends body as JSON to baseURL+path with the api-key header, decoding a 2xx response
// into out (when non-nil). See do for the return contract and redaction guarantees.
func (p *poster) post(ctx context.Context, path string, body, out any) (int, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return 0, fmt.Errorf("announce: %s: marshal request: %w", p.kind, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+path, bytes.NewReader(payload))
	if err != nil {
		return 0, fmt.Errorf("announce: %s: build request: %w", p.kind, scrubURLError(err))
	}
	req.Header.Set("Content-Type", "application/json")
	return p.do(req, path, out)
}

// get sends an authenticated GET to baseURL+path, decoding a 2xx response into out (when
// non-nil). It is used by non-mutating reachability probes (cross-seed v6's /api/ping).
// See do for the return contract and redaction guarantees.
func (p *poster) get(ctx context.Context, path string, out any) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.baseURL+path, nil)
	if err != nil {
		return 0, fmt.Errorf("announce: %s: build request: %w", p.kind, scrubURLError(err))
	}
	return p.do(req, path, out)
}

// do sets the api-key header, sends req, and decodes a 2xx response into out (when
// non-nil). It returns the HTTP status (set even on the error path so a caller can branch
// on, e.g., 404) plus a scrubbed error for any transport failure or non-2xx status. The
// response body is never surfaced — it can reproduce the request, which carries the api
// key and the /dl link.
func (p *poster) do(req *http.Request, path string, out any) (int, error) {
	req.Header.Set(apiKeyHeader, p.apiKey)
	// G704: the URL is p.baseURL (the operator-configured cross-seed tool address,
	// validated absolute http(s) at create/update) + a fixed path — never end-user
	// input. Reaching that address is the whole point, so this is not attacker SSRF.
	resp, err := p.client.Do(req) //nolint:gosec // G704: operator-configured tool URL, not user input.
	if err != nil {
		return 0, fmt.Errorf("announce: %s: %s: %w", p.kind, path, scrubURLError(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Body not echoed (it can reproduce the secret-bearing request); only the status.
		return resp.StatusCode, fmt.Errorf("announce: %s: %s: status %d", p.kind, path, resp.StatusCode)
	}
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return resp.StatusCode, fmt.Errorf("announce: %s: decode %s response: %w", p.kind, path, err)
		}
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode, nil
}

// scrubURLError strips the request URL (which may carry an api key) from a *url.Error.
func scrubURLError(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return fmt.Errorf("%s: %w", ue.Op, ue.Err)
	}
	return err
}

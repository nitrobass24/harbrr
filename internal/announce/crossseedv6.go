package announce

import (
	"context"
	"net/http"
	"strings"
)

// cross-seed v6 is a one-step push: harbrr POSTs the release metadata plus a link, and
// cross-seed fetches the link (harbrr's /dl proxy) itself if it decides to inject. harbrr
// never fetches the .torrent, so the only tracker contact is cross-seed's own fetch on a
// confirmed match.
const (
	csv6AnnouncePath = "/api/announce"
	// csv6PingPath is cross-seed v6's purpose-built, UNAUTHENTICATED health endpoint.
	// It is the only non-mutating endpoint the tool exposes, so Probe can confirm
	// reachability with it but cannot validate the API key (ping ignores it).
	csv6PingPath = "/api/ping"
)

// csv6Request is the /api/announce contract. Link is harbrr's /dl?apikey=… proxy URL —
// cross-seed fetches it, so harbrr holds the tracker creds and the passkey never leaves
// harbrr. SECRET-bearing; never logged.
type csv6Request struct {
	Name    string `json:"name"`
	GUID    string `json:"guid"`
	Link    string `json:"link"`
	Tracker string `json:"tracker"`
}

// csv6Announcer implements Target for a cross-seed v6 instance.
type csv6Announcer struct {
	poster
}

var _ Target = (*csv6Announcer)(nil)

// NewCrossSeedV6 builds a cross-seed v6 announce Target.
func NewCrossSeedV6(baseURL, apiKey string, client *http.Client) Target {
	if client == nil {
		client = defaultHTTPClient()
	}
	return &csv6Announcer{
		poster: poster{kind: "cross-seed", baseURL: strings.TrimRight(baseURL, "/"), apiKey: apiKey, client: client},
	}
}

// Probe checks cross-seed v6 is reachable via its unauthenticated /api/ping health
// endpoint. cross-seed v6 exposes no authed non-mutating endpoint, so this validates
// REACHABILITY ONLY — a wrong API key is not detected here (ping ignores it). Any non-2xx
// / transport failure is a scrubbed error.
func (c *csv6Announcer) Probe(ctx context.Context) error {
	if _, err := c.get(ctx, csv6PingPath, nil); err != nil {
		return err
	}
	return nil
}

// Announce posts the release to /api/announce. cross-seed v6 answers 200 when it found and
// injected a match and 204 when there was nothing to do; both are success (204 = clean
// no-match). Any non-2xx is a scrubbed error.
func (c *csv6Announcer) Announce(ctx context.Context, rel Release) (Result, error) {
	status, err := c.post(ctx, csv6AnnouncePath, csv6Request{
		Name: rel.Name, GUID: rel.GUID, Link: rel.DownloadURL, Tracker: rel.Tracker,
	}, nil)
	if err != nil {
		return Result{}, err
	}
	return Result{Matched: status == http.StatusOK}, nil
}

package announce

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	apphttp "github.com/autobrr/harbrr/internal/http"
)

// qui's cross-seed is a two-step push: a webhook check decides whether the release is
// worth cross-seeding, and only on recommendation "download" does harbrr fetch the
// .torrent (via its own /dl) and apply it. The check carries no torrent bytes, so a
// no-match costs one cheap request and never touches the tracker.
const (
	quiCheckPath = "/api/cross-seed/webhook/check"
	quiApplyPath = "/api/cross-seed/apply"

	quiRecommendationDownload = "download"

	// quiProbeName is the synthetic torrent name / indexer a Probe's webhook/check
	// carries. It never matches a real release, so the check is a pure reachability +
	// auth probe with no side effect (apply is never reached).
	quiProbeName = "harbrr-connection-test"
)

// quiAnnounceTimeout is the ceiling on ONE qui Announce (check + torrent fetch + apply).
// It bounds all three steps, but only the two qui calls actually have the full window: the
// torrent-fetch step keeps a tighter wall of its own, because DefaultTargetFactory builds
// the TorrentFetcher from the un-widened client (see widenTimeout), so a /dl fetch still
// aborts at that client's Timeout. That is deliberate — 30s is plenty to fetch a .torrent,
// and #527's failures are all on apply, which does get the full window.
// The length is the SAFE choice here: qui runs the apply under
// context.WithoutCancel, so hanging up early does not cancel the injection — it only
// throws away the verdict, making harbrr report a failure that never happened (#527).
// For the same reason no retry may be layered on top: qui already did the work, so a
// retry double-pushes. 120s matches autobrr's own webhook action, the reference caller of
// this endpoint, which likewise never inspects the response and never retries.
const quiAnnounceTimeout = 120 * time.Second

// quiCheckRequest / quiCheckResponse are the webhook/check contract (the subset harbrr
// sends + reads).
type quiCheckRequest struct {
	TorrentName string `json:"torrentName"`
	Size        int64  `json:"size"`
	Indexer     string `json:"indexer"`
}

type quiCheckResponse struct {
	CanCrossSeed   bool   `json:"canCrossSeed"`
	Recommendation string `json:"recommendation"`
}

// quiApplyRequest is the apply contract: base64 .torrent bytes (NOT a URL — harbrr holds
// the tracker creds, so it fetches the link itself and hands qui the bytes), the indexer,
// and optional tags.
type quiApplyRequest struct {
	TorrentData string   `json:"torrentData"`
	Indexer     string   `json:"indexer"`
	Tags        []string `json:"tags,omitempty"`
}

// quiAnnouncer implements Target for a qui cross-seed instance. The embedded poster
// carries the announce path (its client's Timeout is widened to fit quiAnnounceTimeout);
// probePoster keeps the injected client untouched so Probe's bound is unchanged.
type quiAnnouncer struct {
	poster
	probePoster poster
	fetch       TorrentFetcher
	tags        []string
}

var _ Target = (*quiAnnouncer)(nil)

// NewQui builds a qui cross-seed announce Target. fetch fetches the .torrent bytes for a
// matched release through harbrr's /dl; tags are applied to every injected torrent.
func NewQui(baseURL, apiKey string, client *http.Client, fetch TorrentFetcher, tags []string) Target {
	if client == nil {
		client = defaultHTTPClient()
	}
	base := poster{kind: "qui", baseURL: strings.TrimRight(baseURL, "/"), apiKey: apiKey, client: client}
	announcing := base
	announcing.client = widenTimeout(client, quiAnnounceTimeout)
	return &quiAnnouncer{
		poster:      announcing,
		probePoster: base,
		fetch:       fetch,
		tags:        tags,
	}
}

// widenTimeout returns client with its Timeout raised to at least d. http.Client.Timeout
// is a hard wall that a longer request context cannot lift, so the shared 30s client
// would abort a slow qui apply long before quiAnnounceTimeout — the fix would be inert
// past 30s. The copy is shallow, so the Transport (and its connection pool) is shared; a
// zero Timeout already means "no wall" and is left alone. Only the announce poster is
// widened: Probe and the TorrentFetcher keep the client they were handed.
func widenTimeout(client *http.Client, d time.Duration) *http.Client {
	if client.Timeout == 0 || client.Timeout >= d {
		return client
	}
	widened := *client
	widened.Timeout = d
	return &widened
}

// AnnounceTimeout is qui's own ceiling — see quiAnnounceTimeout for why it is safe to
// wait this long.
func (q *quiAnnouncer) AnnounceTimeout() time.Duration { return quiAnnounceTimeout }

// Announce runs the two-step push: check, then (on recommendation "download") fetch + apply.
// A 404 from check, or any recommendation other than "download", is a clean no-match.
func (q *quiAnnouncer) Announce(ctx context.Context, rel Release) (Result, error) {
	var cr quiCheckResponse
	status, err := q.post(ctx, quiCheckPath, quiCheckRequest{
		TorrentName: rel.Name, Size: rel.Size, Indexer: rel.Indexer,
	}, &cr)
	if err != nil {
		if status == http.StatusNotFound {
			return Result{Matched: false, Detail: "no match"}, nil
		}
		return Result{}, err
	}
	if cr.Recommendation != quiRecommendationDownload {
		return Result{Matched: false, Detail: "qui recommendation: " + cr.Recommendation}, nil
	}
	return q.apply(ctx, rel)
}

// Probe validates qui without injecting anything: it POSTs a synthetic webhook/check
// (the same non-mutating first step Announce uses) with a token that matches no real
// release, so apply is never reached. A 2xx (a real check verdict) and a 404 ("no match")
// both mean the endpoint is reachable and the key was accepted; any other non-2xx /
// transport failure is a scrubbed error. It runs on probePoster — the injected client as
// given — so a Test against a hung qui is still bounded the way it always was, not by the
// announce path's much longer ceiling.
func (q *quiAnnouncer) Probe(ctx context.Context) error {
	status, err := q.probePoster.post(ctx, quiCheckPath, quiCheckRequest{
		TorrentName: quiProbeName, Size: 0, Indexer: quiProbeName,
	}, nil)
	if err != nil && status != http.StatusNotFound {
		return err
	}
	return nil
}

// apply fetches the .torrent bytes for a matched release and posts them base64-encoded.
func (q *quiAnnouncer) apply(ctx context.Context, rel Release) (Result, error) {
	if q.fetch == nil {
		return Result{}, errors.New("announce: qui: no torrent fetcher configured")
	}
	data, err := q.fetch(ctx, rel.DownloadURL)
	if err != nil {
		// The fetcher error may carry the /dl URL (apikey-bearing); scrub it.
		return Result{}, fmt.Errorf("announce: qui: fetch torrent: %w", apphttp.ScrubURLError(err))
	}
	if len(data) == 0 {
		// An empty /dl body would POST torrentData:"" — garbage to qui; treat as a failure.
		return Result{}, errors.New("announce: qui: fetched torrent is empty")
	}
	if _, err := q.post(ctx, quiApplyPath, quiApplyRequest{
		TorrentData: base64.StdEncoding.EncodeToString(data), Indexer: rel.Indexer, Tags: q.tags,
	}, nil); err != nil {
		return Result{}, err
	}
	return Result{Matched: true}, nil
}

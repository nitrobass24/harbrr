package announce

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// qui's cross-seed is a two-step push: a webhook check decides whether the release is
// worth cross-seeding, and only on recommendation "download" does harbrr fetch the
// .torrent (via its own /dl) and apply it. The check carries no torrent bytes, so a
// no-match costs one cheap request and never touches the tracker.
const (
	quiCheckPath = "/api/cross-seed/webhook/check"
	quiApplyPath = "/api/cross-seed/apply"

	quiRecommendationDownload = "download"
)

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

// quiAnnouncer implements Target for a qui cross-seed instance.
type quiAnnouncer struct {
	poster
	fetch TorrentFetcher
	tags  []string
}

var _ Target = (*quiAnnouncer)(nil)

// NewQui builds a qui cross-seed announce Target. fetch fetches the .torrent bytes for a
// matched release through harbrr's /dl; tags are applied to every injected torrent.
func NewQui(baseURL, apiKey string, client *http.Client, fetch TorrentFetcher, tags []string) Target {
	if client == nil {
		client = defaultHTTPClient()
	}
	return &quiAnnouncer{
		poster: poster{kind: "qui", baseURL: strings.TrimRight(baseURL, "/"), apiKey: apiKey, client: client},
		fetch:  fetch,
		tags:   tags,
	}
}

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

// apply fetches the .torrent bytes for a matched release and posts them base64-encoded.
func (q *quiAnnouncer) apply(ctx context.Context, rel Release) (Result, error) {
	if q.fetch == nil {
		return Result{}, errors.New("announce: qui: no torrent fetcher configured")
	}
	data, err := q.fetch(ctx, rel.DownloadURL)
	if err != nil {
		// The fetcher error may carry the /dl URL (apikey-bearing); scrub it.
		return Result{}, fmt.Errorf("announce: qui: fetch torrent: %w", scrubURLError(err))
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

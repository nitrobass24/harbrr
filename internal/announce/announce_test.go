package announce

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// TestScrubURLError proves a *url.Error's URL (which can carry an apikey/passkey in
// the query, or userinfo credentials) never survives into the scrubbed error, while
// the operation and underlying cause remain; a non-URL error passes through unchanged.
func TestScrubURLError(t *testing.T) {
	t.Parallel()
	urlErr := &url.Error{
		Op:  "Post",
		URL: "http://harbrr:8787/api/indexers/tt/dl?apikey=feedsecret&passkey=NOTREALSECRET",
		Err: errors.New("dial tcp: connection refused"),
	}
	got := scrubURLError(urlErr).Error()
	if strings.Contains(got, "feedsecret") || strings.Contains(got, "NOTREALSECRET") || strings.Contains(got, "harbrr:8787") {
		t.Errorf("scrubURLError leaked URL/credentials: %q", got)
	}
	if !strings.Contains(got, "connection refused") {
		t.Errorf("scrubURLError dropped the underlying cause: %q", got)
	}

	plain := errors.New("boom")
	if got := scrubURLError(plain); !errors.Is(got, plain) {
		t.Errorf("scrubURLError altered a non-URL error: %v", got)
	}
}

const testAPIKey = "qui_secretkey" //nolint:gosec // synthetic test credential

func sampleRelease() Release {
	return Release{
		Name: "Some.Movie.1080p", Size: 1234567, Indexer: "tt", GUID: "harbrr-abc",
		Tracker: "tt", DownloadURL: "http://harbrr:8787/api/indexers/tt/dl?apikey=feedsecret&token=tok",
	}
}

// --- qui (two-step) ---

func TestQuiAnnounce_DownloadFetchesAndApplies(t *testing.T) {
	t.Parallel()
	var check quiCheckRequest
	var apply quiApplyRequest
	var gotKey string
	applyCalls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get("X-API-Key")
		switch r.URL.Path {
		case quiCheckPath:
			_ = json.NewDecoder(r.Body).Decode(&check)
			_ = json.NewEncoder(w).Encode(quiCheckResponse{CanCrossSeed: true, Recommendation: "download"})
		case quiApplyPath:
			applyCalls++
			_ = json.NewDecoder(r.Body).Decode(&apply)
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	fetched := ""
	fetch := func(_ context.Context, url string) ([]byte, error) { fetched = url; return []byte("d0:e"), nil }
	tgt := NewQui(srv.URL, testAPIKey, srv.Client(), fetch, []string{"harbrr"})

	rel := sampleRelease()
	res, err := tgt.Announce(context.Background(), rel)
	if err != nil {
		t.Fatalf("Announce: %v", err)
	}
	if !res.Matched {
		t.Error("Matched = false, want true")
	}
	if applyCalls != 1 {
		t.Errorf("apply called %d times, want 1", applyCalls)
	}
	if check.TorrentName != rel.Name || check.Size != rel.Size || check.Indexer != rel.Indexer {
		t.Errorf("check request = %+v, want name/size/indexer from the release", check)
	}
	if fetched != rel.DownloadURL {
		t.Errorf("fetched %q, want the release DownloadURL", fetched)
	}
	if apply.TorrentData != base64.StdEncoding.EncodeToString([]byte("d0:e")) {
		t.Errorf("apply torrentData = %q, want base64 of the fetched bytes", apply.TorrentData)
	}
	if apply.Indexer != rel.Indexer || len(apply.Tags) != 1 || apply.Tags[0] != "harbrr" {
		t.Errorf("apply request = %+v, want indexer + [harbrr] tags", apply)
	}
	if gotKey != testAPIKey {
		t.Errorf("X-API-Key = %q, want %q", gotKey, testAPIKey)
	}
}

func TestQuiAnnounce_SkipDoesNotFetchOrApply(t *testing.T) {
	t.Parallel()
	applyCalls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == quiApplyPath {
			applyCalls++
		}
		if r.URL.Path == quiCheckPath {
			_ = json.NewEncoder(w).Encode(quiCheckResponse{CanCrossSeed: false, Recommendation: "skip"})
		}
	}))
	defer srv.Close()

	fetchCalls := 0
	fetch := func(_ context.Context, _ string) ([]byte, error) { fetchCalls++; return []byte("x"), nil }
	tgt := NewQui(srv.URL, testAPIKey, srv.Client(), fetch, nil)

	res, err := tgt.Announce(context.Background(), sampleRelease())
	if err != nil {
		t.Fatalf("Announce: %v", err)
	}
	if res.Matched {
		t.Error("Matched = true, want false on a skip recommendation")
	}
	if fetchCalls != 0 || applyCalls != 0 {
		t.Errorf("skip path fetched %d / applied %d, want 0/0", fetchCalls, applyCalls)
	}
}

func TestQuiAnnounce_CheckNotFoundIsCleanNoMatch(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	tgt := NewQui(srv.URL, testAPIKey, srv.Client(), nil, nil)

	res, err := tgt.Announce(context.Background(), sampleRelease())
	if err != nil {
		t.Fatalf("Announce: %v, want nil (404 = no match)", err)
	}
	if res.Matched {
		t.Error("Matched = true, want false on a 404 check")
	}
}

func TestQuiAnnounce_ServerErrorIsScrubbed(t *testing.T) {
	t.Parallel()
	const leak = "qui_secretkey-LEAKED"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(leak)) // a body that must NOT reach the error
	}))
	defer srv.Close()
	tgt := NewQui(srv.URL, testAPIKey, srv.Client(), nil, nil)

	_, err := tgt.Announce(context.Background(), sampleRelease())
	if err == nil {
		t.Fatal("Announce err = nil, want a 500 error")
	}
	if strings.Contains(err.Error(), leak) || strings.Contains(err.Error(), testAPIKey) {
		t.Errorf("error leaked secret/body: %v", err)
	}
}

func TestQuiAnnounce_EmptyTorrentBytesIsError(t *testing.T) {
	t.Parallel()
	applyCalls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == quiApplyPath {
			applyCalls++
		}
		if r.URL.Path == quiCheckPath {
			_ = json.NewEncoder(w).Encode(quiCheckResponse{CanCrossSeed: true, Recommendation: "download"})
		}
	}))
	defer srv.Close()

	fetch := func(_ context.Context, _ string) ([]byte, error) { return nil, nil } // empty body
	tgt := NewQui(srv.URL, testAPIKey, srv.Client(), fetch, nil)

	_, err := tgt.Announce(context.Background(), sampleRelease())
	if err == nil {
		t.Fatal("Announce err = nil, want an empty-torrent error")
	}
	if applyCalls != 0 {
		t.Errorf("apply called %d times on empty bytes, want 0 (no garbage POST)", applyCalls)
	}
}

func TestQuiProbe_NonMutatingAndReachable(t *testing.T) {
	t.Parallel()
	applyCalls := 0
	var probedName, probedIndexer string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case quiCheckPath:
			var cr quiCheckRequest
			_ = json.NewDecoder(r.Body).Decode(&cr)
			probedName, probedIndexer = cr.TorrentName, cr.Indexer
			_ = json.NewEncoder(w).Encode(quiCheckResponse{CanCrossSeed: false, Recommendation: "skip"})
		case quiApplyPath:
			applyCalls++
		}
	}))
	defer srv.Close()
	tgt := NewQui(srv.URL, testAPIKey, srv.Client(), nil, nil)

	if err := tgt.Probe(context.Background()); err != nil {
		t.Fatalf("Probe: %v, want nil (reachable)", err)
	}
	if applyCalls != 0 {
		t.Errorf("Probe called apply %d times, want 0 (probe must not inject)", applyCalls)
	}
	if probedName == "" || probedName != probedIndexer {
		t.Errorf("probe check used name=%q indexer=%q, want a shared synthetic token", probedName, probedIndexer)
	}
}

func TestQuiProbe_NotFoundIsReachable(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	tgt := NewQui(srv.URL, testAPIKey, srv.Client(), nil, nil)
	if err := tgt.Probe(context.Background()); err != nil {
		t.Errorf("Probe: %v, want nil (404 = reachable no-match)", err)
	}
}

func TestQuiProbe_ServerErrorIsScrubbed(t *testing.T) {
	t.Parallel()
	const leak = "qui_secretkey-LEAKED"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(leak))
	}))
	defer srv.Close()
	tgt := NewQui(srv.URL, testAPIKey, srv.Client(), nil, nil)

	err := tgt.Probe(context.Background())
	if err == nil {
		t.Fatal("Probe err = nil, want a 500 error")
	}
	if strings.Contains(err.Error(), leak) || strings.Contains(err.Error(), testAPIKey) {
		t.Errorf("Probe error leaked secret/body: %v", err)
	}
}

func TestCrossSeedV6Probe_PingUpIsReachable(t *testing.T) {
	t.Parallel()
	var probedPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		probedPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	tgt := NewCrossSeedV6(srv.URL, "cs_secret", srv.Client())

	if err := tgt.Probe(context.Background()); err != nil {
		t.Fatalf("Probe: %v, want nil (ping up)", err)
	}
	if probedPath != csv6PingPath {
		t.Errorf("probe hit %q, want %q", probedPath, csv6PingPath)
	}
}

func TestCrossSeedV6Probe_PingDownIsScrubbed(t *testing.T) {
	t.Parallel()
	const leak = "cs_secret-LEAKED"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(leak))
	}))
	defer srv.Close()
	tgt := NewCrossSeedV6(srv.URL, "cs_secret", srv.Client())

	err := tgt.Probe(context.Background())
	if err == nil {
		t.Fatal("Probe err = nil, want a 503 error")
	}
	if strings.Contains(err.Error(), leak) || strings.Contains(err.Error(), "cs_secret") {
		t.Errorf("Probe error leaked secret/body: %v", err)
	}
}

// --- cross-seed v6 (one-step) ---

func TestCrossSeedV6Announce_PostsLinkAndKey(t *testing.T) {
	t.Parallel()
	var body csv6Request
	var gotKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get("X-API-Key")
		if r.URL.Path == csv6AnnouncePath {
			_ = json.NewDecoder(r.Body).Decode(&body)
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	tgt := NewCrossSeedV6(srv.URL, "cs_secret", srv.Client())

	rel := sampleRelease()
	res, err := tgt.Announce(context.Background(), rel)
	if err != nil {
		t.Fatalf("Announce: %v", err)
	}
	if !res.Matched {
		t.Error("Matched = false, want true on 200")
	}
	if body.Name != rel.Name || body.GUID != rel.GUID || body.Tracker != rel.Tracker {
		t.Errorf("announce body = %+v, want name/guid/tracker from the release", body)
	}
	if body.Link != rel.DownloadURL {
		t.Errorf("link = %q, want the /dl proxy URL (cross-seed fetches it itself)", body.Link)
	}
	if gotKey != "cs_secret" {
		t.Errorf("X-API-Key = %q, want cs_secret", gotKey)
	}
}

func TestCrossSeedV6Announce_NoContentIsNoMatch(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	tgt := NewCrossSeedV6(srv.URL, "cs_secret", srv.Client())

	res, err := tgt.Announce(context.Background(), sampleRelease())
	if err != nil {
		t.Fatalf("Announce: %v, want nil (204 = no match)", err)
	}
	if res.Matched {
		t.Error("Matched = true, want false on 204")
	}
}

func TestCrossSeedV6Announce_ErrorIsScrubbed(t *testing.T) {
	t.Parallel()
	const leak = "cs_secret-LEAKED"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(leak))
	}))
	defer srv.Close()
	tgt := NewCrossSeedV6(srv.URL, "cs_secret", srv.Client())

	_, err := tgt.Announce(context.Background(), sampleRelease())
	if err == nil {
		t.Fatal("Announce err = nil, want a 401 error")
	}
	if strings.Contains(err.Error(), leak) || strings.Contains(err.Error(), "cs_secret") {
		t.Errorf("error leaked secret/body: %v", err)
	}
}

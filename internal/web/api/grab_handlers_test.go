package api_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/autobrr/harbrr/internal/domain"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/indexer/registry"
	"github.com/autobrr/harbrr/internal/secrets"
	"github.com/autobrr/harbrr/internal/web/api"
	"github.com/autobrr/harbrr/internal/web/torznabhttp"
)

// The bytes each stub tracker serves for a grab. The torrent must open with 'd' —
// ResolveGrab refuses to hand a non-bencoded body to a client as a .torrent.
const (
	grabTorrentBody = "d8:announce20:http://tracker/anne"
	grabNZBBody     = `<?xml version="1.0"?><nzb><file subject="Example"/></nzb>`
)

// grabUpstreamPasskey is the synthetic secret the stub tracker requires on the
// pre-resolution download link. The whole point of the sealed-link path is that harbrr
// uses it server-side and it never reaches the download client or a response body.
const grabUpstreamPasskey = "PASSKEY" + "0123456789" + "ABCDEF"

// clientAdd records what a stubbed download client was actually asked to add. Written
// by the stub's handler and read after the API round trip completes, so the two are
// ordered by the request/response the assertion waited on.
type clientAdd struct {
	url      string // qBittorrent urls= form field
	body     string // uploaded bytes (qBittorrent multipart torrent / SABnzbd addfile)
	filename string // uploaded file name
	mode     string // SABnzbd mode query param
}

// newQbitStub answers qBittorrent's login + torrents/add, recording the add.
func newQbitStub(t *testing.T, got *clientAdd) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v2/auth/login", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "Ok.")
	})
	mux.HandleFunc("/api/v2/torrents/add", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
			f, hdr, err := r.FormFile("torrents")
			if err != nil {
				t.Errorf("torrents/add: read the torrent part: %v", err)
				return
			}
			defer f.Close()
			b, _ := io.ReadAll(f)
			got.body, got.filename = string(b), hdr.Filename
		} else {
			if err := r.ParseForm(); err != nil {
				t.Errorf("torrents/add: parse form: %v", err)
				return
			}
			got.url = r.Form.Get("urls")
		}
		_, _ = io.WriteString(w, "Ok.")
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// newSabStub answers SABnzbd's single /api endpoint, recording an addfile upload.
func newSabStub(t *testing.T, got *clientAdd) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.mode = r.URL.Query().Get("mode")
		if got.mode == "addfile" {
			f, hdr, err := r.FormFile("name")
			if err != nil {
				t.Errorf("addfile: read the nzb part: %v", err)
				return
			}
			defer f.Close()
			b, _ := io.ReadAll(f)
			got.body, got.filename = string(b), hdr.Filename
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"nzo_ids":["SABnzbd_nzo_abc"]}`)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// newGrabTrackerStub serves the passkey-gated .torrent a sealed link resolves to.
func newGrabTrackerStub(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("passkey") != grabUpstreamPasskey {
			http.Error(w, "missing passkey", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/x-bittorrent")
		_, _ = io.WriteString(w, grabTorrentBody)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// newGrabNewsStub serves the apikey-gated .nzb a sealed usenet link resolves to.
func newGrabNewsStub(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("r") != grabUpstreamPasskey {
			http.Error(w, "missing apikey", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/x-nzb")
		_, _ = io.WriteString(w, grabNZBBody)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// grabEnv wires the whole offline chain: two configured indexers (a torrent
// definition and the native Newznab family) pointed at stub trackers over a real HTTP
// doer, plus the router. It returns the env so a test can mint sealed links with the
// same keyring the router holds.
func grabEnv(t *testing.T, trackerURL, newsURL string) (*env, string, *http.Client) {
	t.Helper()
	e := newEnvWithCache(t, api.Config{
		AuthDisabled: true,
		IPAllowlist:  []string{"127.0.0.0/8", "::1/128"},
	}, nil, registry.WithDoerFactory(func(registry.ClientParams) (search.Doer, error) {
		return http.DefaultClient, nil
	}))
	add := func(slug, defID, baseURL string, settings map[string]string) {
		if _, err := e.registry.Add(context.Background(), registry.AddParams{
			Slug: slug, DefinitionID: defID, BaseURL: baseURL, Settings: settings,
		}); err != nil {
			t.Fatalf("Add(%s): %v", slug, err)
		}
	}
	add("trk", "testtracker", trackerURL, map[string]string{"apikey": "k"})
	add("news", "newznab", newsURL, map[string]string{"apikey": grabUpstreamPasskey, "apiPath": "/api"})
	base, c := serve(t, e)
	return e, base, c
}

// sealedLink builds the management download link a JSON search response would have
// served for original: /api/indexers/{slug}/download/{token}, with the token minted
// under the router's own keyring. The origin is deliberately NOT the request host —
// the handler must match on the path, since a configured external origin differs.
func sealedLink(t *testing.T, kr *secrets.Keyring, slug, original string) string {
	t.Helper()
	sealed, err := torznabhttp.SealedDLURL(kr, slug, "http://harbrr.invalid/dl", "", original)
	if err != nil {
		t.Fatalf("seal %s: %v", slug, err)
	}
	u, err := url.Parse(sealed)
	if err != nil {
		t.Fatalf("parse sealed url: %v", err)
	}
	return "http://harbrr.invalid/api/indexers/" + slug + "/download/" + u.Query().Get("token")
}

// addDownloadClient creates a client through the API and returns its id.
func addDownloadClient(t *testing.T, base string, c *http.Client, name, kind, host string) int64 {
	t.Helper()
	resp, body := do(t, c, http.MethodPost, base+"/api/download-clients", map[string]any{
		"name": name, "kind": kind, "host": host, "username": "admin", "secret": "s3cret",
	}, nil)
	mustStatus(t, resp, body, http.StatusCreated)
	var created struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(body, &created); err != nil {
		t.Fatalf("decode created client: %v (%s)", err, body)
	}
	return created.ID
}

func postGrab(t *testing.T, base string, c *http.Client, id int64, body map[string]string) (*http.Response, []byte) {
	t.Helper()
	return do(t, c, http.MethodPost, base+"/api/download-clients/"+strconv.FormatInt(id, 10)+"/grab", body, nil)
}

// TestGrabSealedTorrentReachesQBittorrent is the issue's headline case: the operator
// picks a torrent result, harbrr resolves the sealed link with the tracker's passkey
// server-side, and qBittorrent receives the .torrent BYTES — never the passkey.
func TestGrabSealedTorrentReachesQBittorrent(t *testing.T) {
	t.Parallel()
	tracker := newGrabTrackerStub(t)
	var got clientAdd
	qbit := newQbitStub(t, &got)
	e, base, c := grabEnv(t, tracker.URL, newGrabNewsStub(t).URL)
	id := addDownloadClient(t, base, c, "seedbox", domain.DownloadClientKindQBittorrent, qbit.URL)

	link := sealedLink(t, e.keyring, "trk", tracker.URL+"/dl?id=1&passkey="+grabUpstreamPasskey)
	resp, body := postGrab(t, base, c, id, map[string]string{
		"indexer": "trk", "link": link, "name": "Big Buck Bunny 1080p",
	})
	mustStatus(t, resp, body, http.StatusNoContent)

	if got.body != grabTorrentBody {
		t.Errorf("qBittorrent received %q, want the resolved .torrent bytes", got.body)
	}
	if got.url != "" {
		t.Errorf("qBittorrent was handed a URL (%q); the sealed link must never leave harbrr", got.url)
	}
}

// TestGrabSealedNZBReachesSabnzbd is the usenet half: Newznab seals its apikey-bearing
// links, so SABnzbd must receive the uploaded .nzb, not a URL it could never fetch.
func TestGrabSealedNZBReachesSabnzbd(t *testing.T) {
	t.Parallel()
	news := newGrabNewsStub(t)
	var got clientAdd
	sab := newSabStub(t, &got)
	e, base, c := grabEnv(t, newGrabTrackerStub(t).URL, news.URL)
	id := addDownloadClient(t, base, c, "sab", domain.DownloadClientKindSabnzbd, sab.URL)

	link := sealedLink(t, e.keyring, "news", news.URL+"/getnzb/abc.nzb?r="+grabUpstreamPasskey)
	resp, body := postGrab(t, base, c, id, map[string]string{
		"indexer": "news", "link": link, "name": "Example.Movie.2023.1080p",
	})
	mustStatus(t, resp, body, http.StatusNoContent)

	if got.mode != "addfile" {
		t.Errorf("SABnzbd mode = %q, want addfile (a sealed link is not fetchable by the client)", got.mode)
	}
	if got.body != grabNZBBody {
		t.Errorf("SABnzbd received %q, want the resolved .nzb bytes", got.body)
	}
	if got.filename != "Example.Movie.2023.1080p.nzb" {
		t.Errorf("upload filename = %q, want the release title", got.filename)
	}
}

// TestGrabMagnetPassesThrough: a magnet carries no harbrr secret and needs no resolve,
// so it is handed to the client verbatim.
func TestGrabMagnetPassesThrough(t *testing.T) {
	t.Parallel()
	var got clientAdd
	qbit := newQbitStub(t, &got)
	_, base, c := grabEnv(t, newGrabTrackerStub(t).URL, newGrabNewsStub(t).URL)
	id := addDownloadClient(t, base, c, "seedbox", domain.DownloadClientKindQBittorrent, qbit.URL)

	const magnet = "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567&dn=test"
	resp, body := postGrab(t, base, c, id, map[string]string{"indexer": "trk", "link": magnet})
	mustStatus(t, resp, body, http.StatusNoContent)

	if got.url != magnet {
		t.Errorf("qBittorrent urls = %q, want the magnet verbatim", got.url)
	}
}

// TestGrabProtocolMismatch: picking an nzb-only client for a torrent is the operator's
// mistake to fix, so it is a 400 they can read — the UI does not filter the list.
func TestGrabProtocolMismatch(t *testing.T) {
	t.Parallel()
	var got clientAdd
	sab := newSabStub(t, &got)
	_, base, c := grabEnv(t, newGrabTrackerStub(t).URL, newGrabNewsStub(t).URL)
	id := addDownloadClient(t, base, c, "sab", domain.DownloadClientKindSabnzbd, sab.URL)

	resp, body := postGrab(t, base, c, id, map[string]string{
		"indexer": "trk", "link": "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567",
	})
	mustStatus(t, resp, body, http.StatusBadRequest)
	if !strings.Contains(string(body), "does not support payload protocol") {
		t.Errorf("body = %s, want the protocol mismatch explained", body)
	}
}

// TestGrabDisabledClient: a client disabled between the page load and the click is
// refused, not silently used.
func TestGrabDisabledClient(t *testing.T) {
	t.Parallel()
	var got clientAdd
	qbit := newQbitStub(t, &got)
	_, base, c := grabEnv(t, newGrabTrackerStub(t).URL, newGrabNewsStub(t).URL)
	id := addDownloadClient(t, base, c, "seedbox", domain.DownloadClientKindQBittorrent, qbit.URL)
	resp, body := do(t, c, http.MethodPost, base+"/api/download-clients/"+strconv.FormatInt(id, 10)+"/disable", nil, nil)
	mustStatus(t, resp, body, http.StatusNoContent)

	resp, body = postGrab(t, base, c, id, map[string]string{"indexer": "trk", "link": "magnet:?xt=urn:btih:x"})
	mustStatus(t, resp, body, http.StatusBadRequest)
	if got.url != "" {
		t.Errorf("a disabled client was still handed %q", got.url)
	}
}

// TestGrabRejectsBadRequests covers the request-shaped failures in one table: an
// unknown client, an unknown indexer, a forged token, and a missing field.
func TestGrabRejectsBadRequests(t *testing.T) {
	t.Parallel()
	var got clientAdd
	qbit := newQbitStub(t, &got)
	_, base, c := grabEnv(t, newGrabTrackerStub(t).URL, newGrabNewsStub(t).URL)
	id := addDownloadClient(t, base, c, "seedbox", domain.DownloadClientKindQBittorrent, qbit.URL)

	tests := []struct {
		name string
		id   int64
		body map[string]string
		want int
	}{
		{"unknown client", 9999, map[string]string{"indexer": "trk", "link": "magnet:?xt=urn:btih:x"}, http.StatusNotFound},
		{"unknown indexer", id, map[string]string{"indexer": "nope", "link": "magnet:?xt=urn:btih:x"}, http.StatusNotFound},
		{"missing link", id, map[string]string{"indexer": "trk"}, http.StatusBadRequest},
		{"missing indexer", id, map[string]string{"link": "magnet:?xt=urn:btih:x"}, http.StatusBadRequest},
		{
			"forged token", id,
			map[string]string{"indexer": "trk", "link": "http://harbrr.invalid/api/indexers/trk/download/not-a-real-token"},
			http.StatusBadRequest,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			resp, body := postGrab(t, base, c, tt.id, tt.body)
			mustStatus(t, resp, body, tt.want)
		})
	}
}

// TestGrabNeverEchoesTheUpstreamSecret: whatever goes wrong, the pre-resolution link's
// passkey must not come back in the response — the token is opaque and the error text
// generic.
func TestGrabNeverEchoesTheUpstreamSecret(t *testing.T) {
	t.Parallel()
	tracker := newGrabTrackerStub(t)
	var got clientAdd
	qbit := newQbitStub(t, &got)
	e, base, c := grabEnv(t, tracker.URL, newGrabNewsStub(t).URL)
	id := addDownloadClient(t, base, c, "seedbox", domain.DownloadClientKindQBittorrent, qbit.URL)

	// A token minted for the OTHER indexer, replayed on trk's download path: the AAD
	// check fails, so the resolve errors instead of grabbing.
	replayed := sealedLink(t, e.keyring, "news", tracker.URL+"/dl?id=1&passkey="+grabUpstreamPasskey)
	link := strings.Replace(replayed, "/api/indexers/news/", "/api/indexers/trk/", 1)
	resp, body := postGrab(t, base, c, id, map[string]string{"indexer": "trk", "link": link})
	mustStatus(t, resp, body, http.StatusBadRequest)
	if strings.Contains(string(body), grabUpstreamPasskey) {
		t.Fatalf("response leaks the upstream passkey: %s", body)
	}
}

// TestGrabRequiresAuth: the route lives in the authenticated group.
func TestGrabRequiresAuth(t *testing.T) {
	t.Parallel()
	base, c := serve(t, newEnv(t, api.Config{}))
	resp, body := postGrab(t, base, c, 1, map[string]string{"indexer": "trk", "link": "magnet:?xt=urn:btih:x"})
	mustStatus(t, resp, body, http.StatusUnauthorized)
}

package search

import (
	"context"
	stdhttp "net/http"
	"net/http/cookiejar"
	"net/url"
	"testing"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/login"
)

// TestGrab_NoDownloadBlock_AppliesSearchHeader covers a login-auth tracker routed
// through /dl that has NO download block but authenticates the download with a
// header in search.headers (the DigitalCore shape: X-API-KEY). Grab must not panic
// on the nil download block, and must apply the search header to the fetch.
func TestGrab_NoDownloadBlock_AppliesSearchHeader(t *testing.T) {
	t.Parallel()
	def := &loader.Definition{
		Search: loader.Search{
			Headers: map[string][]string{"X-Api-Key": {"secret-key-123"}}, //nolint:gosec // G101: synthetic test key
		},
	}
	const link = "https://dl.test/api/v1/torrents/download/9"
	doer := &scriptedDoer{t: t, handlers: map[string]scriptResp{
		"GET " + link: {body: torrentBody},
	}}

	res, err := Grab(context.Background(), def, link, nil, doer, downloadTestDeps())
	if err != nil {
		t.Fatalf("Grab: %v", err)
	}
	if string(res.Body) != torrentBody {
		t.Errorf("Body = %q, want the torrent bytes", res.Body)
	}
	if res.ContentType != torrentContentType {
		t.Errorf("ContentType = %q, want %q", res.ContentType, torrentContentType)
	}
	if len(doer.requests) != 1 {
		t.Fatalf("issued %d requests, want 1 (resolve passes through, fetch only)", len(doer.requests))
	}
	if got := doer.requests[0].headers.Get("X-Api-Key"); got != "secret-key-123" {
		t.Errorf("download X-Api-Key = %q, want the search-header value applied to the fetch", got)
	}
}

// TestGrab_NoDownloadBlock_AppliesSessionCookie covers a session-cookie tracker
// (the TorrentLeech shape): no download block, no token in the URL — the download
// authenticates by the login session cookie, which applySession attaches.
func TestGrab_NoDownloadBlock_AppliesSessionCookie(t *testing.T) {
	t.Parallel()
	const link = "https://dl.test/download/241785226/Release.torrent"
	u, err := url.Parse(link)
	if err != nil {
		t.Fatalf("url.Parse: %v", err)
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar.New: %v", err)
	}
	jar.SetCookies(u, []*stdhttp.Cookie{{Name: "tlsession", Value: "logged-in-cookie"}})
	session := &login.Session{Jar: jar}

	doer := &scriptedDoer{t: t, handlers: map[string]scriptResp{
		"GET " + link: {body: torrentBody},
	}}

	res, err := Grab(context.Background(), &loader.Definition{}, link, session, doer, downloadTestDeps())
	if err != nil {
		t.Fatalf("Grab: %v", err)
	}
	if string(res.Body) != torrentBody {
		t.Errorf("Body = %q, want the torrent bytes", res.Body)
	}
	if got := doer.requests[0].headers.Get("Cookie"); got != "tlsession=logged-in-cookie" {
		t.Errorf("download Cookie = %q, want the session cookie attached", got)
	}
}

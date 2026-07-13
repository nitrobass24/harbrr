package search

import (
	"context"
	"io"
	stdhttp "net/http"
	"strings"
	"testing"
	"time"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
)

// scriptResp is a canned response keyed by "METHOD URL".
type scriptResp struct {
	status int
	body   string
}

// recordedReq captures one issued request for sequence/body assertions the parity
// replay transport cannot make (it discards bodies).
type recordedReq struct {
	method  string
	url     string
	body    string
	headers stdhttp.Header
}

// scriptedDoer serves canned responses by "METHOD URL" and records every request,
// so a test can assert the exact request sequence including POST bodies/headers.
type scriptedDoer struct {
	t        *testing.T
	handlers map[string]scriptResp
	requests []recordedReq
}

func (d *scriptedDoer) Do(req *stdhttp.Request) (*stdhttp.Response, error) {
	var body string
	if req.Body != nil {
		b, _ := io.ReadAll(req.Body)
		body = string(b)
	}
	d.requests = append(d.requests, recordedReq{req.Method, req.URL.String(), body, req.Header.Clone()})

	key := req.Method + " " + req.URL.String()
	resp, ok := d.handlers[key]
	if !ok {
		d.t.Fatalf("scriptedDoer: unexpected request %s", key)
	}
	status := resp.status
	if status == 0 {
		status = stdhttp.StatusOK
	}
	return &stdhttp.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader(resp.body)),
		Header:     stdhttp.Header{},
		Request:    req,
	}, nil
}

// RoundTrip lets a real *http.Client (owning a cookie jar, the production shape)
// wrap the scripted fixture.
func (d *scriptedDoer) RoundTrip(req *stdhttp.Request) (*stdhttp.Response, error) {
	return d.Do(req)
}

func (d *scriptedDoer) sequence() []string {
	out := make([]string, len(d.requests))
	for i, r := range d.requests {
		out[i] = r.method + " " + r.url
	}
	return out
}

func downloadTestDeps() Deps {
	return Deps{
		Filters: NewFilterRegistry(),
		BaseURL: "https://dl.test/",
		Clock:   func() time.Time { return time.Date(2024, 6, 1, 12, 0, 0, 0, time.UTC) },
	}
}

func sel(selector, attribute string, filters ...loader.FilterBlock) loader.SelectorField {
	return loader.SelectorField{Selector: selector, Attribute: attribute, Filters: filters}
}

func boolPtr(b bool) *bool { return &b }

// bencoded torrent body: testlinktorrent only inspects the first byte ('d').
const torrentBody = "d8:announce19:https://dl.test/ann4:infod4:name11:Release.txtee"

// TestResolveDownload_BeforeInputsPost reproduces the hdturk shape: a POST before
// pre-request whose body carries .DownloadUri.Query.id, then a download selector,
// then testlinktorrent validation. It asserts the POST body (which the parity
// replay transport cannot) and the full request sequence.
func TestResolveDownload_BeforeInputsPost(t *testing.T) {
	t.Parallel()
	def := &loader.Definition{
		Download: &loader.DownloadBlock{
			Before: &loader.BeforeBlock{
				Path:   "/takethanks.php",
				Method: "post",
				Inputs: loader.NewInputsBlock(loader.InputEntry{
					Key: "torrentid", Value: loader.Scalar{Value: "{{ .DownloadUri.Query.id }}", Set: true},
				}),
			},
			Selectors: []loader.SelectorField{sel(`a[href*="download.php?id="]`, "href")},
		},
	}
	doer := &scriptedDoer{t: t, handlers: map[string]scriptResp{
		"POST https://dl.test/takethanks.php":    {body: "<html>ok</html>"},
		"GET https://dl.test/details.php?id=42":  {body: `<a href="/download.php?id=42">dl</a>`},
		"GET https://dl.test/download.php?id=42": {body: torrentBody},
	}}

	got, err := ResolveDownload(context.Background(), def, "https://dl.test/details.php?id=42", nil, doer, downloadTestDeps(), true)
	if err != nil {
		t.Fatalf("ResolveDownload: %v", err)
	}
	if want := "https://dl.test/download.php?id=42"; got != want {
		t.Errorf("resolved = %q, want %q", got, want)
	}

	wantSeq := []string{
		"POST https://dl.test/takethanks.php",
		"GET https://dl.test/details.php?id=42",
		"GET https://dl.test/download.php?id=42",
	}
	assertSequence(t, doer.sequence(), wantSeq)

	// The before POST body carries the id pulled from .DownloadUri.Query.id, ordered.
	if body := doer.requests[0].body; body != "torrentid=42" {
		t.Errorf("before POST body = %q, want %q", body, "torrentid=42")
	}
	if ct := doer.requests[0].headers.Get("Content-Type"); ct != "application/x-www-form-urlencoded" {
		t.Errorf("before POST Content-Type = %q", ct)
	}
}

// TestResolveDownload_InfoHashMagnet reproduces the magnetdownload shape: a GET
// before pre-request (id pulled from .DownloadUri.AbsolutePath via re_replace)
// whose JSON response yields the info hash + title via :root+regexp, synthesised
// into a magnet. usebeforeresponse means no second fetch; a magnet skips
// testlinktorrent.
func TestResolveDownload_InfoHashMagnet(t *testing.T) {
	t.Parallel()
	def := &loader.Definition{
		Download: &loader.DownloadBlock{
			Before: &loader.BeforeBlock{
				Path: "/api/json_info",
				Inputs: loader.NewInputsBlock(loader.InputEntry{
					Key: "hashes", Value: loader.Scalar{Value: `{{ re_replace .DownloadUri.AbsolutePath "/info/" "" }}`, Set: true},
				}),
			},
			InfoHash: &loader.InfoHashBlock{
				UseBeforeResponse: boolPtr(true),
				Hash:              &loader.SelectorField{Selector: ":root", Filters: []loader.FilterBlock{{Name: "regexp", Args: loader.FilterArgs{`([A-Fa-f0-9]{40})`}}}},
				Title:             &loader.SelectorField{Selector: ":root", Filters: []loader.FilterBlock{{Name: "regexp", Args: loader.FilterArgs{`"name": "(.+?)"`}}}},
			},
		},
	}
	beforeJSON := `{"name": "Infohash Release", "hash": "ABCDEF0123456789ABCDEF0123456789ABCDEF01"}`
	doer := &scriptedDoer{t: t, handlers: map[string]scriptResp{
		"GET https://dl.test/api/json_info?hashes=123": {body: beforeJSON},
	}}

	got, err := ResolveDownload(context.Background(), def, "https://dl.test/info/123", nil, doer, downloadTestDeps(), true)
	if err != nil {
		t.Fatalf("ResolveDownload: %v", err)
	}
	const wantPrefix = "magnet:?xt=urn:btih:ABCDEF0123456789ABCDEF0123456789ABCDEF01&dn=Infohash+Release&tr=udp%3A%2F%2Ftracker.opentrackr.org%3A1337%2Fannounce"
	if !strings.HasPrefix(got, wantPrefix) {
		t.Errorf("magnet = %q,\nwant prefix %q", got, wantPrefix)
	}
	if !strings.HasSuffix(got, "&tr=udp%3A%2F%2Ftracker.corpscorp.online%3A80%2Fannounce") {
		t.Errorf("magnet = %q, missing final tracker", got)
	}
	assertSequence(t, doer.sequence(), []string{"GET https://dl.test/api/json_info?hashes=123"})
}

// TestResolveDownload_PathSelector reproduces the ildragonero shape: before.pathselector
// GETs the link, selects the before path from it (replacing before.path), then the
// before request runs and a download selector yields the torrent.
func TestResolveDownload_PathSelector(t *testing.T) {
	t.Parallel()
	def := &loader.Definition{
		Download: &loader.DownloadBlock{
			Before:    &loader.BeforeBlock{PathSelector: &loader.SelectorField{Selector: "a.prepare", Attribute: "href"}},
			Selectors: []loader.SelectorField{sel("a.dl", "href")},
		},
	}
	doer := &scriptedDoer{t: t, handlers: map[string]scriptResp{
		"GET https://dl.test/details/7":     {body: `<a class="prepare" href="/prepare/7">p</a><a class="dl" href="/get/7.torrent">d</a>`},
		"GET https://dl.test/prepare/7":     {body: "<html>prepared</html>"},
		"GET https://dl.test/get/7.torrent": {body: torrentBody},
	}}

	got, err := ResolveDownload(context.Background(), def, "https://dl.test/details/7", nil, doer, downloadTestDeps(), true)
	if err != nil {
		t.Fatalf("ResolveDownload: %v", err)
	}
	if want := "https://dl.test/get/7.torrent"; got != want {
		t.Errorf("resolved = %q, want %q", got, want)
	}
	assertSequence(t, doer.sequence(), []string{
		"GET https://dl.test/details/7",     // pathselector preliminary GET
		"GET https://dl.test/prepare/7",     // before request to the selected path
		"GET https://dl.test/details/7",     // selector reads the link page
		"GET https://dl.test/get/7.torrent", // testlinktorrent validation
	})
}

// TestResolveDownload_TestLinkRetry confirms testlinktorrent (default true) rejects
// a selector whose resolved link is not a bencoded torrent and advances to the next
// selector.
func TestResolveDownload_TestLinkRetry(t *testing.T) {
	t.Parallel()
	def := &loader.Definition{
		Download: &loader.DownloadBlock{
			Selectors: []loader.SelectorField{sel("a.first", "href"), sel("a.second", "href")},
		},
	}
	linkPage := `<a class="first" href="/bad">x</a><a class="second" href="/good.torrent">y</a>`
	doer := &scriptedDoer{t: t, handlers: map[string]scriptResp{
		"GET https://dl.test/details/1":    {body: linkPage},
		"GET https://dl.test/bad":          {body: "<html>not a torrent</html>"},
		"GET https://dl.test/good.torrent": {body: torrentBody},
	}}

	got, err := ResolveDownload(context.Background(), def, "https://dl.test/details/1", nil, doer, downloadTestDeps(), true)
	if err != nil {
		t.Fatalf("ResolveDownload: %v", err)
	}
	if want := "https://dl.test/good.torrent"; got != want {
		t.Errorf("resolved = %q, want %q", got, want)
	}
	assertSequence(t, doer.sequence(), []string{
		"GET https://dl.test/details/1", // selector 1 reads link
		"GET https://dl.test/bad",       // selector 1 fails validation
		"GET https://dl.test/details/1", // selector 2 reads link (fresh GET)
		"GET https://dl.test/good.torrent",
	})
}

// TestResolveDownload_TestLinkFetchError confirms a selector whose resolved link
// fails to fetch (non-2xx) is treated as invalid and the loop advances to the next
// selector — Jackett's per-selector continue — rather than aborting the resolve.
func TestResolveDownload_TestLinkFetchError(t *testing.T) {
	t.Parallel()
	def := &loader.Definition{
		Download: &loader.DownloadBlock{
			Selectors: []loader.SelectorField{sel("a.first", "href"), sel("a.second", "href")},
		},
	}
	linkPage := `<a class="first" href="/gone">x</a><a class="second" href="/good.torrent">y</a>`
	doer := &scriptedDoer{t: t, handlers: map[string]scriptResp{
		"GET https://dl.test/details/1":    {body: linkPage},
		"GET https://dl.test/gone":         {status: 404, body: "not found"},
		"GET https://dl.test/good.torrent": {body: torrentBody},
	}}

	got, err := ResolveDownload(context.Background(), def, "https://dl.test/details/1", nil, doer, downloadTestDeps(), true)
	if err != nil {
		t.Fatalf("ResolveDownload: %v", err)
	}
	if want := "https://dl.test/good.torrent"; got != want {
		t.Errorf("resolved = %q, want %q", got, want)
	}
}

// TestResolveDownload_TestLinkDisabled confirms testlinktorrent: false skips the
// validation fetch entirely (no extra request), returning the first resolved link.
func TestResolveDownload_TestLinkDisabled(t *testing.T) {
	t.Parallel()
	def := &loader.Definition{
		TestLinkTorrent: boolPtr(false),
		Download:        &loader.DownloadBlock{Selectors: []loader.SelectorField{sel("a.dl", "href")}},
	}
	doer := &scriptedDoer{t: t, handlers: map[string]scriptResp{
		"GET https://dl.test/details/1": {body: `<a class="dl" href="/get/1.torrent">d</a>`},
	}}

	got, err := ResolveDownload(context.Background(), def, "https://dl.test/details/1", nil, doer, downloadTestDeps(), true)
	if err != nil {
		t.Fatalf("ResolveDownload: %v", err)
	}
	if want := "https://dl.test/get/1.torrent"; got != want {
		t.Errorf("resolved = %q, want %q", got, want)
	}
	assertSequence(t, doer.sequence(), []string{"GET https://dl.test/details/1"})
}

// TestResolveDownload_FeedTimeSkipsValidation confirms that with validate=false
// (feed-time pre-resolution) the default-true testlinktorrent gate does NOT fetch
// the resolved link — so resolving a served page never fires a torrent fetch per
// release. The selector still resolves; only the validation GET is skipped.
func TestResolveDownload_FeedTimeSkipsValidation(t *testing.T) {
	t.Parallel()
	def := &loader.Definition{
		Download: &loader.DownloadBlock{Selectors: []loader.SelectorField{sel("a.dl", "href")}},
	}
	doer := &scriptedDoer{t: t, handlers: map[string]scriptResp{
		"GET https://dl.test/details/1": {body: `<a class="dl" href="/get/1.torrent">d</a>`},
	}}

	got, err := ResolveDownload(context.Background(), def, "https://dl.test/details/1", nil, doer, downloadTestDeps(), false)
	if err != nil {
		t.Fatalf("ResolveDownload: %v", err)
	}
	if want := "https://dl.test/get/1.torrent"; got != want {
		t.Errorf("resolved = %q, want %q", got, want)
	}
	assertSequence(t, doer.sequence(), []string{"GET https://dl.test/details/1"})
}

// TestResolveDownload_Headers confirms download.headers are rendered and attached to
// the resolution GETs.
func TestResolveDownload_Headers(t *testing.T) {
	t.Parallel()
	def := &loader.Definition{
		TestLinkTorrent: boolPtr(false),
		Download: &loader.DownloadBlock{
			Headers:   map[string][]string{"X-Dl": {"token-{{ .DownloadUri.Query.id }}"}},
			Selectors: []loader.SelectorField{sel("a.dl", "href")},
		},
	}
	doer := &scriptedDoer{t: t, handlers: map[string]scriptResp{
		"GET https://dl.test/details.php?id=9": {body: `<a class="dl" href="/get/9.torrent">d</a>`},
	}}

	_, err := ResolveDownload(context.Background(), def, "https://dl.test/details.php?id=9", nil, doer, downloadTestDeps(), true)
	if err != nil {
		t.Fatalf("ResolveDownload: %v", err)
	}
	if h := doer.requests[0].headers.Get("X-Dl"); h != "token-9" {
		t.Errorf("X-Dl header = %q, want %q", h, "token-9")
	}
}

// TestResolveDownload_NoBlockPassthrough confirms a definition with no download
// block returns the link unchanged and issues no request.
func TestResolveDownload_NoBlockPassthrough(t *testing.T) {
	t.Parallel()
	doer := &scriptedDoer{t: t, handlers: map[string]scriptResp{}}
	got, err := ResolveDownload(context.Background(), &loader.Definition{}, "https://dl.test/x.torrent", nil, doer, downloadTestDeps(), true)
	if err != nil {
		t.Fatalf("ResolveDownload: %v", err)
	}
	if want := "https://dl.test/x.torrent"; got != want {
		t.Errorf("resolved = %q, want %q", got, want)
	}
	if len(doer.requests) != 0 {
		t.Errorf("issued %d requests, want 0", len(doer.requests))
	}
}

func assertSequence(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("request sequence = %v,\nwant %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("request[%d] = %q, want %q (full: %v)", i, got[i], want[i], got)
		}
	}
}

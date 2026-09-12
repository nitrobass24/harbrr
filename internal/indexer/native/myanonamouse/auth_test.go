package myanonamouse

import (
	"context"
	"errors"
	"io"
	stdhttp "net/http"
	"os"
	"strings"
	"testing"
	"time"

	apphttp "github.com/autobrr/harbrr/internal/http"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/login"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
)

// distinctive synthetic mam_id so a redaction check can prove it never escapes into a
// URL/query or an error string (it may appear only in the Cookie header).
const mamSecret = "MAMID-SECRET-9f8e"

func fixedClock() time.Time { return time.Unix(1_700_000_000, 0).UTC() }

// recordedReq captures one issued request for assertions a black-box transport cannot
// make (the request path/query, the Cookie + Accept + User-Agent headers).
type recordedReq struct {
	method, url, cookie, accept, userAgent string
}

// scriptDoer records every request and serves a scripted response. setCookie, when
// non-empty, is attached as a Set-Cookie header on every response so a test can drive
// the mam_id rotation path.
type scriptDoer struct {
	handler   func(req *stdhttp.Request) *stdhttp.Response
	setCookie string
	reqs      []recordedReq
}

func (s *scriptDoer) Do(req *stdhttp.Request) (*stdhttp.Response, error) {
	if req.Body != nil {
		_, _ = io.Copy(io.Discard, req.Body)
	}
	s.reqs = append(s.reqs, recordedReq{
		method: req.Method,
		url:    req.URL.String(),
		cookie: req.Header.Get("Cookie"),
		accept: req.Header.Get("Accept"),
		// Go's transport supplies its own default UA at send time, so an empty value
		// here means the driver set none — exactly what the download must not do.
		userAgent: req.Header.Get("User-Agent"),
	})
	r := s.handler(req)
	if s.setCookie != "" {
		r.Header.Set("Set-Cookie", s.setCookie)
	}
	return r, nil
}

func resp(status int, body string) *stdhttp.Response {
	return &stdhttp.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: stdhttp.Header{}}
}

func newDriver(doer *scriptDoer) *driver {
	return &driver{
		Family:       "myanonamouse",
		Def:          &loader.Definition{ID: "myanonamouse"},
		Cfg:          map[string]string{"mam_id": mamSecret},
		Doer:         doer,
		BaseURL:      "https://mam.test/",
		Clock:        fixedClock,
		currentMamID: mamSecret,
	}
}

// TestGetSendsCookie proves every authenticated GET carries the mam_id Cookie and the
// requested Accept header, and that the mam_id never appears in the recorded URL.
func TestGetSendsCookie(t *testing.T) {
	t.Parallel()
	doer := &scriptDoer{handler: func(_ *stdhttp.Request) *stdhttp.Response {
		return resp(stdhttp.StatusOK, `{"error":"","data":[]}`)
	}}
	d := newDriver(doer)
	req, err := d.newRequest(context.Background(), d.BaseURL+searchPath+"?tor[text]=x", "application/json")
	if err != nil {
		t.Fatalf("newRequest: %v", err)
	}
	if _, err := d.do(context.Background(), req); err != nil {
		t.Fatalf("do: %v", err)
	}
	if len(doer.reqs) != 1 {
		t.Fatalf("requests = %d, want 1", len(doer.reqs))
	}
	got := doer.reqs[0]
	if got.cookie != "mam_id="+mamSecret {
		t.Errorf("Cookie = %q, want mam_id=%s", got.cookie, mamSecret)
	}
	if got.accept != "application/json" {
		t.Errorf("Accept = %q, want application/json", got.accept)
	}
	assertNoSecret(t, got.url)
}

// TestMamIDRotation proves a Set-Cookie mam_id on a response is captured so the NEXT
// request carries the rotated value (in-process, in-memory only).
func TestMamIDRotation(t *testing.T) {
	t.Parallel()
	doer := &scriptDoer{
		setCookie: "mam_id=ROTATED; Path=/; HttpOnly",
		handler: func(_ *stdhttp.Request) *stdhttp.Response {
			return resp(stdhttp.StatusOK, `{"error":"","data":[]}`)
		},
	}
	d := newDriver(doer)

	// First request uses the seeded mam_id and receives the rotated Set-Cookie.
	if _, err := d.Search(context.Background(), search.Query{Keywords: "x"}); err != nil {
		t.Fatalf("first Search: %v", err)
	}
	if d.mamID() != "ROTATED" {
		t.Fatalf("mamID after rotation = %q, want ROTATED", d.mamID())
	}
	// Second request must now carry the rotated cookie.
	if _, err := d.Search(context.Background(), search.Query{Keywords: "y"}); err != nil {
		t.Fatalf("second Search: %v", err)
	}
	if len(doer.reqs) != 2 {
		t.Fatalf("requests = %d, want 2", len(doer.reqs))
	}
	if doer.reqs[0].cookie != "mam_id="+mamSecret {
		t.Errorf("first Cookie = %q, want the seeded mam_id", doer.reqs[0].cookie)
	}
	if doer.reqs[1].cookie != "mam_id=ROTATED" {
		t.Errorf("second Cookie = %q, want mam_id=ROTATED", doer.reqs[1].cookie)
	}
}

// TestMamIDRotationPersists proves a rotated mam_id is written back through the persist
// callback exactly once (so the session survives a restart), and not at all when the
// value is unchanged. The persist is synchronous (in-line with the request), so the
// call count is deterministic by the time Search returns — no timing or channels.
func TestMamIDRotationPersists(t *testing.T) {
	t.Parallel()
	type call struct{ name, value string }
	run := func(setCookie string) []call {
		var calls []call
		d := newDriver(&scriptDoer{
			setCookie: setCookie,
			handler:   func(_ *stdhttp.Request) *stdhttp.Response { return resp(stdhttp.StatusOK, `{"error":"","data":[]}`) },
		})
		// Synchronous persist runs on the request goroutine, so no lock is needed.
		d.persist = func(_ context.Context, name, value string) error {
			calls = append(calls, call{name, value})
			return nil
		}
		if _, err := d.Search(context.Background(), search.Query{Keywords: "x"}); err != nil {
			t.Fatalf("Search: %v", err)
		}
		return calls
	}

	// A rotation persists the new value exactly once.
	if got := run("mam_id=ROTATED; Path=/; HttpOnly"); len(got) != 1 || got[0] != (call{mamIDCookie, "ROTATED"}) {
		t.Fatalf("persist calls = %+v, want exactly one {mam_id ROTATED}", got)
	}
	// An unchanged mam_id (server echoes the seeded value) persists nothing.
	if got := run("mam_id=" + mamSecret + "; Path=/"); len(got) != 0 {
		t.Fatalf("persist calls on unchanged mam_id = %+v, want none", got)
	}
}

// TestTestAction proves Test() succeeds on a 200 and maps a 403 to login.ErrLoginFailed
// (mam_id expired/invalid) without leaking the secret.
func TestTestAction(t *testing.T) {
	t.Parallel()
	ok := newDriver(&scriptDoer{handler: func(_ *stdhttp.Request) *stdhttp.Response {
		return resp(stdhttp.StatusOK, `{"error":"","data":[]}`)
	}})
	if err := ok.Test(context.Background()); err != nil {
		t.Errorf("Test on good mam_id = %v, want nil", err)
	}

	bad := newDriver(&scriptDoer{handler: func(_ *stdhttp.Request) *stdhttp.Response {
		return resp(stdhttp.StatusForbidden, "Forbidden")
	}})
	err := bad.Test(context.Background())
	if !errors.Is(err, login.ErrLoginFailed) {
		t.Errorf("Test on 403 = %v, want login.ErrLoginFailed", err)
	}
	assertNoSecret(t, err.Error())
	assertNoSecret(t, apphttp.RedactError(err))
}

// assertNoSecret fails if s contains the synthetic mam_id (it may live only in the
// Cookie header, never in a URL, query, or error string).
func assertNoSecret(t *testing.T, s string) {
	t.Helper()
	if strings.Contains(s, mamSecret) {
		t.Errorf("string leaks the mam_id (%q): %q", mamSecret, s)
	}
}

// TestHasUserVIP covers the jsonLoad.php user-class lookup that gates fl_vip
// freeleech: the oracle's VIP classes map to true, anything else to false, and a
// failed lookup degrades to non-VIP rather than failing the search.
func TestHasUserVIP(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		status int
		body   string
		want   bool
	}{
		{"vip class", stdhttp.StatusOK, `{"classname":"VIP"}`, true},
		{"elite vip class", stdhttp.StatusOK, `{"classname":"Elite VIP"}`, true},
		{"case-insensitive", stdhttp.StatusOK, `{"classname":"elite vip"}`, true},
		{"padded class", stdhttp.StatusOK, `{"classname":"  VIP  "}`, true},
		{"non-vip class", stdhttp.StatusOK, `{"classname":"Power User"}`, false},
		{"missing class", stdhttp.StatusOK, `{}`, false},
		{"malformed body", stdhttp.StatusOK, `not json`, false},
		{"auth failure", stdhttp.StatusForbidden, ``, false},
		{"server error", stdhttp.StatusInternalServerError, ``, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			doer := &scriptDoer{handler: func(_ *stdhttp.Request) *stdhttp.Response {
				return resp(tc.status, tc.body)
			}}
			d := newDriver(doer)
			if got := d.hasUserVIP(context.Background()); got != tc.want {
				t.Errorf("hasUserVIP = %v, want %v", got, tc.want)
			}
			if len(doer.reqs) != 1 {
				t.Fatalf("requests = %d, want 1", len(doer.reqs))
			}
			if doer.reqs[0].url != "https://mam.test/jsonLoad.php" {
				t.Errorf("url = %q", doer.reqs[0].url)
			}
			if doer.reqs[0].cookie != "mam_id="+mamSecret {
				t.Errorf("lookup did not ride the session cookie: %q", doer.reqs[0].cookie)
			}
		})
	}
}

// TestHasUserVIPFixtures runs the lookup against the saved jsonLoad.php bodies.
func TestHasUserVIPFixtures(t *testing.T) {
	t.Parallel()
	cases := []struct {
		file string
		want bool
	}{
		{"testdata/user_data_vip.json", true},
		{"testdata/user_data_nonvip.json", false},
	}
	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			t.Parallel()
			body, err := os.ReadFile(tc.file)
			if err != nil {
				t.Fatalf("read fixture: %v", err)
			}
			d := newDriver(&scriptDoer{handler: func(_ *stdhttp.Request) *stdhttp.Response {
				return resp(stdhttp.StatusOK, string(body))
			}})
			if got := d.hasUserVIP(context.Background()); got != tc.want {
				t.Errorf("hasUserVIP = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestHasUserVIPCaches proves a successful lookup is memoized (one request for
// repeated calls) while a FAILED lookup is not cached, so the next search retries.
func TestHasUserVIPCaches(t *testing.T) {
	t.Parallel()
	ok := &scriptDoer{handler: func(_ *stdhttp.Request) *stdhttp.Response {
		return resp(stdhttp.StatusOK, `{"classname":"VIP"}`)
	}}
	d := newDriver(ok)
	for range 3 {
		if !d.hasUserVIP(context.Background()) {
			t.Fatal("hasUserVIP = false, want true")
		}
	}
	if len(ok.reqs) != 1 {
		t.Errorf("requests = %d, want 1 (cached)", len(ok.reqs))
	}

	failing := &scriptDoer{handler: func(_ *stdhttp.Request) *stdhttp.Response {
		return resp(stdhttp.StatusInternalServerError, ``)
	}}
	f := newDriver(failing)
	for range 2 {
		if f.hasUserVIP(context.Background()) {
			t.Fatal("a failed lookup must read as non-VIP")
		}
	}
	if len(failing.reqs) != 2 {
		t.Errorf("requests = %d, want 2 (a failure is not cached)", len(failing.reqs))
	}
}

// TestSearchGatesFlVipOnUserClass is the end-to-end gate: the same fl_vip row is
// freeleech for a VIP account and full-cost for a non-VIP one.
func TestSearchGatesFlVipOnUserClass(t *testing.T) {
	t.Parallel()
	const row = `{"error":"","data":[{"id":9,"title":"Book","category":"13","main_cat":"13",` +
		`"added":"2024-01-15 10:30:00","size":"1.00 MB","free":false,"personal_freeleech":false,"fl_vip":true}]}`
	cases := []struct {
		name      string
		userClass string
		want      float64
	}{
		{"vip account", "VIP", 0},
		{"non-vip account", "Power User", 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			d := goldenDriver(t)
			d.BaseURL = "https://mam.test/"
			d.currentMamID = mamSecret
			d.Doer = &scriptDoer{handler: func(req *stdhttp.Request) *stdhttp.Response {
				if strings.Contains(req.URL.Path, "jsonLoad.php") {
					return resp(stdhttp.StatusOK, `{"classname":"`+tc.userClass+`"}`)
				}
				return resp(stdhttp.StatusOK, row)
			}}
			rels, err := d.Search(context.Background(), search.Query{Keywords: "book"})
			if err != nil {
				t.Fatalf("Search: %v", err)
			}
			if len(rels) != 1 {
				t.Fatalf("releases = %d, want 1", len(rels))
			}
			if rels[0].DownloadVolumeFactor != tc.want {
				t.Errorf("DownloadVolumeFactor = %v, want %v", rels[0].DownloadVolumeFactor, tc.want)
			}
		})
	}
}

// TestSearchMemoizesFailedVIPLookupPerSearch proves a failing user-class lookup is
// asked once per search, not once per fl_vip row: the driver cache does not store a
// failure (so the next search retries), but within one page the closure reuses it.
func TestSearchMemoizesFailedVIPLookupPerSearch(t *testing.T) {
	t.Parallel()
	const rows = `{"error":"","data":[` +
		`{"id":1,"title":"A","category":"13","main_cat":"13","added":"2024-01-15 10:30:00","size":"1.00 MB","free":false,"personal_freeleech":false,"fl_vip":true},` +
		`{"id":2,"title":"B","category":"13","main_cat":"13","added":"2024-01-15 10:30:00","size":"1.00 MB","free":false,"personal_freeleech":false,"fl_vip":true},` +
		`{"id":3,"title":"C","category":"13","main_cat":"13","added":"2024-01-15 10:30:00","size":"1.00 MB","free":false,"personal_freeleech":false,"fl_vip":true}]}`
	d := goldenDriver(t)
	d.BaseURL = "https://mam.test/"
	d.currentMamID = mamSecret
	lookups := 0
	d.Doer = &scriptDoer{handler: func(req *stdhttp.Request) *stdhttp.Response {
		if strings.Contains(req.URL.Path, "jsonLoad.php") {
			lookups++
			return resp(stdhttp.StatusInternalServerError, "")
		}
		return resp(stdhttp.StatusOK, rows)
	}}
	rels, err := d.Search(context.Background(), search.Query{Keywords: "book"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(rels) != 3 {
		t.Fatalf("releases = %d, want 3", len(rels))
	}
	if lookups != 1 {
		t.Fatalf("user-class lookups = %d, want 1 (a failed lookup is memoized for the rest of the search)", lookups)
	}
	for i, r := range rels {
		if r.DownloadVolumeFactor != 1 {
			t.Errorf("release %d DownloadVolumeFactor = %v, want 1 (non-VIP on lookup failure)", i, r.DownloadVolumeFactor)
		}
	}
	// A second search retries: the driver cache never stored the failure.
	if _, err := d.Search(context.Background(), search.Query{Keywords: "book"}); err != nil {
		t.Fatalf("second Search: %v", err)
	}
	if lookups != 2 {
		t.Fatalf("user-class lookups after a second search = %d, want 2", lookups)
	}
}

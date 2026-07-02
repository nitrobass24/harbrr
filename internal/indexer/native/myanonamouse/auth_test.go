package myanonamouse

import (
	"context"
	"errors"
	"io"
	stdhttp "net/http"
	"strings"
	"testing"
	"time"

	apphttp "github.com/autobrr/harbrr/internal/http"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/login"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
)

// distinctive synthetic mam_id so a redaction check can prove it never escapes into a
// URL/query or an error string (it may appear only in the Cookie header).
const mamSecret = "MAMID-SECRET-9f8e"

func fixedClock() time.Time { return time.Unix(1_700_000_000, 0).UTC() }

// recordedReq captures one issued request for assertions a black-box transport cannot
// make (the request path/query, the Cookie + Accept headers).
type recordedReq struct {
	method, url, cookie, accept string
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
		cfg:          map[string]string{"mam_id": mamSecret},
		doer:         doer,
		baseURL:      "https://mam.test/",
		clock:        fixedClock,
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
	r, err := d.get(context.Background(), d.baseURL+searchPath+"?tor[text]=x", "application/json")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	_ = r.Body.Close()
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

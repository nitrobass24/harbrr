package cardigann

import (
	"io"
	stdhttp "net/http"
	"strings"
	"sync"
	"testing"
)

// redirectLoginDoer scripts a session-expiry-via-redirect scenario: the FIRST
// /browse answers a 302 to the login page (the raw redirect IS the logged-out
// signal — search requests are never auto-followed), later /browse calls answer
// logged-in results (unless alwaysRedirect is set, to prove the retry is
// bounded). /profile (eager CheckTest) and /login.php (relogin) serve logged-in
// pages.
type redirectLoginDoer struct {
	alwaysRedirect bool

	mu       sync.Mutex
	requests []*stdhttp.Request
	browse   int
}

func (d *redirectLoginDoer) Do(req *stdhttp.Request) (*stdhttp.Response, error) {
	d.mu.Lock()
	d.requests = append(d.requests, req)
	status := stdhttp.StatusOK
	header := stdhttp.Header{}
	body := lazyNav
	if req.URL.Path == "/browse" {
		d.browse++
		if d.alwaysRedirect || d.browse == 1 {
			status = stdhttp.StatusFound
			header.Set("Location", "/login.php?returnto=browse")
			body = ""
		} else {
			body = lazyResults
		}
	}
	d.mu.Unlock()
	return &stdhttp.Response{
		StatusCode: status,
		Header:     header,
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    req,
	}, nil
}

func (d *redirectLoginDoer) count(path string) int {
	d.mu.Lock()
	defer d.mu.Unlock()
	n := 0
	for _, r := range d.requests {
		if r.URL.Path == path {
			n++
		}
	}
	return n
}

// TestSearch_RedirectTriggersRelogin proves the redirect half of Jackett's
// CheckIfLoginIsNeeded: an unfollowed 3xx on a search response (def with a
// login block, no path followredirect) triggers one relogin and one retry —
// without ever fetching the redirect target.
func TestSearch_RedirectTriggersRelogin(t *testing.T) {
	t.Parallel()
	def := loadFixtureDef(t, "lazy_login.yml")
	doer := &redirectLoginDoer{}
	eng, err := NewEngine(def, WithClock(fixedClock()), WithDoer(doer))
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	releases, err := eng.Search(t.Context(), Query{Keywords: "lazy"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(releases) != 1 {
		t.Fatalf("releases = %d, want 1 (retry after redirect-triggered relogin)", len(releases))
	}
	assertTitle(t, releases[0].Title, "Lazy Result")

	if got := doer.count("/login.php"); got != 1 {
		t.Errorf("/login.php (relogin) hits = %d, want 1 (the 302 target is signal, not fetched)", got)
	}
	if got := doer.count("/browse"); got != 2 {
		t.Errorf("/browse hits = %d, want 2 (302 + one retry)", got)
	}
}

// TestSearch_RedirectReloginBounded proves the redirect-signaled logout keeps
// the single-retry bound: when the tracker 302s EVERY search (dead session, IP
// block), Search surfaces an error after exactly two /browse attempts and one
// relogin — never a relogin/search loop.
func TestSearch_RedirectReloginBounded(t *testing.T) {
	t.Parallel()
	def := loadFixtureDef(t, "lazy_login.yml")
	doer := &redirectLoginDoer{alwaysRedirect: true}
	eng, err := NewEngine(def, WithClock(fixedClock()), WithDoer(doer))
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	if _, err := eng.Search(t.Context(), Query{Keywords: "lazy"}); err == nil {
		t.Fatal("Search: want error when every search response redirects, got nil")
	}
	if got := doer.count("/browse"); got != 2 {
		t.Errorf("/browse hits = %d, want exactly 2 (initial + one bounded retry, no loop)", got)
	}
	if got := doer.count("/login.php"); got != 1 {
		t.Errorf("/login.php hits = %d, want 1 (single relogin)", got)
	}
}

package cardigann

import (
	"io"
	stdhttp "net/http"
	"strings"
	"sync"
	"testing"
)

// lazyLoginDoer scripts a session-expiry scenario for the lazy-relogin test. It
// serves, by path:
//   - /profile  : the CheckTest probe (eager first login) — a logged-IN page.
//   - /login.php: the relogin GET (login.method=get) — a logged-IN page.
//   - /browse   : the search. The FIRST call returns a logged-OUT page (no logout
//     link, no rows); subsequent calls return logged-IN results, UNLESS
//     alwaysLoggedOut is set (to prove the retry is bounded).
type lazyLoginDoer struct {
	alwaysLoggedOut bool

	mu       sync.Mutex
	requests []*stdhttp.Request
	browse   int
}

const (
	lazyNav     = `<html><body><nav><a class="logout" href="/logout">Logout</a></nav></body></html>`
	lazyLogin   = `<html><body><form id="login"><input name="username"></form></body></html>`
	lazyResults = `<html><body><nav><a class="logout" href="/logout">Logout</a></nav>` +
		`<table><tr class="row"><td><a class="title">Lazy Result</a></td>` +
		`<td class="size">1 GB</td><td class="seeders">10</td>` +
		`<td><a class="dl" href="/dl/1.torrent">dl</a></td></tr></table></body></html>`
)

func (d *lazyLoginDoer) Do(req *stdhttp.Request) (*stdhttp.Response, error) {
	d.mu.Lock()
	d.requests = append(d.requests, req)
	body := lazyNav
	if req.URL.Path == "/browse" {
		d.browse++
		switch {
		case d.alwaysLoggedOut, d.browse == 1:
			body = lazyLogin // logged-out
		default:
			body = lazyResults // logged-in with one row
		}
	}
	d.mu.Unlock()
	return &stdhttp.Response{
		StatusCode: stdhttp.StatusOK,
		Header:     stdhttp.Header{},
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    req,
	}, nil
}

func (d *lazyLoginDoer) count(path string) int {
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

// TestSearch_LazyRelogin proves that a logged-out search response triggers one
// re-login and one retry, after which the results parse normally. The eager first
// login finds the session valid (CheckTest /profile has the logout link), so the
// only /login.php request comes from the relogin.
func TestSearch_LazyRelogin(t *testing.T) {
	t.Parallel()
	def := loadFixtureDef(t, "lazy_login.yml")
	doer := &lazyLoginDoer{}
	eng, err := NewEngine(def, WithClock(fixedClock()), WithDoer(doer))
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	releases, err := eng.Search(Query{Keywords: "lazy"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(releases) != 1 {
		t.Fatalf("releases = %d, want 1 (retry after relogin parses the logged-in page)", len(releases))
	}
	assertTitle(t, releases[0].Title, "Lazy Result")

	if got := doer.count("/profile"); got != 1 {
		t.Errorf("/profile (eager CheckTest) hits = %d, want 1", got)
	}
	if got := doer.count("/login.php"); got != 1 {
		t.Errorf("/login.php (relogin) hits = %d, want 1", got)
	}
	if got := doer.count("/browse"); got != 2 {
		t.Errorf("/browse hits = %d, want 2 (initial logged-out + one retry)", got)
	}
}

// TestSearch_LazyReloginBounded proves the retry is bounded to a single attempt:
// when the session stays logged-out even after relogin, Search returns an error
// and issues exactly two /browse requests (no loop).
func TestSearch_LazyReloginBounded(t *testing.T) {
	t.Parallel()
	def := loadFixtureDef(t, "lazy_login.yml")
	doer := &lazyLoginDoer{alwaysLoggedOut: true}
	eng, err := NewEngine(def, WithClock(fixedClock()), WithDoer(doer))
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	if _, err := eng.Search(Query{Keywords: "lazy"}); err == nil {
		t.Fatal("Search: want error when session stays logged-out, got nil")
	}
	if got := doer.count("/browse"); got != 2 {
		t.Errorf("/browse hits = %d, want exactly 2 (initial + one bounded retry, no loop)", got)
	}
	if got := doer.count("/login.php"); got != 1 {
		t.Errorf("/login.php hits = %d, want 1 (single relogin)", got)
	}
}

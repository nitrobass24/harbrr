package cardigann

import (
	"fmt"
	"io"
	stdhttp "net/http"
	"strings"
	"sync"
	"testing"
)

// multiPathDoer serves a two-path search where only the SECOND path redirects:
// /a answers rows directly; /b answers a 302 to /b2, which answers rows. The
// def opts /b (and only /b) into followredirect.
type multiPathDoer struct {
	mu       sync.Mutex
	requests []*stdhttp.Request
}

func multiPathRows(title string) string {
	return `<html><body><table><tr class="row"><td><a class="title">` + title + `</a></td>` +
		`<td class="size">1 GB</td><td class="seeders">5</td>` +
		`<td><a class="dl" href="/dl/x.torrent">dl</a></td></tr></table></body></html>`
}

func (d *multiPathDoer) Do(req *stdhttp.Request) (*stdhttp.Response, error) {
	d.mu.Lock()
	d.requests = append(d.requests, req)
	d.mu.Unlock()
	status := stdhttp.StatusOK
	header := stdhttp.Header{}
	var body string
	switch req.URL.Path {
	case "/a":
		body = multiPathRows("Path A Result")
	case "/b":
		status = stdhttp.StatusFound
		header.Set("Location", "/b2")
	case "/b2":
		body = multiPathRows("Path B Result")
	default:
		return nil, fmt.Errorf("multiPathDoer: unexpected path %q", req.URL.Path)
	}
	return &stdhttp.Response{
		StatusCode: status,
		Header:     header,
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    req,
	}, nil
}

// TestSearch_FollowRedirectOnNonFirstPath pins per-path followredirect
// consumption in the multi-path loop: the flag set on the SECOND path (and only
// there) drives the manual follow for that path's 302, with no logged-out
// signal and no cross-path leakage of the flag.
func TestSearch_FollowRedirectOnNonFirstPath(t *testing.T) {
	t.Parallel()
	def := loadFixtureDef(t, "multi_path_redirect.yml")
	doer := &multiPathDoer{}
	eng, err := NewEngine(def, WithClock(fixedClock()), WithDoer(doer))
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	releases, err := eng.Search(t.Context(), Query{Keywords: "x"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(releases) != 2 {
		t.Fatalf("releases = %d, want 2 (one per path, the second via its followed 302)", len(releases))
	}
	assertTitle(t, releases[0].Title, "Path A Result")
	assertTitle(t, releases[1].Title, "Path B Result")

	paths := make([]string, 0, len(doer.requests))
	for _, r := range doer.requests {
		paths = append(paths, r.URL.Path)
	}
	want := "/a /b /b2"
	if got := strings.Join(paths, " "); got != want {
		t.Errorf("request sequence = %q, want %q", got, want)
	}
}

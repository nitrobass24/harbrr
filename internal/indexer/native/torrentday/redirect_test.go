package torrentday

import (
	"context"
	"errors"
	stdhttp "net/http"
	"testing"

	apphttp "github.com/autobrr/harbrr/internal/http"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/indexer/native"
)

// captureDoer records the last request it received and serves a fixed 200, so a test can
// inspect the outgoing request's context.
type captureDoer struct {
	last *stdhttp.Request
	body string
}

func (c *captureDoer) Do(req *stdhttp.Request) (*stdhttp.Response, error) {
	c.last = req
	return resp(stdhttp.StatusOK, c.body), nil
}

// TestSearchAndGrabStampNoRedirectFollow proves Search and Grab both stamp the request
// context WithNoRedirectFollow, so the shared registry client's RedirectPolicy surfaces a
// login redirect (3xx) as a raw response instead of following it — the precondition that
// makes isLoginRedirect reachable, so a stale cookie becomes an auth_failure instead of
// being followed to the login page and misread as a parse error. Without the stamp
// RedirectPolicy returns nil and the stdlib would auto-follow.
func TestSearchAndGrabStampNoRedirectFollow(t *testing.T) {
	t.Parallel()
	newDriver := func(doer search.Doer) *driver {
		d, err := New(native.Params{Def: Families()[0].Definition, Cfg: map[string]string{"cookie": credCookie}, Doer: doer})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		dr := d.(*driver)
		dr.clock = fixedClock
		return dr
	}

	t.Run("search", func(t *testing.T) {
		t.Parallel()
		doer := &captureDoer{body: "[]"}
		if _, err := newDriver(doer).Search(context.Background(), search.Query{}); err != nil {
			t.Fatalf("Search: %v", err)
		}
		assertNoFollowStamped(t, doer.last)
	})

	t.Run("grab", func(t *testing.T) {
		t.Parallel()
		doer := &captureDoer{body: "d8:announce"}
		dr := newDriver(doer)
		if _, err := dr.Grab(context.Background(), dr.baseURL+"download.php/1/1.torrent"); err != nil {
			t.Fatalf("Grab: %v", err)
		}
		assertNoFollowStamped(t, doer.last)
	})
}

// assertNoFollowStamped fails unless req's context carries the no-follow marker, detected
// exactly as the production client does: RedirectPolicy returns ErrUseLastResponse only
// when WithNoRedirectFollow stamped the context.
func assertNoFollowStamped(t *testing.T, req *stdhttp.Request) {
	t.Helper()
	if req == nil {
		t.Fatal("no request was captured")
	}
	if got := apphttp.RedirectPolicy(req, nil); !errors.Is(got, stdhttp.ErrUseLastResponse) {
		t.Errorf("RedirectPolicy = %v, want ErrUseLastResponse (context was not stamped WithNoRedirectFollow)", got)
	}
}

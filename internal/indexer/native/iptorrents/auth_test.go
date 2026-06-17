package iptorrents

import (
	"context"
	"errors"
	stdhttp "net/http"
	"testing"

	apphttp "github.com/autobrr/harbrr/internal/http"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/login"
)

// TestTestActionLoggedIn proves Test() succeeds when the page carries the logout link
// (lout.php), sends the cookie+UA headers, and the request URL leaks no secret.
func TestTestActionLoggedIn(t *testing.T) {
	t.Parallel()
	doer := &scriptDoer{handler: func(_ *stdhttp.Request) *stdhttp.Response {
		return resp(stdhttp.StatusOK, `<html><a href="/lout.php">Logout</a></html>`)
	}}
	d := testDriver(doer, nil)
	if err := d.Test(context.Background()); err != nil {
		t.Fatalf("Test on logged-in page = %v, want nil", err)
	}
	if len(doer.reqs) != 1 {
		t.Fatalf("requests = %d, want 1", len(doer.reqs))
	}
	r := doer.reqs[0]
	if r.cookie != credCookie || r.userAgent != credUA {
		t.Errorf("test request cookie=%q ua=%q, want the configured cookie+UA", r.cookie, r.userAgent)
	}
	assertNoSecret(t, r.url)
}

// TestTestActionNotLoggedIn proves Test() returns login.ErrLoginFailed when the page
// does NOT carry the logout link (the cookie expired / is wrong), and leaks no secret.
func TestTestActionNotLoggedIn(t *testing.T) {
	t.Parallel()
	doer := &scriptDoer{handler: func(_ *stdhttp.Request) *stdhttp.Response {
		return resp(stdhttp.StatusOK, `<html><form action="/login.php">login</form></html>`)
	}}
	d := testDriver(doer, nil)
	err := d.Test(context.Background())
	if !errors.Is(err, login.ErrLoginFailed) {
		t.Fatalf("err = %v, want login.ErrLoginFailed", err)
	}
	assertNoSecret(t, err.Error())
	assertNoSecret(t, apphttp.RedactError(err))
}

// TestTestActionAuthStatus proves a 401/403 status is also an auth failure.
func TestTestActionAuthStatus(t *testing.T) {
	t.Parallel()
	for _, status := range []int{stdhttp.StatusUnauthorized, stdhttp.StatusForbidden} {
		doer := &scriptDoer{handler: func(_ *stdhttp.Request) *stdhttp.Response {
			return resp(status, "denied")
		}}
		if err := testDriver(doer, nil).Test(context.Background()); !errors.Is(err, login.ErrLoginFailed) {
			t.Errorf("status %d: err = %v, want login.ErrLoginFailed", status, err)
		}
	}
}

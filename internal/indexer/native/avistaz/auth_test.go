package avistaz

import (
	"context"
	"errors"
	"io"
	stdhttp "net/http"
	"net/url"
	"strings"
	"testing"

	apphttp "github.com/autobrr/harbrr/internal/http"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/login"
)

// recordedReq captures one issued request for assertions a black-box transport
// cannot make (the auth POST body, the Bearer header).
type recordedReq struct {
	method, url, body, auth, contentType string
}

// scriptDoer records every request and serves a scripted response.
type scriptDoer struct {
	t       *testing.T
	handler func(req *stdhttp.Request, body string) *stdhttp.Response
	reqs    []recordedReq
}

func (s *scriptDoer) Do(req *stdhttp.Request) (*stdhttp.Response, error) {
	var body string
	if req.Body != nil {
		b, _ := io.ReadAll(req.Body)
		body = string(b)
	}
	s.reqs = append(s.reqs, recordedReq{req.Method, req.URL.String(), body, req.Header.Get("Authorization"), req.Header.Get("Content-Type")})
	return s.handler(req, body), nil
}

func resp(status int, body string) *stdhttp.Response {
	return &stdhttp.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: stdhttp.Header{}}
}

// distinctive credential values so a redaction check can prove they never escape.
const (
	credUser = "theuser"
	credPass = "p@ss-SECRET-9f8e"
	credPID  = "PID-SECRET-1234"
)

func newDriver(doer *scriptDoer) *driver {
	return &driver{
		cfg:     map[string]string{"username": credUser, "password": credPass, "pid": credPID},
		doer:    doer,
		baseURL: "https://az.test/",
		profile: profileFor("avistaz"),
	}
}

// TestAuthenticate proves the form-encoded POST to api/v1/jackett/auth carries the
// three credentials and returns the token.
func TestAuthenticate(t *testing.T) {
	t.Parallel()
	doer := &scriptDoer{t: t, handler: func(_ *stdhttp.Request, _ string) *stdhttp.Response {
		return resp(stdhttp.StatusOK, `{"token":"tok-123"}`)
	}}
	d := newDriver(doer)
	tok, err := d.authenticate(context.Background())
	if err != nil {
		t.Fatalf("authenticate: %v", err)
	}
	if tok != "tok-123" {
		t.Errorf("token = %q, want tok-123", tok)
	}
	if len(doer.reqs) != 1 {
		t.Fatalf("requests = %d, want 1", len(doer.reqs))
	}
	r := doer.reqs[0]
	if r.method != stdhttp.MethodPost || !strings.HasSuffix(r.url, "/api/v1/jackett/auth") {
		t.Errorf("auth request = %s %s", r.method, r.url)
	}
	if r.contentType != "application/x-www-form-urlencoded" {
		t.Errorf("Content-Type = %q", r.contentType)
	}
	vals, err := url.ParseQuery(r.body)
	if err != nil {
		t.Fatalf("parse body %q: %v", r.body, err)
	}
	if vals.Get("username") != credUser || vals.Get("password") != credPass || vals.Get("pid") != credPID {
		t.Errorf("auth body = %v, want the three credentials", vals)
	}
}

// TestAuthenticateTrims proves whitespace around the credentials is stripped (matching
// Prowlarr/Jackett).
func TestAuthenticateTrims(t *testing.T) {
	t.Parallel()
	doer := &scriptDoer{t: t, handler: func(_ *stdhttp.Request, _ string) *stdhttp.Response {
		return resp(stdhttp.StatusOK, `{"token":"t"}`)
	}}
	d := newDriver(doer)
	d.cfg = map[string]string{"username": "  u  ", "password": " p ", "pid": "\tx\n"}
	if _, err := d.authenticate(context.Background()); err != nil {
		t.Fatalf("authenticate: %v", err)
	}
	vals, _ := url.ParseQuery(doer.reqs[0].body)
	if vals.Get("username") != "u" || vals.Get("password") != "p" || vals.Get("pid") != "x" {
		t.Errorf("trimmed body = %v", vals)
	}
}

// TestAuthFailure proves a 401 is an auth failure (errors.Is login.ErrLoginFailed),
// surfaces the server message, and leaks no credential.
func TestAuthFailure(t *testing.T) {
	t.Parallel()
	doer := &scriptDoer{t: t, handler: func(_ *stdhttp.Request, _ string) *stdhttp.Response {
		return resp(stdhttp.StatusUnauthorized, `{"message":"Invalid user credentials"}`)
	}}
	d := newDriver(doer)
	_, err := d.authenticate(context.Background())
	if err == nil || !errors.Is(err, login.ErrLoginFailed) {
		t.Fatalf("err = %v, want login.ErrLoginFailed", err)
	}
	if !strings.Contains(err.Error(), "Invalid user credentials") {
		t.Errorf("err should surface the server message: %v", err)
	}
	assertNoSecret(t, err.Error())
	assertNoSecret(t, apphttp.RedactError(err))
}

// TestAuthFailureScrubsEchoedCredentials proves a hostile/buggy server that reflects
// the submitted password/PID in its error message cannot leak them: the credential
// values are scrubbed before the message reaches the wrapped error (and therefore the
// persisted health-event detail). RedactError alone would NOT catch these — they appear
// as bare free-prose tokens, not key=value pairs — so the driver scrubs them itself.
func TestAuthFailureScrubsEchoedCredentials(t *testing.T) {
	t.Parallel()
	echo := `{"message":"login failed for theuser with password ` + credPass + ` and pid ` + credPID + `"}`
	doer := &scriptDoer{t: t, handler: func(_ *stdhttp.Request, _ string) *stdhttp.Response {
		return resp(stdhttp.StatusUnprocessableEntity, echo)
	}}
	d := newDriver(doer)
	_, err := d.authenticate(context.Background())
	if err == nil || !errors.Is(err, login.ErrLoginFailed) {
		t.Fatalf("err = %v, want login.ErrLoginFailed", err)
	}
	assertNoSecret(t, err.Error())
	assertNoSecret(t, apphttp.RedactError(err))
}

// TestReactiveRefresh proves a 401 on a search GET triggers exactly one re-auth and
// one retry, and that each GET carries the current Bearer token.
func TestReactiveRefresh(t *testing.T) {
	t.Parallel()
	var auths, gets int
	doer := &scriptDoer{t: t, handler: func(req *stdhttp.Request, _ string) *stdhttp.Response {
		if strings.HasSuffix(req.URL.Path, "/api/v1/jackett/auth") {
			auths++
			return resp(stdhttp.StatusOK, `{"token":"tok-`+itoa(auths)+`"}`)
		}
		gets++
		if gets == 1 {
			return resp(stdhttp.StatusUnauthorized, `{"message":"expired"}`)
		}
		return resp(stdhttp.StatusOK, `{"data":[]}`)
	}}
	d := newDriver(doer)
	r, err := d.get(context.Background(), d.baseURL+"api/v1/jackett/torrents?in=1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	_ = r.Body.Close()
	if r.StatusCode != stdhttp.StatusOK {
		t.Errorf("final status = %d, want 200", r.StatusCode)
	}
	if auths != 2 || gets != 2 {
		t.Errorf("auths=%d gets=%d, want 2/2 (one reactive re-auth + retry)", auths, gets)
	}
	// The first GET used token-1, the retry used the refreshed token-2.
	var first, second string
	for _, q := range doer.reqs {
		if strings.Contains(q.url, "torrents") {
			if first == "" {
				first = q.auth
			} else {
				second = q.auth
			}
		}
	}
	if first != "Bearer tok-1" || second != "Bearer tok-2" {
		t.Errorf("GET auth headers = %q then %q, want Bearer tok-1 then tok-2", first, second)
	}
}

// TestTokenCaching proves a successful token is reused (no re-auth on the next call).
func TestTokenCaching(t *testing.T) {
	t.Parallel()
	var auths int
	doer := &scriptDoer{t: t, handler: func(_ *stdhttp.Request, _ string) *stdhttp.Response {
		auths++
		return resp(stdhttp.StatusOK, `{"token":"t"}`)
	}}
	d := newDriver(doer)
	for range 3 {
		if _, err := d.ensureToken(context.Background()); err != nil {
			t.Fatalf("ensureToken: %v", err)
		}
	}
	if auths != 1 {
		t.Errorf("auth requests = %d, want 1 (token cached)", auths)
	}
}

// TestTestAction proves Test() returns nil on good creds and the auth error on bad.
func TestTestAction(t *testing.T) {
	t.Parallel()
	ok := newDriver(&scriptDoer{t: t, handler: func(_ *stdhttp.Request, _ string) *stdhttp.Response {
		return resp(stdhttp.StatusOK, `{"token":"t"}`)
	}})
	if err := ok.Test(context.Background()); err != nil {
		t.Errorf("Test on good creds = %v, want nil", err)
	}
	bad := newDriver(&scriptDoer{t: t, handler: func(_ *stdhttp.Request, _ string) *stdhttp.Response {
		return resp(stdhttp.StatusUnauthorized, `{"message":"nope"}`)
	}})
	if err := bad.Test(context.Background()); !errors.Is(err, login.ErrLoginFailed) {
		t.Errorf("Test on bad creds = %v, want login.ErrLoginFailed", err)
	}
}

func assertNoSecret(t *testing.T, s string) {
	t.Helper()
	for _, secret := range []string{credPass, credPID} {
		if strings.Contains(s, secret) {
			t.Errorf("string leaks a credential (%q): %q", secret, s)
		}
	}
}

func itoa(n int) string {
	return string(rune('0' + n))
}

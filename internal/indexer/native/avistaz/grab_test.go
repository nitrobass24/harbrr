package avistaz

import (
	"context"
	"errors"
	stdhttp "net/http"
	"strings"
	"testing"

	apphttp "github.com/autobrr/harbrr/internal/http"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/login"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
)

// fakeTorrent is a minimal bencode-shaped body (content is irrelevant to the driver;
// it is returned verbatim to the /dl proxy).
const fakeTorrent = "d8:announce18:https://az.test/an4:infod6:lengthi1e4:name4:fileee"

func grabDriver(handler func(*stdhttp.Request, string) *stdhttp.Response) (*driver, *scriptDoer) {
	doer := &scriptDoer{handler: handler}
	d := &driver{
		cfg:     map[string]string{"username": credUser, "password": credPass, "pid": credPID},
		doer:    doer,
		baseURL: "https://az.test/",
		profile: profileFor("avistaz"),
		clock:   fixedClock,
	}
	return d, doer
}

// TestGrab proves the download is fetched with the Bearer header and returned as a
// direct torrent (no redirect), and that no credential leaks into the result.
func TestGrab(t *testing.T) {
	t.Parallel()
	d, doer := grabDriver(func(req *stdhttp.Request, _ string) *stdhttp.Response {
		if strings.HasSuffix(req.URL.Path, "/"+authPath) {
			return resp(stdhttp.StatusOK, `{"token":"tok-grab"}`)
		}
		r := resp(stdhttp.StatusOK, fakeTorrent)
		r.Header.Set("Content-Type", "application/x-bittorrent")
		return r
	})

	res, err := d.Grab(context.Background(), "https://az.test/api/v1/jackett/download/9")
	if err != nil {
		t.Fatalf("Grab: %v", err)
	}
	if string(res.Body) != fakeTorrent {
		t.Errorf("body = %q, want the torrent bytes", res.Body)
	}
	if res.ContentType != "application/x-bittorrent" {
		t.Errorf("ContentType = %q", res.ContentType)
	}
	if res.Redirect != "" {
		t.Errorf("Redirect = %q, want empty (direct torrent)", res.Redirect)
	}

	var dl *recordedReq
	for i := range doer.reqs {
		if strings.Contains(doer.reqs[i].url, "download") {
			dl = &doer.reqs[i]
		}
	}
	if dl == nil {
		t.Fatal("no download request recorded")
	}
	if dl.method != stdhttp.MethodGet || dl.auth != "Bearer tok-grab" {
		t.Errorf("download request = %s auth=%q, want GET Bearer tok-grab", dl.method, dl.auth)
	}
	assertNoSecret(t, string(res.Body))
	assertNoSecret(t, dl.url)
}

// TestGrabStatusErrors proves a 429 is a rate-limit error, a persistent 401 (surviving
// the reactive re-auth) is an auth failure, and another non-2xx is a plain error — none
// leaking a credential.
func TestGrabStatusErrors(t *testing.T) {
	t.Parallel()
	mk := func(status int) *driver {
		d, _ := grabDriver(func(req *stdhttp.Request, _ string) *stdhttp.Response {
			if strings.HasSuffix(req.URL.Path, "/"+authPath) {
				return resp(stdhttp.StatusOK, `{"token":"t"}`)
			}
			return resp(status, "nope")
		})
		return d
	}

	_, err := mk(stdhttp.StatusTooManyRequests).Grab(context.Background(), "https://az.test/dl/1")
	var rl *search.RateLimitedError
	if !errors.As(err, &rl) {
		t.Errorf("429: err = %v, want *search.RateLimitedError", err)
	}

	_, err = mk(stdhttp.StatusUnauthorized).Grab(context.Background(), "https://az.test/dl/1")
	if !errors.Is(err, login.ErrLoginFailed) {
		t.Errorf("401: err = %v, want login.ErrLoginFailed", err)
	}

	_, err = mk(stdhttp.StatusInternalServerError).Grab(context.Background(), "https://az.test/dl/1")
	if err == nil {
		t.Fatal("500: want an error")
	}
	assertNoSecret(t, err.Error())
	assertNoSecret(t, apphttp.RedactError(err))
}

// authThenErrorDoer answers the auth POST with a token, then fails the download fetch
// with a transport error — the case where sendBearer would wrap the download URL.
type authThenErrorDoer struct{ downloadErr error }

func (a *authThenErrorDoer) Do(req *stdhttp.Request) (*stdhttp.Response, error) {
	if strings.HasSuffix(req.URL.Path, "/"+authPath) {
		return resp(stdhttp.StatusOK, `{"token":"t"}`), nil
	}
	return nil, a.downloadErr
}

// TestGrabTransportErrorSanitized proves a transport failure during the download fetch
// surfaces a fixed, link-free error even though the download URL carries a key in its
// PATH (which the query-scoped URL redactor cannot reach).
func TestGrabTransportErrorSanitized(t *testing.T) {
	t.Parallel()
	const link = "https://az.test/rss/download/9/PATHKEY-SECRET-zzzz.torrent"
	d := &driver{
		cfg:     map[string]string{"username": credUser, "password": credPass, "pid": credPID},
		doer:    &authThenErrorDoer{downloadErr: errors.New("connection refused")},
		baseURL: "https://az.test/",
		profile: profileFor("avistaz"),
		clock:   fixedClock,
	}
	_, err := d.Grab(context.Background(), link)
	if err == nil {
		t.Fatal("want a transport error")
	}
	if strings.Contains(err.Error(), "PATHKEY-SECRET") || strings.Contains(err.Error(), link) {
		t.Errorf("download URL/key leaked into the error: %v", err)
	}
}

func TestReadCapped(t *testing.T) {
	t.Parallel()
	if _, err := readCapped(strings.NewReader("0123456789AB"), 10); !errors.Is(err, errDownloadTooLarge) {
		t.Errorf("12 bytes over cap 10: err = %v, want errDownloadTooLarge", err)
	}
	got, err := readCapped(strings.NewReader("0123456789"), 10) // exactly at cap is fine
	if err != nil || len(got) != 10 {
		t.Errorf("at cap: len=%d err=%v, want 10/nil", len(got), err)
	}
	got, err = readCapped(strings.NewReader("hello"), 10)
	if err != nil || string(got) != "hello" {
		t.Errorf("under cap: got=%q err=%v", got, err)
	}
}

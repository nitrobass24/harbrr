package speedapp

import (
	"encoding/json"
	"errors"
	"fmt"
	stdhttp "net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/login"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
)

func TestGrabReturnsTorrentWithBearer(t *testing.T) {
	t.Parallel()
	doer := &scriptDoer{handler: func(req *stdhttp.Request, _ []byte) (*stdhttp.Response, error) {
		if req.Header.Get("Authorization") != "Bearer "+testToken {
			t.Errorf("Authorization = %q", req.Header.Get("Authorization"))
		}
		if req.Header.Get("Accept") != "" {
			t.Errorf("grab Accept = %q, want empty", req.Header.Get("Accept"))
		}
		return torrentResponse(stdhttp.StatusOK, torrentBytes), nil
	}}
	d := testDriver(t, doer)
	seedDriverToken(d, testToken, 1)
	link := d.BaseURL + "api/torrent/123/download"
	grab, err := d.Grab(t.Context(), link)
	if err != nil {
		t.Fatalf("Grab: %v", err)
	}
	if string(grab.Body) != torrentBytes || grab.ContentType != "application/x-bittorrent" || grab.Redirect != "" {
		t.Errorf("grab = %+v", grab)
	}
	records := doer.records()
	if len(records) != 1 || records[0].method != stdhttp.MethodGet || records[0].url != link {
		t.Errorf("requests = %+v", records)
	}
	for _, secret := range []string{testEmail, testPassword, testToken} {
		if strings.Contains(records[0].url, secret) {
			t.Errorf("grab URL leaked %q: %s", secret, records[0].url)
		}
	}
}

func TestGrabRefreshesOnceOn401(t *testing.T) {
	t.Parallel()
	var logins, oldGrabs, newGrabs atomic.Int64
	doer := &scriptDoer{handler: func(req *stdhttp.Request, _ []byte) (*stdhttp.Response, error) {
		if req.URL.Path == "/api/login" {
			logins.Add(1)
			return jsonResponse(stdhttp.StatusOK, `{"token":"`+nextToken+`"}`), nil
		}
		switch req.Header.Get("Authorization") {
		case "Bearer " + testToken:
			oldGrabs.Add(1)
			return jsonResponse(stdhttp.StatusUnauthorized, `{"message":"expired `+testToken+`"}`), nil
		case "Bearer " + nextToken:
			newGrabs.Add(1)
			return torrentResponse(stdhttp.StatusOK, torrentBytes), nil
		default:
			return nil, fmt.Errorf("unexpected Authorization %q", req.Header.Get("Authorization"))
		}
	}}
	d := testDriver(t, doer)
	seedDriverToken(d, testToken, 1)
	grab, err := d.Grab(t.Context(), d.BaseURL+"api/torrent/123/download")
	if err != nil {
		t.Fatalf("Grab: %v", err)
	}
	if string(grab.Body) != torrentBytes {
		t.Errorf("body = %q", grab.Body)
	}
	if logins.Load() != 1 || oldGrabs.Load() != 1 || newGrabs.Load() != 1 {
		t.Errorf("login/old/new = %d/%d/%d, want 1/1/1", logins.Load(), oldGrabs.Load(), newGrabs.Load())
	}
}

func TestGrabStatusAndRateErrors(t *testing.T) {
	t.Parallel()
	for _, status := range []int{stdhttp.StatusForbidden, stdhttp.StatusTooManyRequests, stdhttp.StatusServiceUnavailable} {
		t.Run(stdhttp.StatusText(status), func(t *testing.T) {
			t.Parallel()
			d := testDriver(t, &scriptDoer{handler: func(*stdhttp.Request, []byte) (*stdhttp.Response, error) {
				return jsonResponse(status, `{"message":"refused"}`), nil
			}})
			seedDriverToken(d, testToken, 1)
			_, err := d.Grab(t.Context(), d.BaseURL+"api/torrent/123/download")
			if err == nil {
				t.Fatal("want error")
			}
			var rate *search.RateLimitedError
			if got := errors.As(err, &rate); got != (status != stdhttp.StatusForbidden) {
				t.Errorf("rate classification = %v: %v", got, err)
			}
			if status == stdhttp.StatusForbidden && errors.Is(err, login.ErrLoginFailed) {
				t.Errorf("403 was treated as auth: %v", err)
			}
		})
	}
}

func TestGrabTransportErrorIsSanitized(t *testing.T) {
	t.Parallel()
	link := "https://retroflix.club/api/torrent/123/download"
	d := testDriver(t, &scriptDoer{handler: func(*stdhttp.Request, []byte) (*stdhttp.Response, error) {
		return nil, &url.Error{
			Op:  "Get",
			URL: link + "?token=" + testToken,
			Err: errors.New("connection refused for " + testToken),
		}
	}})
	seedDriverToken(d, testToken, 1)
	_, err := d.Grab(t.Context(), link)
	if err == nil {
		t.Fatal("want transport error")
	}
	if !strings.Contains(err.Error(), "https://retroflix.club") {
		t.Errorf("error lost host: %v", err)
	}
	for _, secret := range []string{testEmail, testPassword, testToken} {
		if strings.Contains(err.Error(), secret) {
			t.Errorf("error leaked %q: %v", secret, err)
		}
	}
}

func TestGrabRefusalCaptureScrubsRuntimeToken(t *testing.T) {
	t.Parallel()
	d := testDriver(t, &scriptDoer{handler: func(*stdhttp.Request, []byte) (*stdhttp.Response, error) {
		return jsonResponse(stdhttp.StatusForbidden, `{"message":"rejected bearer `+testToken+`"}`), nil
	}})
	seedDriverToken(d, testToken, 1)
	_, err := d.Grab(t.Context(), d.BaseURL+"api/torrent/123/download")
	if err == nil {
		t.Fatal("want refusal error")
	}
	capture, ok := search.CaptureOf(err)
	if !ok {
		t.Fatalf("error has no refusal capture: %v", err)
	}
	rendered, marshalErr := json.Marshal(capture)
	if marshalErr != nil {
		t.Fatalf("marshal capture: %v", marshalErr)
	}
	if strings.Contains(string(rendered), testToken) || strings.Contains(err.Error(), testToken) {
		t.Errorf("refusal leaked runtime token: error=%v capture=%s", err, rendered)
	}
}

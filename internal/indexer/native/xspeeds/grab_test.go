package xspeeds

import (
	"errors"
	"io"
	stdhttp "net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/indexer/native"
)

const testMaxTorrentBytes = 64 << 20

func TestGrabTorrent(t *testing.T) {
	handler := stdhttp.HandlerFunc(func(writer stdhttp.ResponseWriter, request *stdhttp.Request) {
		if request.URL.Path != "/download.php" {
			stdhttp.NotFound(writer, request)
			return
		}
		if cookie, err := request.Cookie("session"); err != nil || cookie.Value != "synthetic-xspeeds-old-cookie" {
			t.Errorf("download cookie = %v, %v", cookie, err)
		}
		writer.Header().Set("Content-Type", "application/x-bittorrent")
		_, _ = writer.Write([]byte("d4:infod4:name4:teste"))
	})
	cfg := testConfig()
	cfg["cookie"] = testOldCookie
	driver, server := newTestServerDriver(t, handler, cfg, nil)

	result, err := driver.Grab(t.Context(), server.URL+"/download.php?id=1")
	if err != nil {
		t.Fatalf("Grab: %v", err)
	}
	if result.ContentType != "application/x-bittorrent" || string(result.Body) != "d4:infod4:name4:teste" {
		t.Errorf("Grab result = %q %q", result.ContentType, result.Body)
	}
}

func TestGrabBencodedBodySkipsLoginPageParsing(t *testing.T) {
	var logins atomic.Int64
	body := []byte(`d1:x36:<form action="takelogin.php"></form>e`)
	handler := stdhttp.HandlerFunc(func(writer stdhttp.ResponseWriter, request *stdhttp.Request) {
		switch request.URL.Path {
		case "/login.php":
		case "/takelogin.php":
			logins.Add(1)
			setTestCookie(writer, "session", "synthetic-fresh-cookie")
			_, _ = writer.Write(readFixture(t, "login_success.html"))
		case "/download.php":
			writer.Header().Set("Content-Type", "application/x-bittorrent")
			_, _ = writer.Write(body)
		default:
			stdhttp.NotFound(writer, request)
		}
	})
	cfg := testConfig()
	cfg["cookie"] = testOldCookie
	driver, _ := newTestServerDriver(t, handler, cfg, nil)

	result, err := driver.Grab(t.Context(), "/download.php?id=1")
	if err != nil {
		t.Fatalf("Grab: %v", err)
	}
	if string(result.Body) != string(body) || logins.Load() != 0 {
		t.Errorf("result/login count = %q/%d, want original body and no login", result.Body, logins.Load())
	}
}

func TestGrabRejectsCrossOriginURL(t *testing.T) {
	var requests atomic.Int64
	cfg := testConfig()
	cfg["cookie"] = testOldCookie
	driver := newTestDriver(t, "https://xspeeds.example/", cfg, roundTripDoer(func(*stdhttp.Request) (*stdhttp.Response, error) {
		requests.Add(1)
		return nil, errors.New("unexpected request")
	}), nil)

	_, err := driver.Grab(t.Context(), "https://evil.example/download.php?id=1")
	if err == nil || requests.Load() != 0 {
		t.Fatalf("Grab error/requests = %v/%d, want local rejection", err, requests.Load())
	}
}

func TestGrabRenewsLoginPage(t *testing.T) {
	var logins, downloads atomic.Int64
	handler := stdhttp.HandlerFunc(func(writer stdhttp.ResponseWriter, request *stdhttp.Request) {
		switch request.URL.Path {
		case "/login.php":
		case "/takelogin.php":
			logins.Add(1)
			setTestCookie(writer, "session", "synthetic-fresh-cookie")
			_, _ = writer.Write(readFixture(t, "login_success.html"))
		case "/download.php":
			downloads.Add(1)
			cookie, _ := request.Cookie("session")
			if cookie == nil || cookie.Value != "synthetic-fresh-cookie" {
				_, _ = writer.Write([]byte(`<html><form action="takelogin.php"></form></html>`))
				return
			}
			writer.Header().Set("Content-Type", "application/x-bittorrent")
			_, _ = writer.Write([]byte("d4:infode"))
		}
	})
	cfg := testConfig()
	cfg["cookie"] = testOldCookie
	driver, _ := newTestServerDriver(t, handler, cfg, nil)
	result, err := driver.Grab(t.Context(), "/download.php?id=1")
	if err != nil {
		t.Fatalf("Grab: %v", err)
	}
	if string(result.Body) != "d4:infode" || logins.Load() != 1 || downloads.Load() != 2 {
		t.Errorf("result/requests = %q, %d logins, %d downloads", result.Body, logins.Load(), downloads.Load())
	}
}

func TestGrabReadFailures(t *testing.T) {
	tests := []struct {
		name    string
		body    io.ReadCloser
		wantErr error
	}{
		{name: "failed read", body: &failingBody{}, wantErr: native.ErrBodyRead},
		{name: "oversized", body: io.NopCloser(io.LimitReader(zeroReader{}, testMaxTorrentBytes+1)), wantErr: native.ErrDownloadTooLarge},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := testConfig()
			cfg["cookie"] = testOldCookie
			transport := roundTripDoer(func(*stdhttp.Request) (*stdhttp.Response, error) {
				return &stdhttp.Response{StatusCode: stdhttp.StatusOK, Header: stdhttp.Header{}, Body: test.body}, nil
			})
			driver := newTestDriver(t, "https://xspeeds.example/", cfg, transport, nil)
			_, err := driver.Grab(t.Context(), "https://xspeeds.example/download.php?id=1")
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("Grab error = %v, want %v", err, test.wantErr)
			}
		})
	}
}

func TestGrabRefusalCaptureScrubsSessionSecrets(t *testing.T) {
	const freshCookie = "synthetic-xspeeds-fresh-cookie"
	var downloads atomic.Int64
	handler := stdhttp.HandlerFunc(func(writer stdhttp.ResponseWriter, request *stdhttp.Request) {
		switch request.URL.Path {
		case "/login.php":
		case "/takelogin.php":
			setTestCookie(writer, "session", freshCookie)
			_, _ = writer.Write(readFixture(t, "login_success.html"))
		case "/download.php":
			downloads.Add(1)
			writer.Header().Set("X-Debug", testUsername+" "+freshCookie)
			writer.WriteHeader(stdhttp.StatusForbidden)
			_, _ = writer.Write([]byte(strings.Join([]string{
				testUsername, testPassword, "synthetic-xspeeds-old-cookie", freshCookie, "diagnostic marker 1",
			}, " ")))
		}
	})
	cfg := testConfig()
	cfg["cookie"] = testOldCookie + "; hide_ads=1"
	driver, _ := newTestServerDriver(t, handler, cfg, nil)
	_, err := driver.Grab(t.Context(), "/download.php?id=1")
	if err == nil || downloads.Load() != 2 {
		t.Fatalf("Grab error/downloads = %v/%d, want exhausted retry", err, downloads.Load())
	}
	capture, ok := search.CaptureOf(err)
	if !ok {
		t.Fatal("Grab error has no refusal capture")
	}
	retained := capture.Body + strings.Join(headerValues(capture.Headers), " ") + err.Error()
	for _, secret := range []string{testUsername, testPassword, "synthetic-xspeeds-old-cookie", freshCookie} {
		if strings.Contains(retained, secret) {
			t.Errorf("capture/error leaked %q: %+v / %v", secret, capture, err)
		}
	}
	if !strings.Contains(retained, "diagnostic marker 1") {
		t.Errorf("capture over-scrubbed preference value: %+v", capture)
	}
}

func headerValues(headers map[string]string) []string {
	values := make([]string, 0, len(headers))
	for _, value := range headers {
		values = append(values, value)
	}
	return values
}

type failingBody struct {
	read bool
}

func (body *failingBody) Read(buffer []byte) (int, error) {
	if !body.read {
		body.read = true
		return copy(buffer, "partial"), nil
	}
	return 0, io.ErrUnexpectedEOF
}

func (*failingBody) Close() error { return nil }

type zeroReader struct{}

func (zeroReader) Read(buffer []byte) (int, error) {
	for index := range buffer {
		buffer[index] = 0
	}
	return len(buffer), nil
}

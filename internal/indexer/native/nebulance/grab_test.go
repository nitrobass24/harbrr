package nebulance

import (
	"context"
	"errors"
	stdhttp "net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/login"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/indexer/native"
)

const testDownloadURL = "https://nebulance.io/api.php?action=download&apikey=NBL-SYNTHETIC-DOWNLOAD-TOKEN&torrentid=101"

// TestGrab proves the download URL is fetched with a plain GET (the token already
// rides its query) and returned as a direct torrent (no redirect).
func TestGrab(t *testing.T) {
	t.Parallel()
	doer := &scriptDoer{handler: func(*stdhttp.Request) (*stdhttp.Response, error) {
		resp := response(stdhttp.StatusOK, "d4:name7:examplee")
		resp.Header.Set("Content-Type", "application/x-bittorrent")
		return resp, nil
	}}
	driver := liveDriver(t, doer)
	result, err := driver.Grab(context.Background(), testDownloadURL)
	if err != nil {
		t.Fatalf("Grab: %v", err)
	}
	if string(result.Body) != "d4:name7:examplee" || result.ContentType != "application/x-bittorrent" || result.Redirect != "" {
		t.Error("grab result mismatch")
	}
	if len(doer.reqs) != 1 || doer.reqs[0].method != stdhttp.MethodGet {
		t.Error("grab must issue one GET")
	}
}

// TestGrabStatusDispatch proves a 401/403 is an actionable auth failure, a 429/503 is
// a rate-limit error, and any other non-2xx is a plain error.
func TestGrabStatusDispatch(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		status   int
		wantAuth bool
		wantRate bool
		wantHTTP string
	}{
		{name: "unauthorized", status: stdhttp.StatusUnauthorized, wantAuth: true},
		{name: "forbidden", status: stdhttp.StatusForbidden, wantAuth: true},
		{name: "rate limited", status: stdhttp.StatusTooManyRequests, wantRate: true},
		{name: "unavailable", status: stdhttp.StatusServiceUnavailable, wantRate: true},
		{name: "generic", status: stdhttp.StatusInternalServerError, wantHTTP: "HTTP 500"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			driver := liveDriver(t, &scriptDoer{handler: func(*stdhttp.Request) (*stdhttp.Response, error) {
				resp := response(tt.status, "error")
				if tt.wantRate {
					resp.Header.Set("Retry-After", "120")
				}
				return resp, nil
			}})
			_, err := driver.Grab(context.Background(), testDownloadURL)
			if tt.wantAuth {
				assertActionableAuthError(t, err)
			} else if errors.Is(err, login.ErrLoginFailed) {
				t.Errorf("auth classification = %v, want %v: %v", errors.Is(err, login.ErrLoginFailed), tt.wantAuth, err)
			}
			var rateLimited *search.RateLimitedError
			gotRate := errors.As(err, &rateLimited)
			if tt.wantRate {
				if !gotRate {
					t.Fatalf("err = %v, want RateLimitedError", err)
				}
				if rateLimited.StatusCode != tt.status || rateLimited.RetryAfter != 2*time.Minute {
					t.Errorf("rate-limit metadata = (%d, %s), want (%d, 2m)", rateLimited.StatusCode, rateLimited.RetryAfter, tt.status)
				}
			} else if gotRate {
				t.Errorf("unexpected rate-limit classification: %v", err)
			}
			if tt.wantHTTP != "" && (err == nil || !strings.Contains(err.Error(), tt.wantHTTP)) {
				t.Errorf("err = %v, want %q", err, tt.wantHTTP)
			}
		})
	}
}

// TestGrabTransportErrorRedactsToken proves a transport failure surfaces the
// endpoint's scheme://host (not a secret) while the download link's token — carried
// in its query — never reaches the error.
func TestGrabTransportErrorRedactsToken(t *testing.T) {
	t.Parallel()
	transportErr := &url.Error{Op: "Get", URL: testDownloadURL, Err: errors.New("connection refused")}
	driver := liveDriver(t, &errorDoer{err: transportErr})
	_, err := driver.Grab(context.Background(), testDownloadURL)
	if err == nil {
		t.Fatal("want transport error")
	}
	if strings.Contains(err.Error(), "NBL-SYNTHETIC-DOWNLOAD-TOKEN") || strings.Contains(err.Error(), "apikey=") {
		t.Error("grab error leaked download token")
	}
	if !strings.Contains(err.Error(), "nebulance.io") {
		t.Errorf("error should surface the host: %v", err)
	}
}

func TestGrabContextErrors(t *testing.T) {
	t.Parallel()
	for _, want := range []error{context.Canceled, context.DeadlineExceeded} {
		driver := liveDriver(t, &errorDoer{err: want})
		if _, err := driver.Grab(context.Background(), testDownloadURL); !errors.Is(err, want) {
			t.Errorf("err = %v, want %v", err, want)
		}
	}
}

func TestGrabDownloadTooLarge(t *testing.T) {
	t.Parallel()
	big := strings.Repeat("x", (64<<20)+1)
	driver := liveDriver(t, &scriptDoer{handler: func(*stdhttp.Request) (*stdhttp.Response, error) {
		return response(stdhttp.StatusOK, big), nil
	}})
	_, err := driver.Grab(context.Background(), testDownloadURL)
	if !errors.Is(err, native.ErrDownloadTooLarge) {
		t.Errorf("err = %v, want native.ErrDownloadTooLarge", err)
	}
}

package xspeeds

import (
	"errors"
	stdhttp "net/http"
	"sync/atomic"
	"testing"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/login"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
)

func TestSearchRenewsAuthFailures(t *testing.T) {
	statuses := []int{
		stdhttp.StatusMovedPermanently,
		stdhttp.StatusFound,
		stdhttp.StatusSeeOther,
		stdhttp.StatusTemporaryRedirect,
		stdhttp.StatusPermanentRedirect,
		stdhttp.StatusUnauthorized,
		stdhttp.StatusForbidden,
	}
	for _, status := range statuses {
		t.Run(stdhttp.StatusText(status), func(t *testing.T) {
			var logins, browses atomic.Int64
			handler := stdhttp.HandlerFunc(func(writer stdhttp.ResponseWriter, request *stdhttp.Request) {
				switch request.URL.Path {
				case "/login.php":
				case "/takelogin.php":
					logins.Add(1)
					setTestCookie(writer, "session", "synthetic-fresh-cookie")
					_, _ = writer.Write(readFixture(t, "login_success.html"))
				case "/browse.php":
					browses.Add(1)
					cookie, _ := request.Cookie("session")
					if cookie == nil || cookie.Value != "synthetic-fresh-cookie" {
						if status >= 300 && status < 400 {
							writer.Header().Set("Location", "/login.php")
						}
						writer.WriteHeader(status)
						return
					}
					_, _ = writer.Write(readFixture(t, "browse_empty.html"))
				}
			})
			cfg := testConfig()
			cfg["cookie"] = testOldCookie
			driver, _ := newTestServerDriver(t, handler, cfg, nil)
			if _, err := driver.Search(t.Context(), search.Query{}); err != nil {
				t.Fatalf("Search: %v", err)
			}
			if logins.Load() != 1 || browses.Load() != 2 {
				t.Errorf("requests = %d logins, %d browses; want 1, 2", logins.Load(), browses.Load())
			}
		})
	}
}

func TestSearchDoesNotRenewNonAuthFailures(t *testing.T) {
	tests := []struct {
		status  int
		wantErr error
	}{
		{status: stdhttp.StatusTooManyRequests, wantErr: search.ErrRateLimited},
		{status: stdhttp.StatusServiceUnavailable, wantErr: search.ErrRateLimited},
		{status: stdhttp.StatusBadGateway, wantErr: search.ErrGatewayStatus},
	}
	for _, test := range tests {
		t.Run(stdhttp.StatusText(test.status), func(t *testing.T) {
			var logins atomic.Int64
			handler := stdhttp.HandlerFunc(func(writer stdhttp.ResponseWriter, request *stdhttp.Request) {
				if request.URL.Path == "/takelogin.php" {
					logins.Add(1)
				}
				writer.WriteHeader(test.status)
			})
			cfg := testConfig()
			cfg["cookie"] = testOldCookie
			driver, _ := newTestServerDriver(t, handler, cfg, nil)
			_, err := driver.Search(t.Context(), search.Query{})
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("Search error = %v, want %v", err, test.wantErr)
			}
			if logins.Load() != 0 {
				t.Errorf("login count = %d, want 0", logins.Load())
			}
		})
	}
}

func TestSearchRetriesAuthExactlyOnce(t *testing.T) {
	var logins, browses atomic.Int64
	handler := stdhttp.HandlerFunc(func(writer stdhttp.ResponseWriter, request *stdhttp.Request) {
		switch request.URL.Path {
		case "/takelogin.php":
			logins.Add(1)
			setTestCookie(writer, "session", "synthetic-fresh-cookie")
			_, _ = writer.Write(readFixture(t, "login_success.html"))
		case "/browse.php":
			browses.Add(1)
			writer.WriteHeader(stdhttp.StatusForbidden)
		}
	})
	cfg := testConfig()
	cfg["cookie"] = testOldCookie
	driver, _ := newTestServerDriver(t, handler, cfg, nil)
	_, err := driver.Search(t.Context(), search.Query{})
	if !errors.Is(err, login.ErrLoginFailed) || logins.Load() != 1 || browses.Load() != 2 {
		t.Fatalf("error/requests = %v, %d logins, %d browses; want login failure, 1, 2", err, logins.Load(), browses.Load())
	}
	_, err = driver.Search(t.Context(), search.Query{})
	if !errors.Is(err, login.ErrLoginFailed) || logins.Load() != 1 || browses.Load() != 2 {
		t.Fatalf("remembered error/requests = %v, %d logins, %d browses; want login failure, 1, 2", err, logins.Load(), browses.Load())
	}
}

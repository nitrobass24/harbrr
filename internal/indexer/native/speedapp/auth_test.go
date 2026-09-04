package speedapp

import (
	"context"
	"errors"
	"fmt"
	stdhttp "net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/login"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
)

func TestLoginRequestAndTokenCache(t *testing.T) {
	t.Parallel()
	var logins atomic.Int64
	doer := &scriptDoer{handler: func(req *stdhttp.Request, body []byte) (*stdhttp.Response, error) {
		switch req.URL.Path {
		case "/api/login":
			logins.Add(1)
			if req.Method != stdhttp.MethodPost || req.Header.Get("Content-Type") != "application/json" {
				t.Errorf("login request = %s content-type %q", req.Method, req.Header.Get("Content-Type"))
			}
			want := `{"username":"` + testEmail + `","password":"` + testPassword + `"}`
			if string(body) != want {
				t.Errorf("login body = %s, want %s", body, want)
			}
			return jsonResponse(stdhttp.StatusOK, `{"token":"`+testToken+`"}`), nil
		case "/api/torrent":
			if req.Header.Get("Authorization") != "Bearer "+testToken {
				t.Errorf("Authorization = %q", req.Header.Get("Authorization"))
			}
			return jsonResponse(stdhttp.StatusOK, `[]`), nil
		default:
			return nil, fmt.Errorf("unexpected path %s", req.URL.Path)
		}
	}}
	d := testDriver(t, doer)
	for range 2 {
		if _, err := d.Search(t.Context(), search.Query{}); err != nil {
			t.Fatalf("Search: %v", err)
		}
	}
	if got := logins.Load(); got != 1 {
		t.Errorf("logins = %d, want 1", got)
	}
}

func TestLoginResponseClassification(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		body     string
		want     error
		contains string
	}{
		{name: "malformed JSON", body: `{`, want: search.ErrParseError, contains: "decode login response"},
		{name: "missing token", body: `{}`, want: login.ErrLoginFailed, contains: "did not contain a token"},
		{name: "empty token", body: `{"token":"  "}`, want: login.ErrLoginFailed, contains: "did not contain a token"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			d := testDriver(t, &scriptDoer{handler: func(*stdhttp.Request, []byte) (*stdhttp.Response, error) {
				return jsonResponse(stdhttp.StatusOK, tt.body), nil
			}})
			err := d.Test(t.Context())
			if !errors.Is(err, tt.want) || !strings.Contains(err.Error(), tt.contains) {
				t.Fatalf("err = %v, want %v containing %q", err, tt.want, tt.contains)
			}
		})
	}
}

func TestRefreshLoginMessageScrubsAllSecrets(t *testing.T) {
	t.Parallel()
	var logins atomic.Int64
	doer := &scriptDoer{handler: func(req *stdhttp.Request, _ []byte) (*stdhttp.Response, error) {
		switch req.URL.Path {
		case "/api/login":
			if logins.Add(1) == 1 {
				return jsonResponse(stdhttp.StatusOK, `{"token":"`+testToken+`"}`), nil
			}
			message := fmt.Sprintf(`{"message":"email=%s password=%s token=%s"}`, testEmail, testPassword, testToken)
			return jsonResponse(stdhttp.StatusOK, message), nil
		case "/api/torrent":
			return jsonResponse(stdhttp.StatusUnauthorized, `{}`), nil
		default:
			return nil, fmt.Errorf("unexpected path %s", req.URL.Path)
		}
	}}
	err := func() error {
		_, err := testDriver(t, doer).Search(t.Context(), search.Query{})
		return err
	}()
	if !errors.Is(err, login.ErrLoginFailed) {
		t.Fatalf("err = %v, want login.ErrLoginFailed", err)
	}
	for _, secret := range []string{testEmail, testPassword, testToken} {
		if strings.Contains(err.Error(), secret) {
			t.Errorf("error leaked %q: %v", secret, err)
		}
	}
	if strings.Count(err.Error(), "[redacted]") < 3 {
		t.Errorf("error did not show all redactions: %v", err)
	}
}

func TestSearchBodyErrorScrubsRuntimeToken(t *testing.T) {
	t.Parallel()
	doer := &scriptDoer{handler: func(req *stdhttp.Request, _ []byte) (*stdhttp.Response, error) {
		if req.URL.Path == "/api/login" {
			return jsonResponse(stdhttp.StatusOK, `{"token":"`+testToken+`"}`), nil
		}
		body := rowsJSON(t, apiRow{ID: 1, CreatedAt: testToken})
		return jsonResponse(stdhttp.StatusOK, body), nil
	}}
	_, err := testDriver(t, doer).Search(t.Context(), search.Query{})
	if !errors.Is(err, search.ErrParseError) {
		t.Fatalf("err = %v, want search.ErrParseError", err)
	}
	if strings.Contains(err.Error(), testToken) {
		t.Errorf("parse error leaked runtime token: %v", err)
	}
}

func TestSpeedAppStatusDialect(t *testing.T) {
	t.Parallel()
	tests := []struct {
		status   int
		wantAuth bool
		wantRate bool
	}{
		{status: stdhttp.StatusUnauthorized, wantAuth: true},
		{status: stdhttp.StatusForbidden},
		{status: stdhttp.StatusTooManyRequests, wantRate: true},
		{status: stdhttp.StatusServiceUnavailable, wantRate: true},
	}
	for _, tt := range tests {
		t.Run(stdhttp.StatusText(tt.status), func(t *testing.T) {
			t.Parallel()
			d := testDriver(t, &scriptDoer{handler: func(*stdhttp.Request, []byte) (*stdhttp.Response, error) {
				return jsonResponse(tt.status, `{}`), nil
			}})
			_, err := d.doBearerGET(t.Context(), d.BaseURL+"api/torrent", "application/json", false, tokenVersion{value: testToken, generation: 1})
			if err == nil {
				t.Fatal("want status error")
			}
			if errors.Is(err, login.ErrLoginFailed) != tt.wantAuth {
				t.Errorf("auth classification = %v, want %v: %v", errors.Is(err, login.ErrLoginFailed), tt.wantAuth, err)
			}
			var rate *search.RateLimitedError
			if errors.As(err, &rate) != tt.wantRate {
				t.Errorf("rate classification = %v, want %v: %v", errors.As(err, &rate), tt.wantRate, err)
			}
		})
	}
}

func TestOne401RetryOnly(t *testing.T) {
	t.Parallel()
	var logins, searches atomic.Int64
	doer := &scriptDoer{handler: func(req *stdhttp.Request, _ []byte) (*stdhttp.Response, error) {
		if req.URL.Path == "/api/login" {
			if logins.Add(1) == 1 {
				return jsonResponse(stdhttp.StatusOK, `{"token":"`+testToken+`"}`), nil
			}
			return jsonResponse(stdhttp.StatusOK, `{"token":"`+nextToken+`"}`), nil
		}
		searches.Add(1)
		return jsonResponse(stdhttp.StatusUnauthorized, `{}`), nil
	}}
	_, err := testDriver(t, doer).Search(t.Context(), search.Query{})
	if !errors.Is(err, login.ErrLoginFailed) {
		t.Fatalf("err = %v, want login.ErrLoginFailed", err)
	}
	if logins.Load() != 2 || searches.Load() != 2 {
		t.Errorf("logins/searches = %d/%d, want 2/2", logins.Load(), searches.Load())
	}
}

func TestStale401ReusesNewerGeneration(t *testing.T) {
	t.Parallel()
	doer := &scriptDoer{}
	d := testDriver(t, doer)
	seedDriverToken(d, nextToken, 2)
	got, err := d.tokenFor(t.Context(), &tokenVersion{value: testToken, generation: 1})
	if err != nil {
		t.Fatalf("tokenFor: %v", err)
	}
	if got.value != nextToken || got.generation != 2 {
		t.Errorf("token = %+v, want generation 2", got)
	}
	if len(doer.records()) != 0 {
		t.Errorf("stale 401 caused %d duplicate login(s)", len(doer.records()))
	}
}

func TestConcurrentFirstUseCoalescesLogin(t *testing.T) {
	t.Parallel()
	loginStarted := make(chan struct{})
	releaseLogin := make(chan struct{})
	var once sync.Once
	var logins atomic.Int64
	d := testDriver(t, &scriptDoer{handler: func(*stdhttp.Request, []byte) (*stdhttp.Response, error) {
		logins.Add(1)
		once.Do(func() { close(loginStarted) })
		<-releaseLogin
		return jsonResponse(stdhttp.StatusOK, `{"token":"`+testToken+`"}`), nil
	}})

	const callers = 12
	errs := make(chan error, callers)
	for range callers {
		go func() { errs <- d.Test(t.Context()) }()
	}
	<-loginStarted
	close(releaseLogin)
	for range callers {
		if err := <-errs; err != nil {
			t.Errorf("Test: %v", err)
		}
	}
	if got := logins.Load(); got != 1 {
		t.Errorf("logins = %d, want 1", got)
	}
}

func TestConcurrent401CoalescesRefresh(t *testing.T) {
	t.Parallel()
	const callers = 10
	allRejected := make(chan struct{})
	var rejected, refreshed, logins atomic.Int64
	var closeOnce sync.Once
	doer := &scriptDoer{handler: func(req *stdhttp.Request, _ []byte) (*stdhttp.Response, error) {
		if req.URL.Path == "/api/login" {
			logins.Add(1)
			return jsonResponse(stdhttp.StatusOK, `{"token":"`+nextToken+`"}`), nil
		}
		switch req.Header.Get("Authorization") {
		case "Bearer " + testToken:
			if rejected.Add(1) == callers {
				closeOnce.Do(func() { close(allRejected) })
			}
			<-allRejected
			return jsonResponse(stdhttp.StatusUnauthorized, `{}`), nil
		case "Bearer " + nextToken:
			refreshed.Add(1)
			return jsonResponse(stdhttp.StatusOK, `[]`), nil
		default:
			return nil, fmt.Errorf("unexpected Authorization %q", req.Header.Get("Authorization"))
		}
	}}
	d := testDriver(t, doer)
	seedDriverToken(d, testToken, 1)
	errs := make(chan error, callers)
	for range callers {
		go func() {
			_, err := d.Search(t.Context(), search.Query{Limit: 1})
			errs <- err
		}()
	}
	for range callers {
		if err := <-errs; err != nil {
			t.Errorf("Search: %v", err)
		}
	}
	if logins.Load() != 1 || rejected.Load() != callers || refreshed.Load() != callers {
		t.Errorf("logins/old/new = %d/%d/%d, want 1/%d/%d", logins.Load(), rejected.Load(), refreshed.Load(), callers, callers)
	}
}

func TestCanceledWaiterDoesNotCancelRefresh(t *testing.T) {
	t.Parallel()
	loginStarted := make(chan struct{})
	releaseLogin := make(chan struct{})
	var once sync.Once
	d := testDriver(t, &scriptDoer{handler: func(*stdhttp.Request, []byte) (*stdhttp.Response, error) {
		once.Do(func() { close(loginStarted) })
		<-releaseLogin
		return jsonResponse(stdhttp.StatusOK, `{"token":"`+testToken+`"}`), nil
	}})

	leader := make(chan error, 1)
	go func() { leader <- d.Test(t.Context()) }()
	<-loginStarted
	waiterCtx, cancel := context.WithCancel(t.Context())
	cancel()
	waiter := make(chan error, 1)
	go func() { waiter <- d.Test(waiterCtx) }()
	select {
	case err := <-waiter:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("waiter error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled waiter remained blocked")
	}
	close(releaseLogin)
	if err := <-leader; err != nil {
		t.Fatalf("leader: %v", err)
	}
	if err := d.Test(t.Context()); err != nil {
		t.Fatalf("cached Test after waiter cancellation: %v", err)
	}
}

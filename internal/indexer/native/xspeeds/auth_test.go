package xspeeds

import (
	"context"
	"errors"
	"fmt"
	stdhttp "net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/login"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
)

func TestManagedLoginRequestAndPersistence(t *testing.T) {
	var persistedName, persistedValue string
	handler := stdhttp.HandlerFunc(func(writer stdhttp.ResponseWriter, request *stdhttp.Request) {
		switch request.URL.Path {
		case "/login.php":
			setTestCookie(writer, "landing", "synthetic-landing")
		case "/takelogin.php":
			assertLoginRequest(t, request)
			setTestCookie(writer, "session", "synthetic-session")
			_, _ = writer.Write(readFixture(t, "login_success.html"))
		case "/browse.php":
			if cookie, err := request.Cookie("session"); err != nil || cookie.Value != "synthetic-session" {
				t.Errorf("browse session cookie = %v, %v", cookie, err)
			}
			_, _ = writer.Write(readFixture(t, "browse_empty.html"))
		default:
			stdhttp.NotFound(writer, request)
		}
	})
	driver, _ := newTestServerDriver(t, handler, testConfig(), func(_ context.Context, name, value string) error {
		persistedName, persistedValue = name, value
		return nil
	})

	if _, err := driver.Search(t.Context(), search.Query{}); err != nil {
		t.Fatalf("Search: %v", err)
	}
	if persistedName != "cookie" || !strings.Contains(persistedValue, "session=synthetic-session") {
		t.Errorf("persisted = %q %q", persistedName, persistedValue)
	}
	if session := driver.sessionSnapshot(); session.generation != 1 || session.cookie != persistedValue {
		t.Errorf("session = %+v, want published persisted cookie", session)
	}
}

func TestLoginErrorSelectors(t *testing.T) {
	tests := []struct {
		name    string
		fixture string
		want    string
	}{
		{name: "table", fixture: "login_error_table.html", want: "Invalid username or password"},
		{name: "notification", fixture: "login_error_notification.html", want: "Account temporarily locked"},
		{name: "generic", fixture: "browse_empty.html", want: "Unknown error message, please report."},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			driver, _ := newTestServerDriver(t, loginFailureHandler(t, test.fixture), testConfig(), nil)
			_, err := driver.Search(t.Context(), search.Query{})
			if !errors.Is(err, login.ErrLoginFailed) || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Search error = %v, want login failure containing %q", err, test.want)
			}
		})
	}
}

func TestLoginRequiresNonemptyCookie(t *testing.T) {
	handler := stdhttp.HandlerFunc(func(writer stdhttp.ResponseWriter, request *stdhttp.Request) {
		if request.URL.Path == "/takelogin.php" {
			_, _ = writer.Write(readFixture(t, "login_success.html"))
			return
		}
		_, _ = writer.Write(readFixture(t, "browse_empty.html"))
	})
	driver, _ := newTestServerDriver(t, handler, testConfig(), nil)
	_, err := driver.Search(t.Context(), search.Query{})
	if !errors.Is(err, login.ErrLoginFailed) || !strings.Contains(err.Error(), "no usable session cookie") {
		t.Fatalf("Search error = %v, want missing-cookie login failure", err)
	}
}

func TestReplaceJarCookiesPathPrefixed(t *testing.T) {
	cfg := testConfig()
	cfg["cookie"] = testOldCookie
	driver := newTestDriver(t, "https://xspeeds.example/root/", cfg, nil, nil)
	//nolint:gosec // G124: synthetic jar state must exercise a root-scoped non-Secure cookie.
	driver.jar.SetCookies(driver.cookieURL, []*stdhttp.Cookie{{
		Name:  "session",
		Value: "synthetic-xspeeds-root-cookie",
		Path:  "/",
	}})
	if got := len(driver.jar.Cookies(driver.cookieURL)); got != 2 {
		t.Fatalf("visible cookies before clear = %d, want root and path-prefixed entries", got)
	}

	driver.replaceJarCookies("")
	if got := serializeCookies(driver.jar.Cookies(driver.cookieURL)); got != "" {
		t.Fatalf("jar after clear = %q, want empty", got)
	}
	driver.replaceJarCookies("session=synthetic-xspeeds-new-cookie")
	if got := serializeCookies(driver.jar.Cookies(driver.cookieURL)); got != "session=synthetic-xspeeds-new-cookie" {
		t.Errorf("jar after replacement = %q", got)
	}
}

func TestLoginPageDetection(t *testing.T) {
	driver := newTestDriver(t, "https://xspeeds.example/root/", testConfig(), nil, nil)
	tests := []struct {
		name string
		body string
		want bool
	}{
		{name: "login form", body: `<form action="takelogin.php"></form>`, want: true},
		{name: "plain logout mention", body: `<script>const path = "logout.php"</script>`, want: true},
		{name: "cross-host logout link", body: `<a href="https://evil.example/logout.php">Logout</a>`, want: true},
		{name: "relative same-host logout link", body: `<a href="logout.php">Logout</a>`},
		{name: "prefixed same-host logout link", body: `<a href="https://xspeeds.example/root/logout.php">Logout</a>`},
		{name: "root same-host logout link", body: `<a href="/logout.php">Logout</a>`},
		{name: "ordinary empty browse", body: `<table id="sortabletable"><tbody></tbody></table>`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := driver.isLoginPage([]byte(test.body)); got != test.want {
				t.Errorf("isLoginPage() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestAuthenticatedOperationsRunConcurrently(t *testing.T) {
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	handler := stdhttp.HandlerFunc(func(writer stdhttp.ResponseWriter, request *stdhttp.Request) {
		if request.URL.Path != "/browse.php" {
			stdhttp.NotFound(writer, request)
			return
		}
		entered <- struct{}{}
		<-release
		_, _ = writer.Write(readFixture(t, "browse_empty.html"))
	})
	cfg := testConfig()
	cfg["cookie"] = testOldCookie
	driver, _ := newTestServerDriver(t, handler, cfg, nil)

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	errs := make([]error, 2)
	var wait sync.WaitGroup
	for index := range errs {
		wait.Go(func() {
			_, errs[index] = driver.Search(ctx, search.Query{})
		})
	}
	concurrent := true
waitForBrowses:
	for range errs {
		select {
		case <-entered:
		case <-ctx.Done():
			concurrent = false
			break waitForBrowses
		}
	}
	close(release)
	wait.Wait()
	if !concurrent {
		t.Fatal("authenticated browse operations were serialized")
	}
	for _, err := range errs {
		if err != nil {
			t.Errorf("Search error = %v", err)
		}
	}
}

func TestConcurrentInitialLogin(t *testing.T) {
	tests := []struct {
		name        string
		loginOK     bool
		wantBrowses int64
	}{
		{name: "success", loginOK: true, wantBrowses: 8},
		{name: "failure shared", loginOK: false, wantBrowses: 0},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var logins, browses atomic.Int64
			handler := concurrentHandler(t, &logins, &browses, false, test.loginOK)
			driver, _ := newTestServerDriver(t, handler, testConfig(), nil)
			errs := runConcurrentSearches(t, driver, 8)
			for _, err := range errs {
				if test.loginOK && err != nil {
					t.Errorf("Search error = %v", err)
				}
				if !test.loginOK && !errors.Is(err, login.ErrLoginFailed) {
					t.Errorf("Search error = %v, want login failure", err)
				}
			}
			if logins.Load() != 1 || browses.Load() != test.wantBrowses {
				t.Errorf("requests = %d logins, %d browses; want 1, %d", logins.Load(), browses.Load(), test.wantBrowses)
			}
			if !test.loginOK {
				if _, err := driver.Search(t.Context(), search.Query{}); !errors.Is(err, login.ErrLoginFailed) {
					t.Errorf("remembered Search error = %v, want login failure", err)
				}
				if logins.Load() != 1 {
					t.Errorf("remembered failure retried login: requests = %d, want 1", logins.Load())
				}
			}
		})
	}
}

func TestConcurrentStaleSessionRenewal(t *testing.T) {
	tests := []struct {
		name        string
		loginOK     bool
		wantBrowses int64
	}{
		{name: "success", loginOK: true, wantBrowses: 16},
		{name: "failure shared", loginOK: false, wantBrowses: 8},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var logins, browses atomic.Int64
			handler := concurrentHandler(t, &logins, &browses, true, test.loginOK)
			cfg := testConfig()
			cfg["cookie"] = testOldCookie
			driver, _ := newTestServerDriver(t, handler, cfg, nil)
			errs := runConcurrentSearches(t, driver, 8)
			for _, err := range errs {
				if test.loginOK && err != nil {
					t.Errorf("Search error = %v", err)
				}
				if !test.loginOK && !errors.Is(err, login.ErrLoginFailed) {
					t.Errorf("Search error = %v, want login failure", err)
				}
			}
			if logins.Load() != 1 || browses.Load() != test.wantBrowses {
				t.Errorf("requests = %d logins, %d browses; want 1, %d", logins.Load(), browses.Load(), test.wantBrowses)
			}
			if !test.loginOK {
				if _, err := driver.Search(t.Context(), search.Query{}); !errors.Is(err, login.ErrLoginFailed) {
					t.Errorf("remembered Search error = %v, want login failure", err)
				}
				if logins.Load() != 1 {
					t.Errorf("remembered renewal failure retried login: requests = %d, want 1", logins.Load())
				}
			}
		})
	}
}

func TestCanceledLoginGateWait(t *testing.T) {
	driver := newTestDriver(t, "https://xspeeds.example/", testConfig(), nil, nil)
	if err := driver.loginGate.Acquire(t.Context(), 1); err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	defer driver.loginGate.Release(1)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := driver.Search(ctx, search.Query{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Search error = %v, want context.Canceled", err)
	}
}

func TestPersistenceFailureRollsBack(t *testing.T) {
	var browseCount atomic.Int64
	handler := stdhttp.HandlerFunc(func(writer stdhttp.ResponseWriter, request *stdhttp.Request) {
		switch request.URL.Path {
		case "/browse.php":
			browseCount.Add(1)
			writer.WriteHeader(stdhttp.StatusForbidden)
		case "/login.php":
			setTestCookie(writer, "landing", "synthetic-new-landing")
		case "/takelogin.php":
			setTestCookie(writer, "session", "synthetic-new-cookie")
			_, _ = writer.Write(readFixture(t, "login_success.html"))
		}
	})
	cfg := testConfig()
	cfg["cookie"] = testOldCookie
	driver, _ := newTestServerDriver(t, handler, cfg, func(_ context.Context, _, value string) error {
		return fmt.Errorf("failed to persist %s for %s", value, testUsername)
	})
	before := driver.sessionSnapshot()
	_, err := driver.Search(t.Context(), search.Query{})
	if !errors.Is(err, login.ErrLoginFailed) {
		t.Fatalf("Search error = %v, want login failure", err)
	}
	for _, secret := range []string{testUsername, "synthetic-new-cookie", "synthetic-new-landing", "synthetic-xspeeds-old-cookie"} {
		if strings.Contains(err.Error(), secret) {
			t.Errorf("error leaked %q: %v", secret, err)
		}
	}
	if after := driver.sessionSnapshot(); after != before {
		t.Errorf("session after failure = %+v, want %+v", after, before)
	}
	if got := serializeCookies(driver.jar.Cookies(driver.cookieURL)); got != testOldCookie {
		t.Errorf("jar after rollback = %q, want %q", got, testOldCookie)
	}
	if browseCount.Load() != 1 {
		t.Errorf("browse count = %d, want 1", browseCount.Load())
	}
}

func assertLoginRequest(t *testing.T, request *stdhttp.Request) {
	t.Helper()
	if request.Method != stdhttp.MethodPost || request.Header.Get("Content-Type") != "application/x-www-form-urlencoded" {
		t.Errorf("login request = %s Content-Type %q", request.Method, request.Header.Get("Content-Type"))
	}
	if request.Header.Get("Referer") != "http://"+request.Host+"/login.php" {
		t.Errorf("Referer = %q", request.Header.Get("Referer"))
	}
	if err := request.ParseForm(); err != nil {
		t.Fatalf("ParseForm: %v", err)
	}
	if !valuesEqual(request.PostForm, url.Values{"username": {testUsername}, "password": {testPassword}}) {
		t.Errorf("login form keys/values differ")
	}
	if cookie, err := request.Cookie("landing"); err != nil || cookie.Value != "synthetic-landing" {
		t.Errorf("landing cookie = %v, %v", cookie, err)
	}
}

func loginFailureHandler(t *testing.T, fixture string) stdhttp.Handler {
	t.Helper()
	return stdhttp.HandlerFunc(func(writer stdhttp.ResponseWriter, request *stdhttp.Request) {
		if request.URL.Path == "/takelogin.php" {
			_, _ = writer.Write(readFixture(t, fixture))
			return
		}
		_, _ = writer.Write(readFixture(t, "browse_empty.html"))
	})
}

func concurrentHandler(t *testing.T, logins, browses *atomic.Int64, stale, loginOK bool) stdhttp.Handler {
	t.Helper()
	return stdhttp.HandlerFunc(func(writer stdhttp.ResponseWriter, request *stdhttp.Request) {
		switch request.URL.Path {
		case "/login.php":
			return
		case "/takelogin.php":
			logins.Add(1)
			if !loginOK {
				_, _ = writer.Write(readFixture(t, "login_error_table.html"))
				return
			}
			setTestCookie(writer, "session", "synthetic-fresh-cookie")
			_, _ = writer.Write(readFixture(t, "login_success.html"))
		case "/browse.php":
			browses.Add(1)
			cookie, _ := request.Cookie("session")
			if stale && (cookie == nil || cookie.Value != "synthetic-fresh-cookie") {
				writer.WriteHeader(stdhttp.StatusForbidden)
				return
			}
			_, _ = writer.Write(readFixture(t, "browse_empty.html"))
		}
	})
}

func runConcurrentSearches(t *testing.T, driver *driver, count int) []error {
	t.Helper()
	if err := driver.loginGate.Acquire(t.Context(), 1); err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	entered := make(chan struct{}, count)
	errs := make([]error, count)
	var wait sync.WaitGroup
	for index := range count {
		wait.Go(func() {
			ctx := &gateSignalContext{done: make(chan struct{}), entered: entered}
			errs[index] = searchOnce(ctx, driver)
		})
	}
	for range count {
		<-entered
	}
	driver.loginGate.Release(1)
	wait.Wait()
	return errs
}

func searchOnce(ctx context.Context, driver *driver) error {
	_, err := driver.Search(ctx, search.Query{})
	return err
}

type gateSignalContext struct {
	once    sync.Once
	done    chan struct{}
	entered chan<- struct{}
}

func (*gateSignalContext) Deadline() (time.Time, bool) { return time.Time{}, false }

func (ctx *gateSignalContext) Done() <-chan struct{} {
	ctx.once.Do(func() { ctx.entered <- struct{}{} })
	return ctx.done
}

func (*gateSignalContext) Err() error { return nil }

func (*gateSignalContext) Value(any) any { return nil }

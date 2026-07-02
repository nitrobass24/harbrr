package login

import (
	"errors"
	stdhttp "net/http"
	"net/http/cookiejar"
	"net/url"
	"testing"

	"golang.org/x/net/publicsuffix"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
)

const baseURL = "https://tracker.example"

func scalar(v string) loader.Scalar { return loader.Scalar{Value: v, Set: true} }

// newExec builds an executor wired to the replay transport with a real
// publicsuffix cookie jar (so cookie capture across steps is exercised offline).
func newExec(t *testing.T, rt *replayTransport, cfg map[string]string) *Executor {
	t.Helper()
	jar, err := cookiejar.New(&cookiejar.Options{PublicSuffixList: publicsuffix.List})
	if err != nil {
		t.Fatalf("cookiejar.New: %v", err)
	}
	return New(
		WithClient(rt),
		WithJar(jar),
		WithBaseURL(baseURL),
		WithConfig(cfg),
	)
}

func TestLoginForm(t *testing.T) {
	t.Parallel()

	rt := newReplay(
		t,
		// 1. GET landing page -> form with CSRF token + Set-Cookie.
		step{
			wantMethod: stdhttp.MethodGet,
			wantPath:   "/login.php",
			respHeader: setCookieHeader("PHPSESSID=sess-from-landing; Path=/"),
			bodyFile:   "login_page.html",
		},
		// 2. POST credentials + CSRF -> 302 + session cookie.
		step{
			wantMethod: stdhttp.MethodPost,
			wantPath:   "/takelogin.php",
			status:     stdhttp.StatusFound,
			respHeader: setCookieHeader("session=authed-token; Path=/"),
			bodyFile:   "logged_in.html",
		},
	)
	def := &loader.Definition{Login: &loader.Login{
		Method: "form",
		Path:   "login.php",
		Form:   "form#loginform",
		Inputs: map[string]loader.Scalar{
			"username": scalar("{{ .Config.username }}"),
			"password": scalar("{{ .Config.password }}"),
		},
		SelectorInputs: map[string]loader.SelectorBlock{
			"csrf_token": {Selector: "input[name=\"csrf_token\"]", Attribute: "value"},
		},
		Error: []loader.ErrorBlock{{Selector: "form#loginform .warning"}},
		Test:  &loader.PageTestBlock{Path: "index.php", Selector: "a[href^=\"/logout.php\"]"},
	}}

	e := newExec(t, rt, map[string]string{
		"username": "alice",
		"password": "s3cr3t-pass",
	})

	if err := e.Login(t.Context(), def); err != nil {
		t.Fatalf("Login: %v", err)
	}

	// The POST (capture index 1) must carry the CSRF token extracted from the
	// landing page AND the rendered credentials.
	post := rt.capture(1)
	if got := post.form.Get("csrf_token"); got != "CSRF-TOKEN-FROM-PAGE-9988" {
		t.Errorf("posted csrf_token = %q, want extracted token", got)
	}
	if got := post.form.Get("username"); got != "alice" {
		t.Errorf("posted username = %q, want alice", got)
	}
	if got := post.form.Get("password"); got != "s3cr3t-pass" {
		t.Errorf("posted password = %q, want rendered password", got)
	}

	// The session cookie set on the landing GET must have been carried into the
	// POST request (jar persistence).
	if !hasCookie(post.cookies, "PHPSESSID", "sess-from-landing") {
		t.Errorf("landing Set-Cookie not carried into POST; got %v", cookieNames(post.cookies))
	}

	// After login, the authed cookie is in the jar.
	if !jarHasCookie(t, e, "session") {
		t.Error("jar missing session cookie after login")
	}
}

func TestLoginFormSeedsStaticCookies(t *testing.T) {
	t.Parallel()

	rt := newReplay(
		t,
		// 1. GET landing page. The static Login.Cookies must already be on it.
		step{wantMethod: stdhttp.MethodGet, wantPath: "/login.php", bodyFile: "login_page.html"},
		// 2. POST credentials; the static cookies must persist onto the POST too.
		step{wantMethod: stdhttp.MethodPost, wantPath: "/takelogin.php", bodyFile: "logged_in.html"},
	)
	def := &loader.Definition{Login: &loader.Login{
		Method:  "form",
		Path:    "login.php",
		Form:    "form#loginform",
		Cookies: []string{"JAVA=OK"}, // avoid jscheck redirect (corpus pattern)
		Inputs: map[string]loader.Scalar{
			"username": scalar("{{ .Config.username }}"),
		},
		SubmitPath: "takelogin.php",
	}}
	e := newExec(t, rt, map[string]string{"username": "alice"})

	if err := e.Login(t.Context(), def); err != nil {
		t.Fatalf("Login: %v", err)
	}

	landing := rt.capture(0)
	if !hasCookie(landing.cookies, "JAVA", "OK") {
		t.Errorf("static cookie not sent on landing GET; got %v", cookieNames(landing.cookies))
	}
	post := rt.capture(1)
	if !hasCookie(post.cookies, "JAVA", "OK") {
		t.Errorf("static cookie not carried into POST; got %v", cookieNames(post.cookies))
	}
}

func TestLoginPost(t *testing.T) {
	t.Parallel()

	rt := newReplay(
		t,
		step{
			wantMethod: stdhttp.MethodPost,
			wantPath:   "/takelogin.php",
			respHeader: setCookieHeader("session=ok; Path=/"),
			bodyFile:   "logged_in.html",
		},
	)
	def := &loader.Definition{Login: &loader.Login{
		Method: "post",
		Path:   "takelogin.php",
		Inputs: map[string]loader.Scalar{
			"username": scalar("{{ .Config.username }}"),
			"password": scalar("{{ .Config.password }}"),
			"returnto": scalar("/browse.php"),
		},
		Error: []loader.ErrorBlock{{Selector: "div.warning"}},
	}}
	e := newExec(t, rt, map[string]string{"username": "bob", "password": "pw-bob"})

	if err := e.Login(t.Context(), def); err != nil {
		t.Fatalf("Login: %v", err)
	}
	post := rt.capture(0)
	if got := post.form.Get("username"); got != "bob" {
		t.Errorf("username = %q", got)
	}
	if got := post.form.Get("returnto"); got != "/browse.php" {
		t.Errorf("returnto = %q", got)
	}
	if ct := post.headers.Get("Content-Type"); ct != "application/x-www-form-urlencoded" {
		t.Errorf("Content-Type = %q", ct)
	}
}

func TestLoginGet(t *testing.T) {
	t.Parallel()

	rt := newReplay(
		t,
		step{wantMethod: stdhttp.MethodGet, wantPath: "/api/search", bodyFile: "api_ok.html"},
	)
	def := &loader.Definition{Login: &loader.Login{
		Method: "get",
		Path:   "api/search",
		Inputs: map[string]loader.Scalar{
			"apikey": scalar("{{ .Config.apikey }}"),
			"q":      scalar("test"),
		},
		Error: []loader.ErrorBlock{{Selector: ":root:contains(\"Unauthorized\")"}},
	}}
	e := newExec(t, rt, map[string]string{"apikey": "API-KEY-123"})

	if err := e.Login(t.Context(), def); err != nil {
		t.Fatalf("Login: %v", err)
	}
	get := rt.capture(0)
	if got := get.query.Get("apikey"); got != "API-KEY-123" {
		t.Errorf("query apikey = %q", got)
	}
	if got := get.query.Get("q"); got != "test" {
		t.Errorf("query q = %q", got)
	}
}

func TestLoginCookieManual(t *testing.T) {
	t.Parallel()

	// Cookie method does NO login round-trip; only the Test GET happens (here via
	// a separate CheckTest call).
	rt := newReplay(
		t,
		step{wantMethod: stdhttp.MethodGet, wantPath: "/index.php", bodyFile: "logged_in.html"},
	)
	def := &loader.Definition{Login: &loader.Login{
		Method: "cookie",
		Inputs: map[string]loader.Scalar{"cookie": scalar("{{ .Config.cookie }}")},
		Test:   &loader.PageTestBlock{Path: "index.php", Selector: "a[href^=\"/logout.php\"]"},
	}}
	e := newExec(t, rt, map[string]string{"cookie": "uid=42; pass=COOKIE-SECRET-VAL"})

	if err := e.Login(t.Context(), def); err != nil {
		t.Fatalf("Login: %v", err)
	}
	// Login made no HTTP request.
	if n := rt.requestCount(); n != 0 {
		t.Fatalf("cookie login made %d requests, want 0", n)
	}
	// Jar was seeded with the manual cookies.
	if !jarHasCookie(t, e, "uid") || !jarHasCookie(t, e, "pass") {
		t.Errorf("jar not seeded with manual cookies: %v", jarNames(t, e))
	}

	// CheckTest now hits index.php and finds the logout link.
	ok, err := e.CheckTest(t.Context(), def)
	if err != nil {
		t.Fatalf("CheckTest: %v", err)
	}
	if !ok {
		t.Error("CheckTest = false, want true after manual cookie seed")
	}
	// The seeded cookie must have been sent on the test request.
	test := rt.capture(0)
	if !hasCookie(test.cookies, "uid", "42") {
		t.Errorf("seeded cookie not sent on test request: %v", cookieNames(test.cookies))
	}
}

func TestCheckTestBeforeAndAfter(t *testing.T) {
	t.Parallel()

	rt := newReplay(
		t,
		// Before login: logged-out page, selector absent -> false.
		step{wantMethod: stdhttp.MethodGet, wantPath: "/index.php", bodyFile: "logged_out.html"},
		// After login: logged-in page, selector present -> true.
		step{wantMethod: stdhttp.MethodGet, wantPath: "/index.php", bodyFile: "logged_in.html"},
	)
	def := &loader.Definition{Login: &loader.Login{
		Method: "post",
		Path:   "takelogin.php",
		Test:   &loader.PageTestBlock{Path: "index.php", Selector: "a[href^=\"/logout.php\"]"},
	}}
	e := newExec(t, rt, nil)

	before, err := e.CheckTest(t.Context(), def)
	if err != nil {
		t.Fatalf("CheckTest before: %v", err)
	}
	if before {
		t.Error("CheckTest before login = true, want false")
	}
	after, err := e.CheckTest(t.Context(), def)
	if err != nil {
		t.Fatalf("CheckTest after: %v", err)
	}
	if !after {
		t.Error("CheckTest after login = false, want true")
	}
}

func TestEnsureLoggedIn(t *testing.T) {
	t.Parallel()

	rt := newReplay(
		t,
		// CheckTest: logged out -> login needed.
		step{wantMethod: stdhttp.MethodGet, wantPath: "/index.php", bodyFile: "logged_out.html"},
		// Login POST.
		step{wantMethod: stdhttp.MethodPost, wantPath: "/takelogin.php", respHeader: setCookieHeader("session=x; Path=/"), bodyFile: "logged_in.html"},
	)
	def := &loader.Definition{Login: &loader.Login{
		Method: "post",
		Path:   "takelogin.php",
		Inputs: map[string]loader.Scalar{"username": scalar("{{ .Config.username }}")},
		Test:   &loader.PageTestBlock{Path: "index.php", Selector: "a[href^=\"/logout.php\"]"},
	}}
	e := newExec(t, rt, map[string]string{"username": "carol"})

	if err := e.EnsureLoggedIn(t.Context(), def); err != nil {
		t.Fatalf("EnsureLoggedIn: %v", err)
	}
	if rt.requestCount() != 2 {
		t.Fatalf("EnsureLoggedIn made %d requests, want 2 (test + login)", rt.requestCount())
	}
}

func TestEnsureLoggedInSkipsWhenSessionValid(t *testing.T) {
	t.Parallel()

	rt := newReplay(
		t,
		step{wantMethod: stdhttp.MethodGet, wantPath: "/index.php", bodyFile: "logged_in.html"},
	)
	def := &loader.Definition{Login: &loader.Login{
		Method: "post",
		Path:   "takelogin.php",
		Test:   &loader.PageTestBlock{Path: "index.php", Selector: "a[href^=\"/logout.php\"]"},
	}}
	e := newExec(t, rt, nil)

	if err := e.EnsureLoggedIn(t.Context(), def); err != nil {
		t.Fatalf("EnsureLoggedIn: %v", err)
	}
	if rt.requestCount() != 1 {
		t.Fatalf("EnsureLoggedIn made %d requests, want 1 (test only)", rt.requestCount())
	}
}

func TestUnknownMethod(t *testing.T) {
	t.Parallel()
	rt := newReplay(t)
	def := &loader.Definition{Login: &loader.Login{Method: "telepathy"}}
	e := newExec(t, rt, nil)
	err := e.Login(t.Context(), def)
	if !errors.Is(err, ErrUnknownMethod) {
		t.Fatalf("err = %v, want ErrUnknownMethod", err)
	}
}

func TestCaptchaDetected(t *testing.T) {
	t.Parallel()
	rt := newReplay(t)
	def := &loader.Definition{Login: &loader.Login{
		Method:  "form",
		Path:    "login.php",
		Captcha: &loader.CaptchaBlock{Type: "image", Selector: "img#captcha", Input: "captcha"},
	}}
	e := newExec(t, rt, nil)
	err := e.Login(t.Context(), def)
	if !errors.Is(err, ErrCaptchaRequired) {
		t.Fatalf("err = %v, want ErrCaptchaRequired", err)
	}
	if rt.requestCount() != 0 {
		t.Error("captcha detection must short-circuit before any request")
	}
}

func TestCloudflareDetected(t *testing.T) {
	t.Parallel()
	rt := newReplay(
		t,
		step{wantMethod: stdhttp.MethodGet, wantPath: "/login.php", bodyFile: "cloudflare.html"},
	)
	def := &loader.Definition{Login: &loader.Login{
		Method: "form",
		Path:   "login.php",
		Inputs: map[string]loader.Scalar{"username": scalar("x")},
	}}
	e := newExec(t, rt, nil)
	err := e.Login(t.Context(), def)
	if !errors.Is(err, ErrSolverRequired) {
		t.Fatalf("err = %v, want ErrSolverRequired", err)
	}
}

func TestNoLoginBlock(t *testing.T) {
	t.Parallel()
	rt := newReplay(t)
	e := newExec(t, rt, nil)
	if err := e.Login(t.Context(), &loader.Definition{}); err != nil {
		t.Fatalf("Login with no block: %v", err)
	}
	ok, err := e.CheckTest(t.Context(), &loader.Definition{})
	if err != nil || !ok {
		t.Fatalf("CheckTest with no login = (%v, %v), want (true, nil)", ok, err)
	}
}

func TestSessionExposesJar(t *testing.T) {
	t.Parallel()

	rt := newReplay(
		t,
		step{wantMethod: stdhttp.MethodGet, wantPath: "/index.php", bodyFile: "logged_in.html"},
	)
	def := &loader.Definition{Login: &loader.Login{
		Method: "cookie",
		Inputs: map[string]loader.Scalar{"cookie": scalar("{{ .Config.cookie }}")},
		Test:   &loader.PageTestBlock{Path: "index.php", Selector: "a[href^=\"/logout.php\"]"},
	}}
	e := newExec(t, rt, map[string]string{"cookie": "uid=42"})

	if err := e.Login(t.Context(), def); err != nil {
		t.Fatalf("Login: %v", err)
	}
	sess := e.Session()
	if sess.Jar != e.Jar {
		t.Fatal("Session().Jar is not the executor's jar")
	}
	u, _ := url.Parse(baseURL)
	if len(sess.Jar.Cookies(u)) == 0 {
		t.Error("session jar has no cookies after manual-cookie login")
	}
}

// --- cookie helpers ---

func hasCookie(cs []*stdhttp.Cookie, name, value string) bool {
	for _, c := range cs {
		if c.Name == name && c.Value == value {
			return true
		}
	}
	return false
}

func cookieNames(cs []*stdhttp.Cookie) []string {
	out := make([]string, 0, len(cs))
	for _, c := range cs {
		out = append(out, c.Name)
	}
	return out
}

func jarHasCookie(t *testing.T, e *Executor, name string) bool {
	t.Helper()
	for _, n := range jarNames(t, e) {
		if n == name {
			return true
		}
	}
	return false
}

func jarNames(t *testing.T, e *Executor) []string {
	t.Helper()
	u, err := url.Parse(baseURL)
	if err != nil {
		t.Fatalf("parse baseURL: %v", err)
	}
	cookies := e.Jar.Cookies(u)
	out := make([]string, 0, len(cookies))
	for _, c := range cookies {
		out = append(out, c.Name)
	}
	return out
}

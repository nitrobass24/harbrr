package registry_test

import (
	"context"
	"io"
	stdhttp "net/http"
	"net/http/cookiejar"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/rs/zerolog"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/indexer/registry"
	"github.com/autobrr/harbrr/internal/secrets"
	"github.com/autobrr/harbrr/internal/web/torznabhttp"
)

const (
	xspeedsE2EUsername = "xspeeds-e2e-synthetic-user"
	xspeedsE2EPassword = "XSPEEDS-E2E-SYNTHETIC-PASSWORD"
	xspeedsE2ESession  = "XSPEEDS-E2E-SYNTHETIC-SESSION"
	xspeedsE2ETorrent  = "d8:announce11:fake-tracker4:infod6:lengthi1ee"
)

type xspeedsDoer struct {
	browseBody string

	mu         sync.Mutex
	jar        stdhttp.CookieJar
	requests   []*stdhttp.Request
	loginCalls int
}

func newXSpeedsDoer(t *testing.T, browseBody string) *xspeedsDoer {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar.New: %v", err)
	}
	return &xspeedsDoer{browseBody: browseBody, jar: jar}
}

func (doer *xspeedsDoer) CookieJar() stdhttp.CookieJar {
	doer.mu.Lock()
	defer doer.mu.Unlock()
	return doer.jar
}

func (doer *xspeedsDoer) Do(request *stdhttp.Request) (*stdhttp.Response, error) {
	doer.mu.Lock()
	jar := doer.jar
	for _, cookie := range jar.Cookies(request.URL) {
		request.AddCookie(cookie)
	}
	doer.requests = append(doer.requests, request.Clone(request.Context()))
	doer.mu.Unlock()

	response := doer.response(request)
	jar.SetCookies(request.URL, response.Cookies())
	return response, nil
}

func (doer *xspeedsDoer) response(request *stdhttp.Request) *stdhttp.Response {
	switch request.URL.Path {
	case "/login.php":
		return xspeedsResponse(stdhttp.StatusOK, "", "text/html", "landing=XSPEEDS-E2E-LANDING; Path=/")
	case "/takelogin.php":
		_ = request.ParseForm()
		doer.mu.Lock()
		doer.loginCalls++
		doer.mu.Unlock()
		if request.PostForm.Get("username") != xspeedsE2EUsername || request.PostForm.Get("password") != xspeedsE2EPassword {
			return xspeedsResponse(stdhttp.StatusOK, `<div class="notification-body">bad credentials</div>`, "text/html", "")
		}
		return xspeedsResponse(stdhttp.StatusOK, `<a href="logout.php">Logout</a>`, "text/html", "session="+xspeedsE2ESession+"; Path=/")
	case "/browse.php":
		if !requestHasCookie(request, "session", xspeedsE2ESession) {
			return xspeedsResponse(stdhttp.StatusForbidden, "", "text/html", "")
		}
		return xspeedsResponse(stdhttp.StatusOK, doer.browseBody, "text/html", "")
	case "/download.php":
		if !requestHasCookie(request, "session", xspeedsE2ESession) {
			return xspeedsResponse(stdhttp.StatusForbidden, "", "text/html", "")
		}
		return xspeedsResponse(stdhttp.StatusOK, xspeedsE2ETorrent, "application/x-bittorrent", "")
	default:
		return xspeedsResponse(stdhttp.StatusNotFound, "", "text/plain", "")
	}
}

func xspeedsResponse(status int, body, contentType, setCookie string) *stdhttp.Response {
	header := stdhttp.Header{"Content-Type": []string{contentType}}
	if setCookie != "" {
		header.Set("Set-Cookie", setCookie)
	}
	return &stdhttp.Response{StatusCode: status, Header: header, Body: io.NopCloser(strings.NewReader(body))}
}

func requestHasCookie(request *stdhttp.Request, name, value string) bool {
	cookie, err := request.Cookie(name)
	return err == nil && cookie.Value == value
}

func (doer *xspeedsDoer) clearJar(t *testing.T) {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar.New: %v", err)
	}
	doer.mu.Lock()
	doer.jar = jar
	doer.mu.Unlock()
}

func (doer *xspeedsDoer) snapshot() ([]*stdhttp.Request, int) {
	doer.mu.Lock()
	defer doer.mu.Unlock()
	requests := make([]*stdhttp.Request, len(doer.requests))
	copy(requests, doer.requests)
	return requests, doer.loginCalls
}

func TestXSpeedsEndToEnd(t *testing.T) {
	browse, err := os.ReadFile("../native/xspeeds/testdata/browse_mixed.html")
	if err != nil {
		t.Fatalf("read browse fixture: %v", err)
	}
	doer := newXSpeedsDoer(t, string(browse))
	reg, _ := newRegistry(t, doer)
	ctx := context.Background()
	if _, err := reg.Add(ctx, registry.AddParams{
		Slug:         "xs",
		DefinitionID: "xspeeds",
		BaseURL:      "https://xspeeds.example/",
		Settings: map[string]string{
			"username": xspeedsE2EUsername,
			"password": xspeedsE2EPassword,
		},
	}); err != nil {
		t.Fatalf("Add(xspeeds): %v", err)
	}

	indexer, ok := reg.Indexer(ctx, "xs")
	if !ok {
		t.Fatal("xspeeds indexer should resolve")
	}
	if indexer.NeedsResolver() || !indexer.DownloadNeedsAuth() || !torznabhttp.NeedsDLProxy(indexer) {
		t.Errorf("resolver flags NeedsResolver=%v DownloadNeedsAuth=%v proxy=%v", indexer.NeedsResolver(), indexer.DownloadNeedsAuth(), torznabhttp.NeedsDLProxy(indexer))
	}
	releases, err := indexer.Search(ctx, search.Query{Keywords: "Anime"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(releases) != 4 {
		t.Fatalf("releases = %d, want 4", len(releases))
	}

	keyring, err := secrets.OpenKeyring(secrets.KeyringOptions{EncryptionKey: testHexKey}, zerolog.Nop())
	if err != nil {
		t.Fatalf("OpenKeyring: %v", err)
	}
	rewrite := torznabhttp.NewDLRewriter(keyring, indexer, "http://harbrr.test/api/indexers/xs/dl", "synthetic-api-key")
	sealed, _, ok := rewrite(releases[0].Link, releases[0].Title, releases[0].Categories)
	if !ok || !strings.HasPrefix(sealed, "http://harbrr.test/api/indexers/xs/dl?") || strings.Contains(sealed, "download.php") {
		t.Errorf("sealed link = %q, ok=%v", sealed, ok)
	}

	grab, err := indexer.Grab(ctx, releases[0].Link)
	if err != nil {
		t.Fatalf("Grab: %v", err)
	}
	if string(grab.Body) != xspeedsE2ETorrent || grab.ContentType != "application/x-bittorrent" {
		t.Errorf("Grab = %q %q", grab.ContentType, grab.Body)
	}

	_, views, err := reg.Get(ctx, "xs")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	foundCookie := false
	for _, view := range views {
		if view.Name == "cookie" {
			foundCookie = view.Secret && view.Value == secrets.Redacted
		}
	}
	if !foundCookie {
		t.Error("persisted hidden cookie was not returned as a redacted secret")
	}

	doer.clearJar(t)
	reg.InvalidateAll()
	reused, ok := reg.Indexer(ctx, "xs")
	if !ok {
		t.Fatal("xspeeds indexer should resolve after invalidation")
	}
	if _, err := reused.Search(ctx, search.Query{}); err != nil {
		t.Fatalf("Search with persisted cookie: %v", err)
	}

	requests, loginCalls := doer.snapshot()
	if loginCalls != 1 {
		t.Errorf("login calls = %d, want 1 after persisted-cookie reuse", loginCalls)
	}
	for _, request := range requests {
		raw := request.URL.String()
		for _, secret := range []string{xspeedsE2EUsername, xspeedsE2EPassword, xspeedsE2ESession} {
			if strings.Contains(raw, secret) {
				t.Errorf("credential leaked into request URL %q", raw)
			}
		}
	}
}

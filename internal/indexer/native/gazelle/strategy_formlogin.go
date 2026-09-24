package gazelle

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	stdhttp "net/http"
	"net/url"
	"strings"

	"github.com/PuerkitoBio/goquery"

	apphttp "github.com/autobrr/harbrr/internal/http"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/login"
	"github.com/autobrr/harbrr/internal/indexer/native"
)

// classifyFormLogin is the form-login regime's status dialect handed to Base.Do/
// DoDownload. On top of the shared 401/403 auth codes it treats a redirect as an auth
// failure: an expired session bounces to login.php, and requestContext disables
// redirect following so that 3xx surfaces here instead of being swallowed as login
// HTML. Base.Do therefore returns login.ErrLoginFailed and Search/Grab renew once and
// retry. 429/503 stay rate limits (a RateLimitedError, never a renewal).
var classifyFormLogin = native.ClassifyAuth403.AlsoAuth(
	stdhttp.StatusMovedPermanently,
	stdhttp.StatusFound,
	stdhttp.StatusSeeOther,
	stdhttp.StatusTemporaryRedirect,
	stdhttp.StatusPermanentRedirect,
)

const (
	alphaRatioUserAgent = "harbrr"
	maxLoginBodyBytes   = 1 << 20
)

// formLoginAuth is the username/password form-login regime (ADR 0003): AlphaRatio and
// the planned #28-#31 Gazelle sites. It logs in against login.php, persists the
// resulting session cookie through the driver's setting store, and renews once on an
// auth-classified failure (redirect to login, 401/403, or an auth-flavoured response
// body). Implementations are stateless per-call; the session itself lives on the
// driver (sessionMu/session), guarded by loginGate for single-flight login/renewal.
type formLoginAuth struct{}

// Prepare ensures a session exists (driving an automatic login on first use) and
// attaches it: the User-Agent formLoginAuth login/browse traffic always carries, and —
// only when the doer has no cookie jar of its own — the session as an explicit Cookie
// header (a jar-owning doer attaches it from the jar instead).
func (formLoginAuth) Prepare(ctx context.Context, d *driver, req *stdhttp.Request) error {
	if _, err := d.Ensure(ctx, d.login); err != nil {
		return err
	}
	req.Header.Set("User-Agent", alphaRatioUserAgent)
	if d.Jar == nil {
		req.Header.Set("Cookie", d.Snapshot().Cookie)
	}
	return nil
}

// Recover renews the session once on an auth-classified failure. The generation
// travelling inside cause (see withGeneration) is the one the failed request actually
// used, so a concurrent renewal that already replaced the session is detected and
// skipped rather than triggering a duplicate login (see renewSession).
func (formLoginAuth) Recover(ctx context.Context, d *driver, cause error) (bool, error) {
	if !errors.Is(cause, login.ErrLoginFailed) {
		return false, cause
	}
	if err := d.Renew(ctx, generationFrom(cause), d.login); err != nil {
		return false, err
	}
	return true, nil
}

// Scrub returns the configured username plus every session cookie undeclared in
// Settings (so Base.Scrub's IsSecret-derived set never sees it on its own): the
// configured cookie setting and the current in-memory session, each expanded into its
// serialized and bare-value forms.
func (formLoginAuth) Scrub(d *driver) []string {
	return append([]string{d.Cfg["username"]},
		cookieScrubExtras(d.Cfg[d.site.sessionCookieSetting], d.Snapshot().Cookie)...)
}

// requestContext disables redirect following for cookie-authenticated operations. An
// expired AlphaRatio-regime session redirects to login; Search/Grab must see that
// response so they can renew once and retry instead of consuming login HTML.
func (d *driver) requestContext(ctx context.Context) context.Context {
	if d.site.disableRedirects {
		return apphttp.WithNoRedirectFollow(ctx)
	}
	return ctx
}

// login creates a clean session with the configured credentials, verifies a same-site
// post-login logout link, then persists the replacement cookie before publishing it to
// concurrent requests. It is the CookieSession's native.LoginFunc, so it always runs
// with the single-login gate held; the failed generation is not needed here, because
// the published generation is derived from the session this login actually replaced.
func (d *driver) login(ctx context.Context, _ uint64) error {
	form, err := d.alphaRatioLoginForm()
	if err != nil {
		return err
	}

	previous := d.Snapshot()
	d.ReplaceJarCookies("")
	restore := true
	defer func() {
		if restore {
			d.ReplaceJarCookies(previous.Cookie)
		}
	}()

	cookie, err := d.requestAlphaRatioLogin(ctx, form)
	if err != nil {
		return err
	}
	if d.persist != nil {
		if err := d.persist(ctx, d.site.sessionCookieSetting, cookie); err != nil {
			return alphaRatioLoginError("persist replacement session", err)
		}
	}

	d.Publish(native.SessionState{Cookie: cookie, Generation: previous.Generation + 1})
	restore = false
	return nil
}

func (d *driver) alphaRatioLoginForm() (url.Values, error) {
	username := d.Cfg["username"]
	password := d.Cfg["password"]
	if strings.TrimSpace(username) == "" || strings.TrimSpace(password) == "" {
		return nil, alphaRatioLoginError("username or password is empty", nil)
	}
	return url.Values{
		"username":   {username},
		"password":   {password},
		"keeplogged": {"1"},
	}, nil
}

// requestAlphaRatioLogin submits the login form and returns the bounded response's
// serialized session cookies only after authenticated-page validation. It updates the
// private cookie jar when present and wraps transport, response, and validation
// failures with [login.ErrLoginFailed].
func (d *driver) requestAlphaRatioLogin(ctx context.Context, form url.Values) (string, error) {
	req, err := d.NewRequest(ctx, stdhttp.MethodPost, d.BaseURL+"login.php", strings.NewReader(form.Encode()))
	if err != nil {
		return "", alphaRatioLoginError("build login request", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Referer", d.BaseURL+"login.php")
	req.Header.Set("User-Agent", alphaRatioUserAgent)

	resp, err := d.Doer.Do(req)
	if err != nil {
		return "", alphaRatioLoginError("login request failed", apphttp.RedactURLError(err))
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", alphaRatioLoginError(fmt.Sprintf("login returned HTTP %d", resp.StatusCode), nil)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxLoginBodyBytes+1))
	if err != nil {
		return "", alphaRatioLoginError("read login response", err)
	}
	if len(body) > maxLoginBodyBytes {
		return "", alphaRatioLoginError("login response exceeded size cap", nil)
	}
	if !d.alphaRatioAuthenticatedPage(body) {
		return "", alphaRatioLoginError("post-login page did not confirm authentication", nil)
	}

	if d.Jar != nil && len(resp.Cookies()) > 0 {
		d.Jar.SetCookies(d.CookieURL, resp.Cookies())
	}
	cookie := d.cookieHeader(resp.Cookies())
	if cookie == "" {
		return "", alphaRatioLoginError("login returned no usable session cookie", nil)
	}
	return cookie, nil
}

// alphaRatioAuthenticatedPage reports whether body contains an actual logout link whose
// path is /logout.php and whose host is relative or matches the configured tracker.
// Plain-text mentions and links to other hosts are not authentication evidence.
func (d *driver) alphaRatioAuthenticatedPage(body []byte) bool {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(body))
	if err != nil {
		return false
	}
	authenticated := false
	doc.Find("a[href]").EachWithBreak(func(_ int, link *goquery.Selection) bool {
		href, exists := link.Attr("href")
		if !exists {
			return true
		}
		parsed, err := url.Parse(strings.TrimSpace(href))
		if err != nil {
			return true
		}
		sameHost := parsed.Host == "" || strings.EqualFold(parsed.Host, d.CookieURL.Host)
		authenticated = sameHost && (parsed.Path == "/logout.php" || parsed.Path == "logout.php")
		return !authenticated
	})
	return authenticated
}

func (d *driver) cookieHeader(responseCookies []*stdhttp.Cookie) string {
	if d.Jar != nil {
		return d.JarCookieHeader()
	}
	return native.SerializeCookies(responseCookies)
}

// cookieScrubExtras expands each non-empty serialized cookie into itself plus its
// component bare values, matching Base.Scrub's literal-substring contract. Malformed
// input still contributes its exact raw form.
func cookieScrubExtras(cookies ...string) []string {
	var extra []string
	for _, raw := range cookies {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		extra = append(extra, raw)
		for _, cookie := range native.ParseCookieHeader(raw) {
			if value := strings.TrimSpace(cookie.Value); value != "" {
				extra = append(extra, value)
			}
		}
	}
	return extra
}

func alphaRatioLoginError(reason string, cause error) error {
	err := fmt.Errorf("gazelle: automatic login failed; verify configured username/password: %s: %w", reason, login.ErrLoginFailed)
	if cause == nil {
		return err
	}
	return errors.Join(err, cause)
}

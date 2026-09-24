package xspeeds

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	stdhttp "net/http"
	"net/url"
	"strings"

	"github.com/PuerkitoBio/goquery"

	apphttp "github.com/autobrr/harbrr/internal/http"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/login"
	"github.com/autobrr/harbrr/internal/indexer/native"
)

var classifySession = native.ClassifyAuth403.AlsoAuth(
	stdhttp.StatusMovedPermanently,
	stdhttp.StatusFound,
	stdhttp.StatusSeeOther,
	stdhttp.StatusTemporaryRedirect,
	stdhttp.StatusPermanentRedirect,
)

func runOperation[T any](ctx context.Context, d *driver, op string, attempt func(context.Context, native.SessionState) (T, error)) (T, error) {
	result, err := retrySession(ctx, d, op, attempt)
	return result, d.scrubErr(err)
}

func retrySession[T any](ctx context.Context, d *driver, op string, attempt func(context.Context, native.SessionState) (T, error)) (T, error) {
	var zero T
	session, err := d.Ensure(ctx, d.login)
	if err != nil {
		return zero, err
	}
	result, err := attempt(ctx, session)
	if err == nil || !errors.Is(err, login.ErrLoginFailed) {
		return result, d.scrubErr(err, session.Cookie)
	}
	if err := d.Renew(ctx, session.Generation, d.login); err != nil {
		return zero, err
	}
	renewed := d.Snapshot()
	result, err = attempt(ctx, renewed)
	if err != nil && errors.Is(err, login.ErrLoginFailed) {
		err = fmt.Errorf("xspeeds: automatic session renewal did not authenticate %s: %w", op, err)
	}
	err = d.scrubErr(err, session.Cookie, renewed.Cookie)
	d.RememberLoginError(renewed.Generation, err)
	return result, err
}

// login is the CookieSession's native.LoginFunc: it runs the form login against a
// cleared jar, persists the replacement cookie, and publishes the new session — or
// restores the previous one and remembers the failure against failedGeneration, so
// concurrent callers on that generation fail fast instead of re-running a login that
// just failed.
func (d *driver) login(ctx context.Context, failedGeneration uint64) error {
	previous := d.Snapshot()
	d.ReplaceJarCookies("")

	err := d.performLogin(ctx)
	replacement := d.JarCookieHeader()
	if err == nil && replacement == "" {
		err = loginFailed("login returned no usable session cookie", nil)
	}
	if err == nil && d.persist != nil {
		if persistErr := d.persist(ctx, "cookie", replacement); persistErr != nil {
			err = fmt.Errorf("xspeeds: persist replacement session: %w", persistErr)
		}
	}
	if err != nil {
		d.ReplaceJarCookies(previous.Cookie)
		err = d.scrubErr(err, previous.Cookie, replacement)
		d.CompleteLogin(failedGeneration, previous, err)
		return err
	}

	d.CompleteLogin(failedGeneration, native.SessionState{
		Cookie:     replacement,
		Generation: previous.Generation + 1,
	}, nil)
	return nil
}

func (d *driver) performLogin(ctx context.Context) error {
	landingURL := d.BaseURL + "login.php"
	landing, err := d.NewRequest(ctx, stdhttp.MethodGet, landingURL, nil)
	if err != nil {
		return err
	}
	if _, err := d.Do(ctx, landing, native.ClassifyAuth403); err != nil {
		return fmt.Errorf("xspeeds: fetch login landing page: %w", err)
	}

	form := url.Values{
		"username": {d.Cfg["username"]},
		"password": {d.Cfg["password"]},
	}
	request, err := d.NewRequest(ctx, stdhttp.MethodPost, d.BaseURL+"takelogin.php", strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Referer", landingURL)

	response, err := d.Do(ctx, request, native.ClassifyAuth403)
	if err != nil {
		return fmt.Errorf("xspeeds: submit login request: %w", err)
	}
	if bytes.Contains(response.Body, []byte("logout.php")) {
		return nil
	}
	return loginFailed(loginMessage(response.Body), nil)
}

func loginMessage(body []byte) string {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(body))
	if err == nil {
		for _, selector := range []string{
			".left_side table:nth-of-type(1) tr:nth-of-type(2)",
			"div.notification-body",
		} {
			if message := strings.Join(strings.Fields(doc.Find(selector).First().Text()), " "); message != "" {
				return message
			}
		}
	}
	return "Unknown error message, please report."
}

func loginFailed(reason string, cause error) error {
	err := fmt.Errorf("xspeeds: automatic login failed: %s: %w", reason, login.ErrLoginFailed)
	if cause == nil {
		return err
	}
	return errors.Join(err, cause)
}

func (d *driver) isLoginPage(body []byte) bool {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(body))
	if err != nil {
		return false
	}
	logoutPath := strings.TrimRight(d.CookieURL.Path, "/") + "/logout.php"
	authenticated := false
	doc.Find("a[href]").EachWithBreak(func(_ int, link *goquery.Selection) bool {
		href, exists := link.Attr("href")
		if !exists {
			return true
		}
		parsed, parseErr := url.Parse(strings.TrimSpace(href))
		if parseErr != nil {
			return true
		}
		resolved := d.CookieURL.ResolveReference(parsed)
		authenticated = resolved.User == nil &&
			strings.EqualFold(resolved.Scheme, d.CookieURL.Scheme) &&
			strings.EqualFold(resolved.Host, d.CookieURL.Host) &&
			(resolved.Path == "/logout.php" || resolved.Path == logoutPath)
		return !authenticated
	})
	if authenticated {
		return false
	}
	return doc.Find(`form[action*="takelogin.php"]`).Length() > 0
}

func (d *driver) scrubErr(err error, requestCookies ...string) error {
	if err == nil {
		return nil
	}
	current := d.Snapshot().Cookie
	extra := credentialScrubExtras(d.Cfg["username"])
	extra = append(extra, cookieScrubExtras(d.Cfg["cookie"], current)...)
	extra = append(extra, cookieScrubExtras(requestCookies...)...)
	return d.ScrubErr(err, extra...)
}

func (d *driver) captureSecrets(requestCookie string) []string {
	current := d.Snapshot().Cookie
	extra := credentialScrubExtras(d.Cfg["username"])
	return append(extra, cookieScrubExtras(d.Cfg["cookie"], current, requestCookie)...)
}

// credentialScrubExtras is the length guard for values this driver adds to the scrub
// set beyond the definition-derived ones. The password needs no entry: it is declared
// Type "password", so loader.SecretValues already hands it to Base.Scrub on every
// call. The username is NOT a declared secret (text-typed, no credential token in its
// name), so scrubbing it is this driver's own choice — and an unguarded short one
// would be the shredding hazard native.SessionSecrets exists to prevent, since
// apphttp.ScrubValues is a literal ReplaceAll.
func credentialScrubExtras(username string) []string {
	return native.SessionSecrets(strings.TrimSpace(username))
}

// cookieScrubExtras expands a serialized Cookie header into the values worth scrubbing:
// the header itself, plus each cookie value long enough to be a session token.
// XSpeeds exposes no stable session-cookie name, so length is the only signal — the
// same judgement, and now the same threshold, as native.SessionSecrets.
func cookieScrubExtras(cookies ...string) []string {
	var extra []string
	for _, raw := range cookies {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		extra = append(extra, raw)
		values := make([]string, 0, 4)
		for _, cookie := range native.ParseCookieHeader(raw) {
			values = append(values, strings.TrimSpace(cookie.Value))
		}
		extra = append(extra, native.SessionSecrets(values...)...)
	}
	return extra
}

func noRedirects(ctx context.Context) context.Context {
	return apphttp.WithNoRedirectFollow(ctx)
}

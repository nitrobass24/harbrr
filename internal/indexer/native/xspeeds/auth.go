package xspeeds

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	stdhttp "net/http"
	"net/url"
	"slices"
	"strings"
	"time"

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

func runOperation[T any](ctx context.Context, d *driver, op string, attempt func(context.Context, sessionState) (T, error)) (T, error) {
	result, err := retrySession(ctx, d, op, attempt)
	return result, d.scrubErr(err)
}

func retrySession[T any](ctx context.Context, d *driver, op string, attempt func(context.Context, sessionState) (T, error)) (T, error) {
	var zero T
	session, err := d.ensureSession(ctx)
	if err != nil {
		return zero, err
	}
	result, err := attempt(ctx, session)
	if err == nil || !errors.Is(err, login.ErrLoginFailed) {
		return result, d.scrubErr(err, session.cookie)
	}
	if err := d.renewSession(ctx, session.generation); err != nil {
		return zero, err
	}
	renewed := d.sessionSnapshot()
	result, err = attempt(ctx, renewed)
	if err != nil && errors.Is(err, login.ErrLoginFailed) {
		err = fmt.Errorf("xspeeds: automatic session renewal did not authenticate %s: %w", op, err)
	}
	err = d.scrubErr(err, session.cookie, renewed.cookie)
	d.rememberLoginError(renewed.generation, err)
	return result, err
}

func (d *driver) ensureSession(ctx context.Context) (sessionState, error) {
	session, last := d.snapshot()
	if err := rememberedLoginError(session, last); err != nil {
		return sessionState{}, err
	}
	if session.cookie != "" {
		return session, nil
	}
	if err := d.loginGate.Acquire(ctx, 1); err != nil {
		return sessionState{}, fmt.Errorf("xspeeds: wait for automatic login: %w", err)
	}
	defer d.loginGate.Release(1)

	session, last = d.snapshot()
	if err := rememberedLoginError(session, last); err != nil {
		return sessionState{}, err
	}
	if session.cookie != "" {
		return session, nil
	}
	if err := d.loginLocked(ctx, session.generation); err != nil {
		return sessionState{}, err
	}
	return d.sessionSnapshot(), nil
}

func (d *driver) renewSession(ctx context.Context, failedGeneration uint64) error {
	if err := d.loginGate.Acquire(ctx, 1); err != nil {
		return fmt.Errorf("xspeeds: wait for automatic session renewal: %w", err)
	}
	defer d.loginGate.Release(1)

	session, last := d.snapshot()
	if session.cookie != "" && session.generation != failedGeneration {
		return nil
	}
	if err := rememberedLoginError(session, last); err != nil {
		return err
	}
	return d.loginLocked(ctx, failedGeneration)
}

func rememberedLoginError(session sessionState, last loginResult) error {
	if last.err != nil && session.generation == last.failedGeneration {
		return last.err
	}
	return nil
}

func (d *driver) rememberLoginError(failedGeneration uint64, err error) {
	if !errors.Is(err, login.ErrLoginFailed) {
		return
	}
	d.stateMu.Lock()
	if d.session.generation == failedGeneration {
		d.lastLogin = loginResult{failedGeneration: failedGeneration, err: err}
	}
	d.stateMu.Unlock()
}

func (d *driver) loginLocked(ctx context.Context, failedGeneration uint64) error {
	previous := d.sessionSnapshot()
	d.replaceJarCookies("")

	err := d.performLogin(ctx)
	replacement := serializeCookies(d.jar.Cookies(d.cookieURL))
	if err == nil && replacement == "" {
		err = loginFailed("login returned no usable session cookie", nil)
	}
	if err == nil && d.persist != nil {
		if persistErr := d.persist(ctx, "cookie", replacement); persistErr != nil {
			err = loginFailed("persist replacement session", persistErr)
		}
	}
	if err != nil {
		d.replaceJarCookies(previous.cookie)
		err = d.scrubErr(err, previous.cookie, replacement)
		d.completeLogin(failedGeneration, previous, err)
		return err
	}

	d.completeLogin(failedGeneration, sessionState{
		cookie:     replacement,
		generation: previous.generation + 1,
	}, nil)
	return nil
}

func (d *driver) completeLogin(failedGeneration uint64, session sessionState, err error) {
	d.stateMu.Lock()
	d.session = session
	d.lastLogin = loginResult{}
	if errors.Is(err, login.ErrLoginFailed) {
		d.lastLogin = loginResult{
			failedGeneration: failedGeneration,
			err:              err,
		}
	}
	d.stateMu.Unlock()
}

func (d *driver) performLogin(ctx context.Context) error {
	landingURL := d.BaseURL + "login.php"
	landing, err := stdhttp.NewRequestWithContext(ctx, stdhttp.MethodGet, landingURL, nil)
	if err != nil {
		return fmt.Errorf("xspeeds: build login landing request: %w", err)
	}
	if _, err := d.Do(ctx, landing, native.ClassifyAuth403); err != nil {
		return fmt.Errorf("xspeeds: fetch login landing page: %w", err)
	}

	form := url.Values{
		"username": {d.Cfg["username"]},
		"password": {d.Cfg["password"]},
	}
	request, err := stdhttp.NewRequestWithContext(ctx, stdhttp.MethodPost, d.BaseURL+"takelogin.php", strings.NewReader(form.Encode()))
	if err != nil {
		return fmt.Errorf("xspeeds: build login request: %w", err)
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
	logoutPath := strings.TrimRight(d.cookieURL.Path, "/") + "/logout.php"
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
		resolved := d.cookieURL.ResolveReference(parsed)
		authenticated = resolved.User == nil &&
			strings.EqualFold(resolved.Scheme, d.cookieURL.Scheme) &&
			strings.EqualFold(resolved.Host, d.cookieURL.Host) &&
			(resolved.Path == "/logout.php" || resolved.Path == logoutPath)
		return !authenticated
	})
	if authenticated {
		return false
	}
	return doc.Find(`form[action*="takelogin.php"]`).Length() > 0 || bytes.Contains(body, []byte("logout.php"))
}

func (d *driver) replaceJarCookies(raw string) {
	current := d.jar.Cookies(d.cookieURL)
	expired := make([]*stdhttp.Cookie, 0, len(current))
	for _, cookie := range current {
		//nolint:gosec // G124: deletion cookies stay inside the private per-instance jar.
		expired = append(expired, &stdhttp.Cookie{
			Name:    cookie.Name,
			Value:   "",
			MaxAge:  -1,
			Expires: time.Unix(1, 0),
		})
	}
	if len(expired) > 0 {
		d.jar.SetCookies(d.cookieURL, expired)
	}
	if cookies := parseCookieHeader(raw); len(cookies) > 0 {
		d.jar.SetCookies(d.cookieURL, cookies)
	}
}

func parseCookieHeader(raw string) []*stdhttp.Cookie {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	request := &stdhttp.Request{Header: stdhttp.Header{"Cookie": []string{raw}}}
	return request.Cookies()
}

func serializeCookies(cookies []*stdhttp.Cookie) string {
	usable := make([]*stdhttp.Cookie, 0, len(cookies))
	for _, cookie := range cookies {
		if cookie != nil && strings.TrimSpace(cookie.Name) != "" && cookie.Value != "" {
			usable = append(usable, cookie)
		}
	}
	slices.SortFunc(usable, func(a, b *stdhttp.Cookie) int {
		return cmp.Compare(a.Name, b.Name)
	})
	request := &stdhttp.Request{Header: stdhttp.Header{}}
	for _, cookie := range usable {
		request.AddCookie(cookie)
	}
	return request.Header.Get("Cookie")
}

func (d *driver) scrubErr(err error, requestCookies ...string) error {
	if err == nil {
		return nil
	}
	current := d.sessionSnapshot().cookie
	extra := make([]string, 0, 2)
	extra = append(extra, d.Cfg["username"], d.Cfg["password"])
	extra = append(extra, cookieScrubExtras(d.Cfg["cookie"], current)...)
	extra = append(extra, cookieScrubExtras(requestCookies...)...)
	return d.ScrubErr(err, extra...)
}

func (d *driver) captureSecrets(requestCookie string) []string {
	current := d.sessionSnapshot().cookie
	extra := make([]string, 0, 2)
	extra = append(extra, d.Cfg["username"], d.Cfg["password"])
	return append(extra, cookieScrubExtras(d.Cfg["cookie"], current, requestCookie)...)
}

func cookieScrubExtras(cookies ...string) []string {
	// ponytail: XSpeeds exposes no stable session-cookie name; scrub values of
	// credential-like length until sanitized live headers establish one.
	const minCookieSecretLength = 8

	var extra []string
	for _, raw := range cookies {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		extra = append(extra, raw)
		for _, cookie := range parseCookieHeader(raw) {
			if value := strings.TrimSpace(cookie.Value); len(value) >= minCookieSecretLength {
				extra = append(extra, value)
			}
		}
	}
	return extra
}

func noRedirects(ctx context.Context) context.Context {
	return apphttp.WithNoRedirectFollow(ctx)
}

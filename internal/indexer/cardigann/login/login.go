package login

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	stdhttp "net/http"
	"net/url"
	"strings"

	apphttp "github.com/autobrr/harbrr/internal/http"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/template"
)

// Typed errors. Callers (item 10) branch on these to decide whether to surface a
// captcha/solver requirement to the user, retry, or fail hard. None of these
// error values ever embed a credential, cookie, or response body — only the
// method, a redacted URL, a selector string, or an HTTP status.
var (
	// ErrCaptchaRequired reports the login page presents a captcha. Solving is
	// Phase 6; item 8 only detects it and fails loud behind this boundary.
	ErrCaptchaRequired = errors.New("login requires captcha (solving is Phase 6)")
	// ErrSolverRequired reports an anti-bot interstitial (e.g. Cloudflare) that
	// needs a FlareSolverr-style solver. Solving is Phase 6.
	ErrSolverRequired = errors.New("login requires an anti-bot solver (FlareSolverr; Phase 6)")
	// ErrUnknownMethod reports a Login.Method this executor does not implement.
	ErrUnknownMethod = errors.New("unknown login method")
	// ErrLoginFailed reports that the login round-trip completed but an error
	// selector matched (bad credentials, etc.). The wrapped message is the
	// definition-authored error text, never a credential.
	ErrLoginFailed = errors.New("login failed")
)

// maxLoginBodyBytes caps how much of a response body the executor reads into
// memory for selector evaluation. Login/test pages are small; this guards
// against a hostile or broken server streaming unbounded bytes.
const maxLoginBodyBytes = 8 << 20 // 8 MiB

// Login authenticates the definition by dispatching on Login.Method. A
// definition with no Login block is a no-op success (matching Jackett's
// DoLogin early return). Unknown methods fail loud via ErrUnknownMethod.
func (e *Executor) Login(ctx context.Context, def *loader.Definition) error {
	if def.Login == nil {
		return nil
	}
	if err := e.checkCaptcha(def.Login); err != nil {
		return err
	}

	// Jackett defaults an unset Login.Method to "form"
	// (CardigannIndexer: `if (Definition.Login is { Method: null }) Method = "form"`).
	switch loginMethod(def.Login) {
	case "form":
		return e.loginForm(ctx, def)
	case "post":
		return e.loginPost(ctx, def)
	case "get":
		return e.loginGet(ctx, def)
	case "cookie":
		// cookie login seeds the jar only — no request, so no ctx.
		return e.loginCookie(def)
	case "oneurl":
		return e.loginOneURL(ctx, def)
	default:
		return fmt.Errorf("%w: %q", ErrUnknownMethod, def.Login.Method)
	}
}

// loginMethod returns the effective login method, defaulting an unset method to
// "form" exactly as Jackett does at definition load.
func loginMethod(l *loader.Login) string {
	if l.Method == "" {
		return "form"
	}
	return l.Method
}

// checkCaptcha fails loud when the login declares a captcha block. Detection
// only; solving is Phase 6. The error names the captcha type/selector, never
// page content.
func (e *Executor) checkCaptcha(l *loader.Login) error {
	if l.Captcha == nil {
		return nil
	}
	return fmt.Errorf("%w: type=%q selector=%q", ErrCaptchaRequired, l.Captcha.Type, l.Captcha.Selector)
}

// CheckTest runs the Login.Test block: GET Test.Path and assert Test.Selector
// matches at least one element (Jackett's TestLogin / CheckIfLoginIsNeeded
// signal). A definition with no Test block cannot be probed, so CheckTest
// reports false (login is needed) without error — the caller then runs Login.
func (e *Executor) CheckTest(ctx context.Context, def *loader.Definition) (bool, error) {
	if def.Login == nil {
		return true, nil
	}
	if def.Login.Test == nil {
		return false, nil
	}

	testURL, err := e.resolvePath(def.Login.Test.Path)
	if err != nil {
		return false, err
	}
	body, _, err := e.get(ctx, testURL, def.Login.Headers)
	if err != nil {
		return false, err
	}
	if def.Login.Test.Selector == "" {
		return true, nil
	}
	matched, err := e.selectorMatches(body, def.Login.Test.Selector)
	if err != nil {
		return false, err
	}
	return matched, nil
}

// EnsureLoggedIn probes the session with CheckTest and only logs in when the
// test fails. This is the re-login entry point item 10 calls before each search.
func (e *Executor) EnsureLoggedIn(ctx context.Context, def *loader.Definition) error {
	ok, err := e.CheckTest(ctx, def)
	if err != nil {
		return err
	}
	if ok {
		return nil
	}
	return e.Login(ctx, def)
}

// resolvePath resolves a (possibly relative) definition path against BaseURL,
// mirroring Jackett's resolvePath. An absolute URL in the definition is returned
// as-is. Errors reference the redacted path only.
func (e *Executor) resolvePath(raw string) (string, error) {
	rendered, err := template.Eval(raw, e.templateContext())
	if err != nil {
		return "", fmt.Errorf("rendering path %q: %w", apphttp.RedactURL(raw), err)
	}
	ref, err := url.Parse(rendered)
	if err != nil {
		return "", fmt.Errorf("parsing path %q: %w", apphttp.RedactURL(rendered), err)
	}
	if ref.IsAbs() {
		return ref.String(), nil
	}
	base, err := url.Parse(e.BaseURL)
	if err != nil {
		return "", fmt.Errorf("parsing base URL %q: %w", apphttp.RedactURL(e.BaseURL), err)
	}
	return base.ResolveReference(ref).String(), nil
}

// get issues a GET, returning the (capped) body and final status. The cookie jar
// is applied/updated by the production Doer; tests assert on the recorded
// request. All error sites redact the URL.
func (e *Executor) get(ctx context.Context, rawURL string, headers map[string][]string) (body []byte, status int, err error) {
	return e.do(ctx, stdhttp.MethodGet, rawURL, nil, headers)
}

// do performs one request through the seam and reads the body. It applies jar
// cookies on the way out and stores Set-Cookie on the way back when the Doer
// does not own a jar (the replay transport in tests), so cookie capture is
// exercised offline. Production *http.Client owns its own jar and this
// best-effort store is harmless (idempotent) there.
func (e *Executor) do(ctx context.Context, method, rawURL string, bodyReader io.Reader, headers map[string][]string) ([]byte, int, error) {
	req, err := stdhttp.NewRequestWithContext(ctx, method, rawURL, bodyReader)
	if err != nil {
		return nil, 0, fmt.Errorf("building %s request to %s: %w", method, apphttp.RedactURL(rawURL), err)
	}
	for name, vals := range headers {
		for _, v := range vals {
			rendered, rerr := template.Eval(v, e.templateContext())
			if rerr != nil {
				return nil, 0, fmt.Errorf("rendering header %q: %w", name, rerr)
			}
			req.Header.Add(name, rendered)
		}
	}
	e.applyJar(req)

	resp, err := e.Client.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("%s %s: %w", method, apphttp.RedactURL(rawURL), redactErr(err))
	}
	defer func() { _ = resp.Body.Close() }()

	e.storeJar(req.URL, resp)

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxLoginBodyBytes))
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("reading response from %s: %w", apphttp.RedactURL(rawURL), err)
	}
	return data, resp.StatusCode, nil
}

// applyJar attaches the jar's cookies for the request URL onto the outgoing
// request. Production *http.Client does this itself; doing it here too makes the
// offline replay transport see authenticated cookies on the wire.
func (e *Executor) applyJar(req *stdhttp.Request) {
	if e.Jar == nil {
		return
	}
	for _, c := range e.Jar.Cookies(req.URL) {
		req.AddCookie(c)
	}
}

// storeJar records any Set-Cookie from the response into the jar, scoped to the
// request URL, so cookies persist across the login round-trip and into search.
func (e *Executor) storeJar(reqURL *url.URL, resp *stdhttp.Response) {
	if e.Jar == nil {
		return
	}
	if cs := resp.Cookies(); len(cs) > 0 {
		e.Jar.SetCookies(reqURL, cs)
	}
}

// redactErr scrubs a transport error string of any embedded URL secrets. The
// stdlib *url.Error stringifies the full URL (with query) into its message, so
// we rebuild a redacted form rather than risk leaking a passkey in a wrapped
// "Get \"...?passkey=...\"" error.
func redactErr(err error) error {
	var uerr *url.Error
	if errors.As(err, &uerr) {
		return fmt.Errorf("%s %s: %w", uerr.Op, apphttp.RedactURL(uerr.URL), uerr.Err)
	}
	return err
}

// cloudflareMarkers are byte signatures of a Cloudflare (or similar) anti-bot
// challenge page. Their presence means the real login page never loaded, so the
// definition's selectors would all miss; we fail loud with ErrSolverRequired
// rather than mis-report a login failure. Solving is Phase 6.
var cloudflareMarkers = [][]byte{
	[]byte("Just a moment..."),
	[]byte("cf-browser-verification"),
	[]byte("cf_chl_"),
	[]byte("Checking your browser before accessing"),
	[]byte("Attention Required! | Cloudflare"),
	[]byte("DDoS protection by Cloudflare"),
}

// detectAntiBot fails loud when the landing page is an anti-bot interstitial.
// The error names the detector only; no page bytes are included.
func detectAntiBot(body []byte) error {
	for _, m := range cloudflareMarkers {
		if bytes.Contains(body, m) {
			return fmt.Errorf("%w: detected an anti-bot challenge page", ErrSolverRequired)
		}
	}
	return nil
}

// parseCookieHeader splits a raw Cookie-header string ("a=1; b=2; c=3") into
// http.Cookie values. Whitespace-only or empty pairs are skipped. Returns nil
// when no valid pair is present.
func parseCookieHeader(raw string) []*stdhttp.Cookie {
	var out []*stdhttp.Cookie
	for _, part := range strings.Split(raw, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		name, value, _ := strings.Cut(part, "=")
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		// These are REQUEST cookies parsed from a user-supplied Cookie header for
		// the manual-cookie login fallback; Secure/HttpOnly/SameSite are
		// response-cookie (Set-Cookie) attributes and do not apply to a cookie we
		// send. The jar scopes them to the tracker host.
		out = append(out, &stdhttp.Cookie{Name: name, Value: strings.TrimSpace(value)}) //nolint:gosec // request cookie; Set-Cookie security attrs are N/A
	}
	return out
}

// trimMessage trims and single-lines an extracted error message before it is
// wrapped into ErrLoginFailed. The message is definition-authored error text
// (e.g. "Invalid username or password"), not a credential, but we still keep it
// compact and free of stray whitespace for clean logs.
func trimMessage(s string) string {
	return strings.TrimSpace(strings.Join(strings.Fields(s), " "))
}

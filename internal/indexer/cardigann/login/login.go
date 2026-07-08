package login

import (
	"bufio"
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
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

// Typed errors. Callers branch on these to decide whether to surface a
// captcha/solver requirement to the user, retry, or fail hard. None of these
// error values ever embed a credential, cookie, or response body — only the
// method, a redacted URL, a selector string, or an HTTP status.
var (
	// ErrCaptchaRequired reports the login page presents a captcha, which the
	// executor detects and fails loud behind this boundary rather than solving.
	ErrCaptchaRequired = errors.New("login requires captcha (not supported)")
	// ErrSolverRequired reports an anti-bot interstitial (e.g. Cloudflare) that
	// needs a FlareSolverr-style solver.
	ErrSolverRequired = errors.New("login requires an anti-bot solver (FlareSolverr)")
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
// only; it does not solve the captcha. The error names the captcha
// type/selector, never page content.
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
// test fails. This is the re-login entry point the engine calls before each search.
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
		return "", fmt.Errorf("rendering path %q: %w", apphttp.SchemeHost(raw), err)
	}
	ref, err := url.Parse(rendered)
	if err != nil {
		return "", fmt.Errorf("parsing path %q: %w", apphttp.SchemeHost(rendered), apphttp.RedactURLError(err))
	}
	if ref.IsAbs() {
		return ref.String(), nil
	}
	base, err := url.Parse(e.BaseURL)
	if err != nil {
		return "", fmt.Errorf("parsing base URL %q: %w", apphttp.SchemeHost(e.BaseURL), apphttp.RedactURLError(err))
	}
	return base.ResolveReference(ref).String(), nil
}

// get issues a GET, returning the (capped) body and final status. Cookies are
// applied/recorded by the Doer's jar (see the Doer cookie contract); tests
// assert on the recorded request. All error sites redact the URL.
func (e *Executor) get(ctx context.Context, rawURL string, headers map[string][]string) (body []byte, status int, err error) {
	return e.do(ctx, stdhttp.MethodGet, rawURL, nil, headers)
}

// do performs one request through the seam and reads the body. It deliberately
// touches NO cookies: the Doer's jar is the single cookie authority — it applies
// jar cookies to every hop and records Set-Cookie from every hop, including a
// session rotation on a login POST's followed 302 (which this method never sees;
// only the final response comes back). Writing a Cookie header here as well
// would put a second, possibly stale pair on the wire — trackers (PHP) read the
// FIRST pair, so a stale-first duplicate presents the logged-out session forever.
func (e *Executor) do(ctx context.Context, method, rawURL string, bodyReader io.Reader, headers map[string][]string) ([]byte, int, error) {
	req, err := stdhttp.NewRequestWithContext(ctx, method, rawURL, bodyReader)
	if err != nil {
		return nil, 0, fmt.Errorf("building %s request to %s: %w", method, apphttp.SchemeHost(rawURL), apphttp.RedactURLError(err))
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
	// A UA-bound cf_clearance is rejected unless every request reuses the solver's
	// User-Agent, so once a solve set one this session, replay it here (the login
	// POST, login.test, and the post-solve retry all flow through do()). A
	// definition's own User-Agent header still wins.
	if ua := e.solverUA(); ua != "" && req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", ua)
	}

	resp, err := e.Client.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("%s %s: %w", method, apphttp.SchemeHost(rawURL), apphttp.RedactURLError(err))
	}
	defer func() { _ = resp.Body.Close() }()

	reader, err := decompressBody(resp)
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("decompressing response from %s: %w", apphttp.SchemeHost(rawURL), err)
	}
	data, err := io.ReadAll(io.LimitReader(reader, maxLoginBodyBytes))
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("reading response from %s: %w", apphttp.SchemeHost(rawURL), err)
	}
	return data, resp.StatusCode, nil
}

// decompressBody wraps the response body when it carries a Content-Encoding that
// harbrr requested explicitly (the solver replay sends "Accept-Encoding: gzip, deflate",
// which suppresses net/http's transparent gzip handling). A normal request lets
// net/http auto-decompress and strip the header, so this is a pass-through there.
func decompressBody(resp *stdhttp.Response) (io.Reader, error) {
	switch strings.ToLower(resp.Header.Get("Content-Encoding")) {
	case "gzip":
		zr, err := gzip.NewReader(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("gzip reader: %w", err)
		}
		return zr, nil
	case "deflate":
		// HTTP "deflate" per RFC 9110 is zlib-wrapped (RFC 1950), but many servers
		// send raw DEFLATE (RFC 1951). Sniff the 2-byte zlib header and pick the
		// matching reader so both interoperate.
		br := bufio.NewReader(resp.Body)
		if looksZlibWrapped(br) {
			zr, err := zlib.NewReader(br)
			if err != nil {
				return nil, fmt.Errorf("zlib reader: %w", err)
			}
			return zr, nil
		}
		return flate.NewReader(br), nil
	default:
		return resp.Body, nil
	}
}

// looksZlibWrapped reports whether the next two bytes are a valid zlib header
// (RFC 1950): the low nibble of CMF is 8 (deflate) and the 16-bit CMF·FLG value is
// a multiple of 31. Peek does not consume, so the chosen reader sees the full stream.
func looksZlibWrapped(r *bufio.Reader) bool {
	b, err := r.Peek(2)
	if err != nil {
		return false
	}
	return b[0]&0x0f == 8 && (uint16(b[0])<<8|uint16(b[1]))%31 == 0
}

// cloudflareMarkers are byte signatures of a Cloudflare (or similar) anti-bot
// challenge page. Their presence means the real login page never loaded, so the
// definition's selectors would all miss; we fail loud with ErrSolverRequired
// rather than mis-report a login failure.
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

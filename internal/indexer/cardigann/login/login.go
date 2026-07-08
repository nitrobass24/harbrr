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

// CheckTest runs the Login.Test block to decide whether the session is still
// authenticated, reproducing Jackett's TestLogin (CardigannIndexer.TestLogin).
// A definition with no Login block reports true (nothing to log into); a Login
// block with no Test block cannot be probed, so CheckTest reports false (login
// is needed) without error — the caller then runs Login.
//
// Jackett's order, which harbrr matches for parity:
//  1. Fetch Test.Path WITHOUT auto-following — Jackett's WebClient never
//     follows, so a logged-out redirect is observable; harbrr stamps
//     WithNoRedirectFollow to make the shared client surface the raw 3xx.
//  2. Follow a SAME-DOMAIN redirect exactly once (TestLogin ~881-886, its
//     FollowIfRedirect maxRedirects:1); a cross-domain redirect is never
//     followed.
//  3. If the response is STILL a redirect after that single follow, login is
//     needed — bail BEFORE the selector check (~888-907), for both
//     selector-bearing and selector-less test blocks.
//  4. Otherwise a selector-less block trusts the non-redirect landing (Jackett
//     returns true), and a selector-bearing block requires its selector to
//     match at least one element (~909-920).
//
// This is the fix for U4-F4: the previous CheckTest let the client follow every
// redirect and discarded the status, so an expired session that redirected to a
// login page (cross-domain, or a chain) was reported as logged in.
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
	body, status, location, err := e.getNoFollow(ctx, testURL, def.Login.Headers)
	if err != nil {
		return false, err
	}
	// Jackett follows a same-domain redirect once, then re-evaluates.
	if isRedirectStatus(status) && location != "" && !e.crossDomainRedirect(testURL, location) {
		body, status, _, err = e.getNoFollow(ctx, location, def.Login.Headers)
		if err != nil {
			return false, err
		}
	}
	// A still-redirecting response — cross-domain, or a chain unresolved after
	// one hop — means the session is gone: login needed, before any selector.
	if isRedirectStatus(status) {
		return false, nil
	}
	if def.Login.Test.Selector == "" {
		return true, nil
	}
	return e.selectorMatches(body, def.Login.Test.Selector)
}

// crossDomainRedirect reports whether a redirect leaves the tracker's site,
// mirroring Jackett's GetRedirectDomainHint: a redirect counts as cross-domain
// only when the request was under BaseURL and the target is not. CheckTest
// follows a same-domain redirect once but never follows a cross-domain one.
//
// The prefix is forced to end in "/" to match Jackett, whose SiteLink invariant
// is slash-terminated for exactly this reason: without it, a BaseURL of
// "https://t.example" (which a user can save unnormalized) would prefix-match a
// look-alike host "https://t.example.evil.com/…" and wrongly treat it as
// same-domain, following one hop off-site.
func (e *Executor) crossDomainRedirect(requestURL, redirectURL string) bool {
	base := strings.TrimRight(e.BaseURL, "/") + "/"
	return strings.HasPrefix(requestURL, base) && !strings.HasPrefix(redirectURL, base)
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

// get issues a GET, returning the (capped) body and final status. The shared
// client follows redirects (see do); the login-test path uses getNoFollow.
// Cookies are applied/recorded by the Doer's jar (see the Doer cookie contract);
// tests assert on the recorded request. All error sites redact the URL.
func (e *Executor) get(ctx context.Context, rawURL string, headers map[string][]string) (body []byte, status int, err error) {
	return e.do(ctx, stdhttp.MethodGet, rawURL, nil, headers)
}

// getNoFollow issues a GET whose redirects are surfaced to the caller instead of
// followed: it stamps apphttp.WithNoRedirectFollow so the shared client's
// RedirectPolicy hands back the raw 3xx (the same no-follow contract the search
// stage uses). It additionally returns the redirect Location resolved against
// rawURL ("" when the response is not a Location-bearing 3xx). Only CheckTest
// uses it, to reproduce Jackett's TestLogin — whose WebClient never auto-follows.
func (e *Executor) getNoFollow(ctx context.Context, rawURL string, headers map[string][]string) (body []byte, status int, location string, err error) {
	return e.send(apphttp.WithNoRedirectFollow(ctx), stdhttp.MethodGet, rawURL, nil, headers)
}

// do performs one request through the seam and reads the body, letting the
// client follow redirects — the post-login 302 lands on the page the error/test
// selectors read. It discards the redirect Location (there is none on a followed
// request's final response); the login-test path wants it, so it calls send via
// getNoFollow instead.
func (e *Executor) do(ctx context.Context, method, rawURL string, bodyReader io.Reader, headers map[string][]string) ([]byte, int, error) {
	body, status, _, err := e.send(ctx, method, rawURL, bodyReader, headers)
	return body, status, err
}

// send is the shared request core for do/getNoFollow. It deliberately touches NO
// cookies: the Doer's jar is the single cookie authority — it applies jar
// cookies to every hop and records Set-Cookie from every hop, including a
// session rotation on a login POST's followed 302 (which this method never sees;
// only the final response comes back). Writing a Cookie header here as well
// would put a second, possibly stale pair on the wire — trackers (PHP) read the
// FIRST pair, so a stale-first duplicate presents the logged-out session forever.
// Whether a redirect is followed is decided by the caller through ctx (see
// getNoFollow); on an unfollowed 3xx, send returns the Location resolved against
// rawURL.
func (e *Executor) send(ctx context.Context, method, rawURL string, bodyReader io.Reader, headers map[string][]string) ([]byte, int, string, error) {
	req, err := stdhttp.NewRequestWithContext(ctx, method, rawURL, bodyReader)
	if err != nil {
		return nil, 0, "", fmt.Errorf("building %s request to %s: %w", method, apphttp.SchemeHost(rawURL), apphttp.RedactURLError(err))
	}
	for name, vals := range headers {
		for _, v := range vals {
			rendered, rerr := template.Eval(v, e.templateContext())
			if rerr != nil {
				return nil, 0, "", fmt.Errorf("rendering header %q: %w", name, rerr)
			}
			req.Header.Add(name, rendered)
		}
	}
	// A UA-bound cf_clearance is rejected unless every request reuses the solver's
	// User-Agent, so once a solve set one this session, replay it here (the login
	// POST, login.test, and the post-solve retry all flow through send()). A
	// definition's own User-Agent header still wins.
	if ua := e.solverUA(); ua != "" && req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", ua)
	}

	resp, err := e.Client.Do(req)
	if err != nil {
		return nil, 0, "", fmt.Errorf("%s %s: %w", method, apphttp.SchemeHost(rawURL), apphttp.RedactURLError(err))
	}
	defer func() { _ = resp.Body.Close() }()

	reader, err := decompressBody(resp)
	if err != nil {
		return nil, resp.StatusCode, "", fmt.Errorf("decompressing response from %s: %w", apphttp.SchemeHost(rawURL), err)
	}
	data, err := io.ReadAll(io.LimitReader(reader, maxLoginBodyBytes))
	if err != nil {
		return nil, resp.StatusCode, "", fmt.Errorf("reading response from %s: %w", apphttp.SchemeHost(rawURL), err)
	}
	return data, resp.StatusCode, redirectLocation(resp, rawURL), nil
}

// isRedirectStatus reports whether status is a Location-bearing redirect the
// login-test path treats as Jackett's WebResult.IsRedirect does (301/302/303/
// 307/308). Mirrors search.isRedirectStatus; kept local to avoid coupling the
// login stage to the search package.
func isRedirectStatus(status int) bool {
	switch status {
	case stdhttp.StatusMovedPermanently, stdhttp.StatusFound, stdhttp.StatusSeeOther,
		stdhttp.StatusTemporaryRedirect, stdhttp.StatusPermanentRedirect:
		return true
	default:
		return false
	}
}

// redirectLocation resolves a 3xx response's Location against reqURL, so a
// relative Location works regardless of whether the Doer set resp.Request.
// Returns "" when the response is not a redirect or carries no usable Location.
// The result is never logged raw — like the request URL it can embed a secret.
func redirectLocation(resp *stdhttp.Response, reqURL string) string {
	if !isRedirectStatus(resp.StatusCode) {
		return ""
	}
	loc := resp.Header.Get("Location")
	if loc == "" {
		return ""
	}
	base, err := url.Parse(reqURL)
	if err != nil {
		return ""
	}
	ref, err := url.Parse(loc)
	if err != nil {
		return ""
	}
	return base.ResolveReference(ref).String()
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

package http

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	stdhttp "net/http"
	"strings"
)

// maxReasonBytes caps how much of a non-2xx body a Reason parser may see. Servarr's
// validation array is the largest shape harbrr reads and is well under this.
const maxReasonBytes = 64 << 10

// minScrubbableSecret is the shortest credential JSONClient will value-scrub out of
// its own error messages. ScrubValues is a literal strings.ReplaceAll, so a one- or
// two-character "secret" would shred every message it appears in (an api key of "a"
// would redact every letter a). No remote app harbrr talks to issues a credential
// that short, so the guard costs nothing real; below the threshold only the
// name-matched pattern scrub applies.
const minScrubbableSecret = 4

// RawBody is a pre-encoded request body for a remote endpoint that does not take
// JSON — qui's multipart torrent upload is the only one today. Pass it as Do's `in`
// and it is sent verbatim under its own content type instead of being marshalled.
type RawBody struct {
	ContentType string
	Data        []byte
}

// JSONClient issues authenticated JSON requests to ONE remote app (qui, a Servarr,
// cross-seed, Flood) and is the single place the "may a remote's error body reach a
// log?" question is answered. Every app-facing package shares it so the answer cannot
// drift per package again (#553).
//
// The default policy is status-only: a non-2xx yields the status and nothing else,
// because a remote's error body routinely echoes the request harbrr sent — which
// carries the harbrr feed key. Reason opts a caller into an ALLOWLIST of known-safe
// fields (see appsync.parseServarrReason); there is deliberately no raw-body echo.
//
// Every error string it emits is scrubbed twice: the caller's own credential is
// VALUE-scrubbed (ScrubValues — the client holds the secret, so it is the only layer
// that can guarantee a bare echoed value is gone), then the shared name-matched
// pattern scrub runs on top (RedactSecretsInText). The wrapped cause is preserved,
// so errors.Is/As still reach a transport error.
type JSONClient struct {
	// Prefix identifies the caller in every error ("appsync: qui", "download: flood").
	// It is how a user tells the packages apart in a log.
	Prefix string
	// Base is the remote's origin, trailing slashes stripped by NewJSONClient. Every
	// request URL is Base+path, so a path keeps its query string verbatim.
	Base string
	// Auth is added to every request. It is a header set, not a key/value pair,
	// because the callers differ: qui sends X-API-Key, a Servarr X-Api-Key, Flood a
	// jwt Cookie the driver rewrites after each login, and some callers send none.
	Auth stdhttp.Header
	// Client is the transport; its Timeout is the per-request ceiling.
	Client *stdhttp.Client
	// Secret is the credential this client authenticates with. It is scrubbed by
	// VALUE from every emitted error, so a remote that echoes it back cannot leak it.
	Secret string
	// Reason, when non-nil, extracts a redaction-safe reason from a non-2xx body.
	// nil (the default) is status-only. A parser must allowlist known-safe fields —
	// never return raw bytes.
	Reason func(body []byte) string
}

// NewJSONClient normalises c's Base (stripping trailing slashes so Base+path can
// never yield "//api/...") and returns it. This is the one place the normalisation
// lives; every caller previously repeated it, and apps/qui forgot to.
func NewJSONClient(c JSONClient) *JSONClient {
	c.Base = strings.TrimRight(c.Base, "/")
	return &c
}

// Do sends method+path with in as the body (nil = none, a RawBody = verbatim,
// anything else = JSON) and decodes a 2xx response into out (nil = discard). It
// returns the HTTP status — set even on the error path, so a caller can branch on a
// 404/401 — plus a scrubbed error for a transport failure, a non-2xx, or a decode
// failure.
func (c *JSONClient) Do(ctx context.Context, method, path string, in, out any) (int, error) {
	req, err := c.request(ctx, method, path, in)
	if err != nil {
		return 0, err
	}
	// G704: the URL is c.Base (an operator-configured app address, validated at
	// create/update time) plus a fixed path — never end-user input. Reaching that
	// address is the whole point, so this is not attacker-controlled SSRF.
	resp, err := c.Client.Do(req) //nolint:gosec // G704: operator-configured app URL, not user input.
	if err != nil {
		return 0, c.errorf("%s: %s %s: %w", c.Prefix, method, path, ScrubURLError(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp.StatusCode, c.statusError(method, path, resp)
	}
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return resp.StatusCode, c.errorf("%s: decode %s: %w", c.Prefix, path, err)
		}
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode, nil
}

// request builds the authenticated request for one call.
func (c *JSONClient) request(ctx context.Context, method, path string, in any) (*stdhttp.Request, error) {
	var body io.Reader
	var contentType string
	switch v := in.(type) {
	case nil:
	case RawBody:
		body, contentType = bytes.NewReader(v.Data), v.ContentType
	default:
		encoded, err := json.Marshal(in)
		if err != nil {
			return nil, c.errorf("%s: marshal request: %w", c.Prefix, err)
		}
		body, contentType = bytes.NewReader(encoded), "application/json"
	}
	req, err := stdhttp.NewRequestWithContext(ctx, method, c.Base+path, body)
	if err != nil {
		return nil, c.errorf("%s: build request: %w", c.Prefix, ScrubURLError(err))
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	// Add (not a raw map copy) so a header written with a non-canonical spelling —
	// qui's "X-API-Key" — reaches the wire canonicalised, exactly as Header.Set did
	// at every call site before this client existed.
	for name, values := range c.Auth {
		for _, value := range values {
			req.Header.Add(name, value)
		}
	}
	return req, nil
}

// statusError renders a non-2xx. The body is read ONLY when a Reason parser is
// configured, and only that parser's output — never raw bytes — reaches the message.
func (c *JSONClient) statusError(method, path string, resp *stdhttp.Response) error {
	if c.Reason != nil {
		raw, err := io.ReadAll(io.LimitReader(resp.Body, maxReasonBytes))
		if err == nil && len(raw) > 0 {
			if reason := c.Reason(raw); reason != "" {
				return c.errorf("%s: %s %s: status %d: %s", c.Prefix, method, path, resp.StatusCode, reason)
			}
		}
	}
	return c.errorf("%s: %s %s: status %d", c.Prefix, method, path, resp.StatusCode)
}

// errorf formats an error and returns it with its message scrubbed.
//
// Only a GENUINE wrapped cause is retained — the inner error a %w verb named, not
// the formatted error itself. Keeping the latter would leave an UNSCRUBBED copy of
// the whole message (including a parsed remote reason, which can echo the
// credential) reachable through errors.Unwrap, quietly undoing the scrub this type
// exists to guarantee. A format with no %w therefore yields a plain scrubbed error
// with nothing underneath, while %w keeps errors.Is/As working against the cause.
func (c *JSONClient) errorf(format string, args ...any) error {
	err := fmt.Errorf(format, args...)
	cause := errors.Unwrap(err)
	if cause == nil {
		return errors.New(c.scrub(err.Error()))
	}
	return &scrubbedError{msg: c.scrub(err.Error()), cause: cause}
}

// scrub applies the value scrub (this client's own credential) and then the shared
// name-matched pattern scrub.
func (c *JSONClient) scrub(s string) string {
	if len(c.Secret) >= minScrubbableSecret {
		s = ScrubValues(s, []string{c.Secret})
	}
	return RedactSecretsInText(s)
}

// scrubbedError carries a redacted message over an unredacted cause: Error() is the
// only thing that ever reaches a log or an API client, while Unwrap keeps the chain
// (a *url.Error, context.Canceled) intact for errors.Is/As.
type scrubbedError struct {
	msg   string
	cause error
}

func (e *scrubbedError) Error() string { return e.msg }
func (e *scrubbedError) Unwrap() error { return e.cause }

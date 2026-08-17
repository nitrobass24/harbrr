package native

import (
	"context"
	"errors"
	"fmt"
	"io"
	stdhttp "net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/rs/zerolog"

	apphttp "github.com/autobrr/harbrr/internal/http"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/login"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/mapper"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
)

const (
	// defaultMaxBodyBytes caps a Do response body. An API/browse page is small
	// JSON/XML/HTML, so this is generous while still bounding a hostile or runaway
	// body. Drivers with larger pages (the usenet pair) raise Base.MaxBodyBytes.
	defaultMaxBodyBytes = 8 << 20 // 8 MiB
	// maxTorrentBytes caps a DoDownload body. It is far larger than the API cap
	// because a large pack carries megabytes of piece hashes; DoDownload errors with
	// ErrDownloadTooLarge rather than silently truncating a corrupt torrent.
	maxTorrentBytes = 64 << 20
)

// ErrDownloadTooLarge is returned by DoDownload when a fetched torrent exceeds the
// size cap: erroring beats silently truncating, because a truncated .torrent is
// corrupt. Drivers and tests classify with errors.Is.
var ErrDownloadTooLarge = errors.New("download exceeds the size cap")

// ErrBodyRead is returned by BOTH of roundTrip's body-read paths — the API response
// and the download — when the read fails mid-body (status already read, body
// truncated/dropped). The registry's health classifier matches it via errors.Is as a
// TRANSPORT failure (#234) — a connection dying mid-read is not a parse problem — so
// rewording the message below can't silently break that classification.
var ErrBodyRead = errors.New("read response body")

// Base is the shared implementation core a native driver embeds: the per-instance
// wiring every family carries (definition, capabilities, decrypted settings, paced
// doer, normalised base URL, clock, logger) plus the Do/DoDownload transport that
// owns redaction and status classification. A driver adds only its request
// generator and response parser on top — the two pieces that actually differ per
// family (docs/native-indexer-pattern.md).
//
// The fields are exported for direct driver access; they are wired once by NewBase
// and read-only afterwards (MaxBodyBytes may be raised by the driver's New before
// first use).
type Base struct {
	Family  string // package name, prefixes every error this Base emits
	Def     *loader.Definition
	Caps    *mapper.Capabilities
	Cfg     map[string]string
	Doer    search.Doer
	BaseURL string // normalised with a single trailing slash
	Clock   func() time.Time
	Log     zerolog.Logger
	// MaxBodyBytes caps a Do response body, silently truncating past the cap
	// (matching the pre-Base drivers). Defaults to defaultMaxBodyBytes.
	MaxBodyBytes int64
	// captureSecrets are the configured credential VALUES a refused grab's capture is
	// scrubbed with, snapshotted once at construction. It is a snapshot rather than a
	// live loader.SecretValues(Def.Settings, Cfg) read because the transport runs
	// concurrently with a driver that rotates its own credential into Cfg under its
	// own mutex (gazellegames' on-demand passkey) — see Scrub's precondition. The
	// values that snapshot misses (a rotated token) are exactly the ones unsafe to
	// read here; their driver supplies them through DoDownload's runtimeSecrets.
	captureSecrets []string
}

// NewBase wires the scaffold every native driver constructor repeated: nil-def
// guard, capabilities build, base-URL resolution (explicit BaseURL, else the
// definition's first link) normalised to a single trailing slash, and the clock
// default. family is the driver's package name and prefixes every error.
func NewBase(family string, p Params) (Base, error) {
	if p.Def == nil {
		return Base{}, fmt.Errorf("%s: nil definition", family)
	}
	caps, err := mapper.Build(p.Def)
	if err != nil {
		return Base{}, fmt.Errorf("%s: build capabilities for %q: %w", family, p.Def.ID, err)
	}
	base := p.BaseURL
	if base == "" && len(p.Def.Links) > 0 {
		base = p.Def.Links[0]
	}
	if base == "" {
		// Fail fast rather than normalise an empty base into "/": every request
		// would then target a relative URL and fail far from the misconfiguration.
		return Base{}, fmt.Errorf("%s: no base URL: neither Params.BaseURL nor definition links for %q", family, p.Def.ID)
	}
	clock := p.Clock
	if clock == nil {
		clock = time.Now
	}
	return Base{
		Family:         family,
		Def:            p.Def,
		Caps:           caps,
		Cfg:            p.Cfg,
		Doer:           p.Doer,
		BaseURL:        strings.TrimRight(base, "/") + "/",
		Clock:          clock,
		Log:            p.Logger,
		MaxBodyBytes:   defaultMaxBodyBytes,
		captureSecrets: loader.SecretValues(p.Def.Settings, p.Cfg),
	}, nil
}

// Capabilities returns the family's capabilities document.
func (b *Base) Capabilities() *mapper.Capabilities { return b.Caps }

// SupportsOffsetPaging is the Driver method's default: most trackers cannot forward
// offset/limit upstream. The usenet drivers (newznab, nzbindex) override it.
func (b *Base) SupportsOffsetPaging() bool { return false }

// ConsumesSearchMode is the Driver method's default: most families ignore
// search.Query.Mode entirely. newznab, torznab, and animebytes override it.
func (b *Base) ConsumesSearchMode() bool { return false }

// Response is the owned result of a Do/DoDownload round-trip: the status, the
// response headers (Set-Cookie for MyAnonamouse's rotation, Content-Type for a
// grab), and the fully-read, capped, closed body. There is no live connection to
// leak. On a status-classified error Do/DoDownload still return the Response
// (with a nil Body) alongside the error, so a driver that must inspect headers on
// every exchange — MAM captures a rotated mam_id even off a 403 — can.
type Response struct {
	StatusCode int
	Header     stdhttp.Header
	Body       []byte
}

// Classify is an endpoint's status dialect: which HTTP statuses mean "credentials
// bad" versus "back off". 429/503 are always rate-limit statuses
// (search.IsRateLimitStatus); the dialect adds the codes trackers overload —
// most treat 403 as bad credentials, while HDBits and newznab treat it as a
// spent rate budget, and MAM has no 401 at all. It is a required Do/DoDownload
// parameter so classification can never be forgotten, and per-endpoint variance
// (avistaz: 412 on search, 422 on login) is expressed at the call site.
type Classify struct {
	auth       []int
	rateLimit  []int
	authReason string
}

// The three shipped dialects. AlsoAuth/AlsoRateLimited extend them per endpoint.
var (
	// ClassifyAuth403 — the majority dialect: 401/403 are auth failures.
	ClassifyAuth403 = Classify{auth: []int{stdhttp.StatusUnauthorized, stdhttp.StatusForbidden}}
	// ClassifyRateLimit403 — 401 is auth; 403 is a spent query/rate budget
	// (Prowlarr's RequestLimitReached), so it backs off like 429/503 rather than
	// misreporting working credentials as an auth failure.
	ClassifyRateLimit403 = Classify{auth: []int{stdhttp.StatusUnauthorized}, rateLimit: []int{stdhttp.StatusForbidden}}
	// ClassifyAuthOnly403 — cookie-session trackers (MAM): 403 means the session
	// expired; there is no 401.
	ClassifyAuthOnly403 = Classify{auth: []int{stdhttp.StatusForbidden}}
)

// AlsoAuth returns a copy that additionally treats codes as auth failures.
func (c Classify) AlsoAuth(codes ...int) Classify {
	c.auth = append(append([]int(nil), c.auth...), codes...)
	return c
}

// AlsoRateLimited returns a copy that additionally treats codes as rate limits.
func (c Classify) AlsoRateLimited(codes ...int) Classify {
	c.rateLimit = append(append([]int(nil), c.rateLimit...), codes...)
	return c
}

// WithAuthReason returns a copy whose auth-failure error reads
// "<family>: <reason>: login failed" instead of the generic "request
// unauthorized" — for trackers whose auth failure has a more useful diagnosis
// (MAM: "mam_id expired or invalid").
func (c Classify) WithAuthReason(reason string) Classify {
	c.authReason = reason
	return c
}

// statusError maps a non-2xx status through the dialect: an auth code wraps
// login.ErrLoginFailed, a rate-limit code becomes a *search.RateLimitedError
// carrying any Retry-After, and anything else is a plain HTTP-code error. A 2xx
// returns nil. op is "request" or "download" (error wording only).
func (c Classify) statusError(family, op string, resp *stdhttp.Response, clock func() time.Time) error {
	code := resp.StatusCode
	switch {
	case slices.Contains(c.auth, code):
		reason := c.authReason
		if reason == "" {
			reason = op + " unauthorized"
		}
		return fmt.Errorf("%s: %s: %w", family, reason, login.ErrLoginFailed)
	case slices.Contains(c.rateLimit, code) || search.IsRateLimitStatus(code):
		return &search.RateLimitedError{
			StatusCode: code,
			RetryAfter: search.ParseRetryAfter(resp.Header.Get("Retry-After"), clock),
		}
	case search.IsGatewayStatus(code):
		return fmt.Errorf("%s: %s returned HTTP %d: %w", family, op, code, search.ErrGatewayStatus)
	case code < 200 || code >= 300:
		return fmt.Errorf("%s: %s returned HTTP %d", family, op, code)
	}
	return nil
}

// Do performs one API round-trip: it sends the (fully built, auth-injected)
// request through the paced Doer, classifies the status per the dialect, and
// returns the capped, read, closed body. The security posture is structural, not
// per-call-site: a transport error surfaces only the endpoint's scheme://host
// (apphttp.SchemeHost) with the cause routed through apphttp.RedactURLError, so a
// secret-bearing URL can never leak through an error, no matter the caller.
//
// On a classified status the Response (status + headers, nil body) is returned
// alongside the error. The body read silently truncates at MaxBodyBytes,
// matching the pre-Base drivers; use DoDownload for a torrent fetch, which must
// error on truncation instead.
func (b *Base) Do(ctx context.Context, req *stdhttp.Request, c Classify) (*Response, error) {
	return b.roundTrip(ctx, req, c, "request")
}

// DoDownload is Do for the grab path: same transport redaction and status
// classification (op "download" in error text), but the body is read under the
// torrent cap and a body past the cap is ErrDownloadTooLarge rather than a silent
// truncation — a truncated .torrent is corrupt, not shorter. runtimeSecrets adds
// per-request values, such as a rotated Bearer token, to refusal-capture scrubbing.
func (b *Base) DoDownload(ctx context.Context, req *stdhttp.Request, c Classify, runtimeSecrets ...string) (*Response, error) {
	return b.roundTrip(ctx, req, c, "download", runtimeSecrets...)
}

func (b *Base) roundTrip(ctx context.Context, req *stdhttp.Request, c Classify, op string, runtimeSecrets ...string) (*Response, error) {
	resp, err := b.Doer.Do(req.WithContext(ctx))
	if err != nil {
		// The transport error is a *url.Error whose Error() embeds the FULL
		// unredacted URL (which may carry a passkey), so only its scheme://host
		// survives. %w keeps context.Canceled/DeadlineExceeded and the paced
		// client's sentinels detectable through errors.Is.
		werr := fmt.Errorf("%s: %s to %s failed: %w",
			b.Family, op, apphttp.SchemeHost(req.URL.String()), apphttp.RedactURLError(err))
		// Mark the wrap host-redacted only when the cause is PROVABLY scrubbed —
		// a *url.Error just rebuilt host-only, or a paced-client error that arrived
		// pre-redacted — so SanitizeGrabError can tell it apart from free text that
		// may embed a secret-bearing URL and must be flattened.
		var uerr *url.Error
		if errors.As(err, &uerr) || apphttp.IsHostRedacted(err) {
			werr = apphttp.MarkHostRedacted(werr)
		}
		return nil, werr //nolint:wrapcheck // werr is already the fully-shaped, host-redacted transport error; the marker is a transparent Error()/Unwrap() passthrough, and re-wrapping would only add noise
	}
	defer func() { _ = resp.Body.Close() }()

	out := &Response{StatusCode: resp.StatusCode, Header: resp.Header}
	if serr := c.statusError(b.Family, op, resp, b.Clock); serr != nil {
		if op == "download" {
			serr = b.captureRefusal(req, resp, serr, runtimeSecrets...)
		}
		return out, serr
	}
	if op == "download" {
		out.Body, err = readCapped(resp.Body, maxTorrentBytes)
		if err != nil {
			// readCapped's errors are URL-free by construction (it reads an io.Reader
			// and never sees the request URL), so the marker is truthful here and it
			// is what stops sanitizeGrabError flattening the wrap — which would drop
			// the ErrBodyRead a mid-body drop needs to classify as transport (#479).
			return nil, apphttp.MarkHostRedacted(fmt.Errorf("%s: %w", b.Family, err)) //nolint:wrapcheck // the wrap IS the shaped error; the marker is a transparent Error()/Unwrap() passthrough
		}
		return out, nil
	}
	bodyCap := b.MaxBodyBytes
	if bodyCap <= 0 {
		bodyCap = defaultMaxBodyBytes
	}
	out.Body, err = io.ReadAll(io.LimitReader(resp.Body, bodyCap))
	if err != nil {
		return nil, fmt.Errorf("%s: %w: %w", b.Family, ErrBodyRead, err)
	}
	return out, nil
}

// captureRefusal attaches the refused grab's redacted exchange to err, so the
// response body that names WHY a tracker rejected a download reaches the diagnostics
// ring instead of being discarded with the closed body (autobrr/harbrr#465 — MAM's
// 406 is not a classified status, so nothing else retains it). It wraps the status
// error unchanged, leaving every errors.Is classification intact.
//
// Download path only: an API response's body is read and returned to the driver on
// the "request" path, so nothing there is lost. The body read is bounded by
// search.CaptureReadLimit and deliberately best-effort — a partial read still shows
// what the tracker managed to send, and a read failure must never replace the status
// error the caller is classifying.
func (b *Base) captureRefusal(req *stdhttp.Request, resp *stdhttp.Response, err error, runtimeSecrets ...string) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, search.CaptureReadLimit))
	secrets := append(append([]string(nil), b.captureSecrets...), runtimeSecrets...)
	return &search.CaptureError{
		Capture: search.NewCapture(req.Method, req.URL.String(), resp.StatusCode, resp.Header, body, secrets),
		Err:     err,
	}
}

// readCapped reads up to limit bytes, returning ErrDownloadTooLarge when the
// source exceeds the cap and ErrBodyRead when the connection dies mid-body. The
// returned errors never carry the source URL: readCapped only ever sees an
// io.Reader.
func readCapped(r io.Reader, limit int64) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, fmt.Errorf("%w: download: %w", ErrBodyRead, err)
	}
	if int64(len(body)) > limit {
		return nil, ErrDownloadTooLarge
	}
	return body, nil
}

// TestViaSearch is the default credential probe: an empty search either
// authenticates (nil) or surfaces the classified failure. Drivers with a real
// probe endpoint (avistaz's login, newznab's caps) implement Test themselves.
func TestViaSearch(ctx context.Context, s Searcher) error {
	_, err := s.Search(ctx, search.Query{})
	return err
}

// Scrub returns s with every configured secret value redacted: the definition's
// IsSecret-derived config values (loader.SecretValues over b.Def.Settings/b.Cfg)
// plus any extra values the driver supplies — a non-IsSecret field it still submits
// to the tracker (e.g. a username forced into the header/body alongside a real
// secret), or a secret held OUTSIDE b.Cfg (a runtime-rotated session token, a
// typed field cached at construction). It is a native driver's one value-scrub
// chokepoint, replacing the ~13 hand-rolled per-driver ReplaceAll idioms this
// consolidates: same IsSecret-derived correctness the login stage already has,
// the same "[redacted]" placeholder, and the longest-first substring safety
// apphttp.ScrubValues applies unconditionally (previously only passthepopcorn
// bothered to sort).
//
// Precondition: b.Cfg is read here WITHOUT synchronization, matching Base's
// documented contract that Cfg is wired once by NewBase and read-only afterwards.
// A driver that mutates its own Cfg post-construction (GazelleGames, whose
// on-demand download passkey is persisted back into Cfg under its own mutex) MUST
// NOT call Scrub/ScrubErr directly — it needs its own lock-protected snapshot
// before deriving the secret set.
func (b *Base) Scrub(s string, extra ...string) string {
	secrets := loader.SecretValues(b.Def.Settings, b.Cfg)
	if len(extra) > 0 {
		secrets = append(secrets, extra...)
	}
	return apphttp.ScrubValues(s, secrets)
}

// scrubbedError is an error whose displayed message has been value-scrubbed, while
// preserving errors.Is/As traversal to the ORIGINAL error's chain via Unwrap. The
// naive replacement this displaces — scrub the message, then errors.New(msg) — is a
// latent sentinel drop: passthepopcorn and torrentday's prior scrubError both did
// exactly this, so a scrubbed 401 silently stopped satisfying
// errors.Is(err, login.ErrLoginFailed) (the registry's auth_failure health-event
// classification) the moment a credential actually appeared in the message and got
// redacted. Keeping Unwrap pointed at the original error — never rebuilding a fresh
// errors.New — means the sentinel is always still reachable, however deep it is
// wrapped in the original's own chain.
type scrubbedError struct {
	msg string
	err error
}

func (e *scrubbedError) Error() string { return e.msg }
func (e *scrubbedError) Unwrap() error { return e.err }

// ScrubErr is Scrub for an error: it redacts err's message and, only when scrubbing
// actually changed it, wraps the result in scrubbedError so errors.Is/errors.As
// still traverse to err's own sentinel (login.ErrLoginFailed, *search.
// RateLimitedError, context.Canceled, ...). A nil err returns nil; an err whose
// message carried no configured secret is returned UNCHANGED (no wrapper, so its
// identity/type is untouched for a caller that compares it directly).
func (b *Base) ScrubErr(err error, extra ...string) error {
	if err == nil {
		return nil
	}
	msg := b.Scrub(err.Error(), extra...)
	if msg == err.Error() {
		return err
	}
	return &scrubbedError{msg: msg, err: err}
}

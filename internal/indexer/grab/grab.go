package grab

import (
	"context"
	"errors"
	"mime"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/rs/zerolog"

	apphttp "github.com/autobrr/harbrr/internal/http"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/indexer/core"
	"github.com/autobrr/harbrr/internal/secrets"
)

// torrentContentType is what the /dl proxy serves a fetched .torrent as; it also
// gates the serve-boundary bencode check (only torrent bodies are validated).
const torrentContentType = "application/x-bittorrent"

// ErrorWriter renders a ServeGrab failure in the caller's error contract: the feed
// /dl proxy answers in Torznab XML (torznabhttp's torznabGrabError), the management
// route in the api package's JSON envelope. msg is always generic and secret-free.
type ErrorWriter func(w http.ResponseWriter, status int, msg string)

// ServeGrab is the shared resolve-and-stream core behind both the apikey-gated feed
// /dl proxy (torznabhttp's serveDL) and the session-authed management download route
// (GET /api/indexers/{slug}/download/{token}). It decodes the opaque token into the
// pre-resolution link (bound to idx — a token minted for another indexer fails the AAD
// check), grabs the release through harbrr's own session, and streams the
// .torrent/.nzb bytes back — or 302s a resolved magnet, so a passkey-bearing link is
// never exposed. The CALLER authorizes before calling (the feed by apikey; the
// management route by session cookie or X-API-Key) and supplies the ErrorWriter that
// renders failures in its own contract (Torznab XML vs JSON). Every failure is
// generic; the link/passkey never reaches a log, error body, or redirect. A nil
// keyring means the proxy is disabled -> 503. The resolve itself is ResolveGrab, which
// the management "send to download client" route calls without an http.ResponseWriter.
// Byte responses use the token's safe title-derived filename suffixed with the
// indexer ID, falling back to the indexer ID when title metadata is absent.
func ServeGrab(w http.ResponseWriter, r *http.Request, idx core.Indexer, dlToken *secrets.Keyring, log zerolog.Logger, token string, errw ErrorWriter) {
	p, err := ResolveGrab(r.Context(), idx, dlToken, token)
	if err != nil {
		writeGrabError(w, log, idx.Info().ID, p, err, errw)
		return
	}
	if p.Magnet != "" {
		http.Redirect(w, r, p.Magnet, http.StatusFound) //nolint:gosec // G710: ResolveGrab validated the magnet: URI, not a web open-redirect
		return
	}
	w.Header().Set("Content-Type", p.ContentType)
	// Give the browser a sensible download filename. The web UI navigates to this route
	// directly, and the URL's last segment is an opaque token, so without this the file
	// would save under the token with no .torrent/.nzb extension. *arr ignores it (it
	// parses the body), so sharing this with the feed /dl proxy is harmless.
	ext := ".torrent"
	if p.ContentType != torrentContentType {
		ext = ".nzb"
	}
	name := downloadAttachmentName(p.Name, idx.Info().ID)
	w.Header().Set("Content-Disposition", contentDispositionAttachment(name+ext))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(p.Body) //nolint:gosec // G705: torrent file served as application/x-bittorrent, fixed non-HTML content type
}

// contentDispositionAttachment renders an attachment Content-Disposition carrying
// BOTH a plain-ASCII filename and, when the name needs it, the RFC 5987 filename*
// form. mime.FormatMediaType alone emits ONLY filename* for a non-ASCII name — a
// consumer that predates RFC 5987 then sees no filename at all and falls back to
// the URL's last path segment, which here is an opaque token (Jackett, the parity
// target, always ships a plain filename). The name comes from pathologize.Clean or
// an indexer ID, so it carries no quotes, backslashes, or control bytes and can be
// quoted verbatim.
func contentDispositionAttachment(filename string) string {
	plain := `attachment; filename="` + asciiApprox(filename) + `"`
	if isASCII(filename) {
		return plain
	}
	// Reuse the stdlib's RFC 2231/5987 encoder for the extended form; it renders
	// `attachment; filename*=utf-8''…` for any non-ASCII value.
	extended := mime.FormatMediaType("attachment", map[string]string{"filename": filename})
	return plain + strings.TrimPrefix(extended, "attachment")
}

func isASCII(s string) bool {
	for i := range len(s) {
		if s[i] >= utf8.RuneSelf {
			return false
		}
	}
	return true
}

// asciiApprox substitutes '_' for every non-ASCII rune — the legacy-consumer
// fallback next to the exact filename* form.
func asciiApprox(s string) string {
	if isASCII(s) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if r < utf8.RuneSelf {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	return b.String()
}

// writeGrabError renders a ResolveGrab failure through the caller's ErrorWriter and
// logs whatever the operator needs to act on. The served message is always generic;
// the link/passkey never reaches a log, error body, or redirect.
func writeGrabError(w http.ResponseWriter, log zerolog.Logger, indexerID string, p GrabPayload, err error, errw ErrorWriter) {
	switch {
	case errors.Is(err, ErrProxyDisabled):
		// No direct Jackett equivalent (Jackett always has download): the proxy feature
		// is unavailable, so 503 Service Unavailable.
		errw(w, http.StatusServiceUnavailable, "download proxy is not enabled")
	case errors.Is(err, ErrInvalidToken):
		// The error never carries the link; an invalid/forged token is a bad request.
		errw(w, http.StatusBadRequest, "invalid download token")
	case errors.Is(err, ErrNotTorrent):
		log.Warn().
			Str("stage", "grab").
			Str("indexer", indexerID).
			Int("bytes", len(p.Body)).
			Msg("grab produced a non-torrent body (likely an expired session); refusing to serve it as a .torrent")
		errw(w, http.StatusNotFound, "requested torrent is not available")
	default:
		LogInternalError(log, "grab", indexerID, err)
		errw(w, http.StatusInternalServerError, InternalErrorDescription(err))
	}
}

// GrabPayload is a resolved grab: either the fetched .torrent/.nzb Body (with the
// ContentType it must be served/uploaded under) or a Magnet URI — never both.
type GrabPayload struct { //nolint:revive // exported: name carried over verbatim from torznabhttp.GrabPayload (#552 was a pure move); renaming it to grab.Payload is a separate mechanical change.
	Body        []byte
	ContentType string
	Magnet      string
	// Name is the sanitized filename stem sealed with the token. It is empty for
	// legacy tokens and callers that had no release title.
	Name string
}

// The closed set of ResolveGrab failures a caller maps to its own status codes.
// Anything else is an internal failure (the grab itself, or a resolved link that is
// neither servable bytes nor a magnet) and must be reported generically.
var (
	// ErrProxyDisabled means no keyring is configured, so no token can be opened.
	ErrProxyDisabled = errors.New("torznabhttp: download proxy is not enabled")
	// ErrInvalidToken means the token was malformed, forged, or minted for another indexer.
	ErrInvalidToken = errors.New("torznabhttp: invalid download token")
	// ErrNotTorrent means the grab produced bytes that are not a bencoded torrent
	// (typically a login page after an expired session). The returned GrabPayload still
	// carries those bytes so the caller can report their size.
	ErrNotTorrent = errors.New("torznabhttp: grab produced a non-torrent body")
)

// errNonMagnetRedirect is the resolved-link guard: only a magnet (public, no secret)
// may be handed back as a redirect, so a resolved http(s) link can never become an open
// redirect or leak a passkey in Location. It is an internal failure, not a caller-mapped
// one.
var errNonMagnetRedirect = errors.New("grab returned a non-magnet redirect")

// ResolveGrab is the HTTP-free core behind both the feed /dl proxy and the management
// download route: it decodes the opaque token into the pre-resolution link (bound to
// idx — a token minted for another indexer fails the AAD check) and grabs the release
// through harbrr's own session, so a passkey-bearing link is never exposed. The CALLER
// authorizes before calling and maps the returned error to its own contract; no error
// ever carries the link.
//
// The decoded link is trusted because the token is AEAD-authenticated under the keyring
// (only harbrr could mint it) and the endpoint is auth-gated. Plaintext credential mode
// still authenticates download tokens with a process-local transient key, so its tokens
// expire across restarts. We do not host-filter the link: a self-hosted operator may run
// a private/LAN tracker, so a filter would break legitimate setups for little gain.
func ResolveGrab(ctx context.Context, idx core.Indexer, dlToken *secrets.Keyring, token string) (GrabPayload, error) {
	if dlToken == nil {
		return GrabPayload{}, ErrProxyDisabled
	}
	payload, err := decodeDLToken(dlToken, idx.Info().ID, token)
	if err != nil {
		return GrabPayload{}, ErrInvalidToken
	}
	// The sealed category rides through on the context so the stats layer can tally the
	// grab under the release's family without widening the Indexer contract (#403).
	result, err := idx.Grab(core.WithGrabCategory(ctx, payload.CategoryID), payload.Link)
	if err != nil {
		return GrabPayload{}, err //nolint:wrapcheck // the caller renders this generically; wrapping would add link-adjacent context.
	}
	if result.Redirect != "" {
		if !strings.HasPrefix(result.Redirect, "magnet:") {
			return GrabPayload{}, errNonMagnetRedirect
		}
		return GrabPayload{Magnet: result.Redirect, Name: payload.Name}, nil
	}
	ct := result.ContentType
	if ct == "" {
		ct = torrentContentType
	}
	p := GrabPayload{Body: result.Body, ContentType: ct, Name: payload.Name}
	// Serve boundary (Jackett's DownloadController analogue): a torrent body must be a
	// bencoded dictionary before it is served as a .torrent. When the session has
	// expired, the .torrent fetch 302s to the login page and the client follows it
	// (deliberate, matching Jackett), so the login-page HTML can come back with HTTP
	// 200 — refuse to hand that to a consumer as a .torrent. Jackett runs
	// BencodeParser.Parse on the bytes and returns 404 on failure; we mirror that. This
	// gates on the torrent content type only: a magnet is the Redirect branch above, and
	// a usenet .nzb (served as application/x-nzb) is XML, not bencode — neither is
	// bencode-checked.
	if ct == torrentContentType && !isBencodeTorrent(result.Body) {
		return p, ErrNotTorrent
	}
	return p, nil
}

// isBencodeTorrent reports whether body is a bencoded torrent: a top-level bencoded
// dictionary starts with 'd'. This is the serve-boundary equivalent of Jackett's
// BencodeParser.Parse — a cheap, robust sniff (all real .torrents begin with 'd', with
// no leading whitespace) that rejects an empty body and login-page HTML alike.
func isBencodeTorrent(body []byte) bool {
	return len(body) > 0 && body[0] == 'd'
}

// InternalErrorMsg is the fixed client-facing text for any internal failure; the raw
// error only ever reaches the (redacted) log.
const InternalErrorMsg = "internal error processing the request"

// InternalErrorDescription is the 900/500 document's description: search.ErrGatewayStatus's
// fixed, secret-free sentinel text when a reverse proxy/CDN reported the tracker's origin
// unreachable (autobrr/harbrr#307 — the Torznab-feed sibling of the management API's 502
// upstream_unreachable, api/encode.go), so a Torznab consumer's log can act on the failure
// without querying the management API; otherwise the generic InternalErrorMsg. The status
// (500) and code (900) never change — only this description does.
//
// It is exported from here, next to the grab proxy that renders it through a caller's
// ErrorWriter, because the Torznab feed's own 900 document must say exactly the same
// thing: the two surfaces answer for the same indexer failures and may not disagree.
func InternalErrorDescription(err error) string {
	if errors.Is(err, search.ErrGatewayStatus) {
		return search.ErrGatewayStatus.Error()
	}
	return InternalErrorMsg
}

// LogInternalError records a failed indexer request with the error redacted,
// response-free — ServeGrab logs here and answers through its caller-supplied
// ErrorWriter, and the Torznab feed logs its own resolve/search/serialize failures
// through the same call so the two surfaces cannot classify a failure differently.
func LogInternalError(log zerolog.Logger, stage, indexerID string, err error) {
	// "indexer request failed", not "torznab request failed": this layer serves BOTH
	// protocols over the torznab-shaped feed API, and naming the API surface here reads
	// as a protocol mismatch when the indexer is usenet (a newznab driver error inside
	// a "torznab" failure).
	//
	// An open circuit is debug, not error: it is a self-imposed gate, the same fact
	// aggregate.go's classifySkip reports as SkipCircuit and searchcache.go declines to
	// feed to the negative breaker — "harbrr chose not to ask", with no request made and
	// nothing for an operator to act on. The failure that opened the circuit was already
	// logged at error when it happened, and the current state is served by the health
	// API; repeating it per request only buries live errors. A dead tracker on a 30
	// minute poll emits 48 of these a day, for days, until the circuit's daily probe
	// finally succeeds.
	event := log.Error()
	if errors.Is(err, core.ErrCircuitOpen) {
		event = log.Debug()
	}
	event.
		Str("stage", stage).
		Str("indexer", indexerID).
		Str("error", apphttp.RedactError(err)).
		Msg("indexer request failed")
}

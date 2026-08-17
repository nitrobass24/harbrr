package torznabhttp

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/spf13/pathologize"

	"github.com/autobrr/harbrr/internal/secrets"
)

const maxDownloadNameBytes = 255 - len(".torrent")

// dlTokenPayload is the sealed grab metadata, marshalled as compact JSON. The
// leading "{" doubles as the version marker: neither unversioned legacy layout
// (categoryID;link, or a bare link) can start with it, and a future field is a
// one-line struct addition instead of a new delimiter format. The payload is
// AEAD-sealed and never user-visible, so the wire shape costs nothing.
type dlTokenPayload struct {
	CategoryID int    `json:"c"`
	Name       string `json:"n,omitempty"`
	Link       string `json:"l"`
}

func dlTokenPurpose(indexerID string) string {
	return "dl-proxy:" + indexerID
}

// cleanedDefaultName is what pathologize.Clean substitutes when nothing usable
// remains of the input — derived, not hard-coded, so a dep upgrade that renames
// the sentinel can't silently break the emptiness check below.
var cleanedDefaultName = pathologize.Clean("")

// downloadName converts an untrusted release title into a cross-platform filename
// stem. A title with no usable content — blank, or one Clean reduces to its default
// sentinel (`???`, `..`) — stays empty so callers can apply their explicit fallback.
//
// pathologize.Clean caps a name at 255 bytes; we need room for the extension on top,
// so a long title is cut further here. Every cut is re-cleaned: cutting can expose a
// trailing dot or space that Clean had already trimmed, and an unclean stem is a stem
// the decoder drops.
func downloadName(title string) string {
	title = strings.ReplaceAll(title, "\x7f", "")
	if strings.TrimSpace(title) == "" {
		return ""
	}
	name := pathologize.Clean(title)
	if name == cleanedDefaultName && !strings.EqualFold(strings.TrimSpace(title), cleanedDefaultName) {
		return ""
	}
	return truncateDownloadName(name, maxDownloadNameBytes)
}

func truncateDownloadName(name string, maxBytes int) string {
	for len(name) > maxBytes {
		_, size := utf8.DecodeLastRuneInString(name)
		name = pathologize.Clean(name[:len(name)-size])
	}
	return name
}

// downloadAttachmentName adds the source indexer to a title-derived stem so
// equal titles from different indexers do not collide. The title is trimmed
// again to keep the suffix and extension inside the portable filename limit.
//
// indexerID is usually a registry slug (≤64 bytes, already portable), but the
// aggregate feeds serve grabs under ids like `profile:<name>` whose profile name
// has no length cap and whose ':' is not filename-safe — so the id is cleaned on
// the titleless path and the suffix is dropped outright when it alone would blow
// the byte budget (truncateDownloadName must never see a limit below Clean's
// non-empty floor, or it cannot terminate).
func downloadAttachmentName(name, indexerID string) string {
	if name == "" {
		return truncateDownloadName(pathologize.Clean(indexerID), maxDownloadNameBytes)
	}
	suffix := " [" + indexerID + "]"
	if limit := maxDownloadNameBytes - len(suffix); limit >= len(cleanedDefaultName) {
		return truncateDownloadName(name, limit) + suffix
	}
	return truncateDownloadName(name, maxDownloadNameBytes)
}

// parseDLTokenPayload parses the JSON payload and accepts both unversioned legacy
// layouts.
func parseDLTokenPayload(payload string) (dlTokenPayload, error) {
	if !strings.HasPrefix(payload, "{") {
		return parseLegacyDLTokenPayload(payload), nil
	}
	var p dlTokenPayload
	if err := json.Unmarshal([]byte(payload), &p); err != nil {
		return dlTokenPayload{}, errors.New("dl token payload: malformed")
	}
	if p.CategoryID < 0 {
		return dlTokenPayload{}, errors.New("dl token payload: invalid category")
	}
	if p.Link == "" {
		return dlTokenPayload{}, errors.New("dl token payload: missing link")
	}
	// An unusable stem is dropped, never rejected: the payload is AEAD-authenticated
	// under our own keyring, so an unclean name means harbrr minted it wrong (e.g. a
	// pathologize skew across binary versions), not that someone forged it. Failing
	// the token here would turn a cosmetic filename problem into a dead download; the
	// empty stem falls through to the caller's fallback. IsClean also rejects invalid
	// UTF-8 (Clean substitutes U+FFFD, so the round trip differs).
	if len(p.Name) > maxDownloadNameBytes || strings.ContainsRune(p.Name, '\x7f') || !pathologize.IsClean(p.Name) {
		p.Name = ""
	}
	return p, nil
}

func parseLegacyDLTokenPayload(payload string) dlTokenPayload {
	prefix, rest, ok := strings.Cut(payload, ";")
	if !ok {
		return dlTokenPayload{Link: payload}
	}
	id, err := strconv.Atoi(prefix)
	if err != nil {
		return dlTokenPayload{Link: payload}
	}
	return dlTokenPayload{CategoryID: id, Link: rest}
}

// encodeDLToken seals the pre-resolution download link, release parent category, and a
// sanitized title-derived filename stem into an opaque, URL-safe token bound to
// indexerID for the grab-time /dl proxy. The link may carry a passkey, so it must never
// reach the served feed in the clear.
//
// The token is always AEAD ciphertext, including when credential storage explicitly
// runs in plaintext mode. Plaintext mode uses a process-local transient token key, so
// its tokens expire across restarts instead of becoming forgeable. The AEAD purpose
// binds the token to indexerID, preventing cross-indexer replay.
//
// The result is base64url so it drops straight into a query parameter without
// escaping.
func encodeDLToken(kr *secrets.Keyring, indexerID string, categoryID int, title, link string) (string, error) {
	payload, err := json.Marshal(dlTokenPayload{CategoryID: categoryID, Name: downloadName(title), Link: link})
	if err != nil {
		return "", fmt.Errorf("dl token: marshal: %w", err)
	}
	blob, err := kr.SealToken(dlTokenPurpose(indexerID), string(payload))
	if err != nil {
		return "", fmt.Errorf("dl token: encrypt: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString([]byte(blob)), nil
}

// decodeDLToken reverses encodeDLToken. It also decodes the two unversioned legacy
// payloads, leaving their filename stem empty and their missing category as zero. It
// fails when the token is malformed or was not minted for indexerID (an AAD mismatch,
// so a token cannot be replayed across indexers). The error never carries the link or
// filename stem.
func decodeDLToken(kr *secrets.Keyring, indexerID, token string) (dlTokenPayload, error) {
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return dlTokenPayload{}, fmt.Errorf("dl token: decode: %w", err)
	}
	payload, err := kr.OpenToken(dlTokenPurpose(indexerID), string(raw))
	if err != nil {
		return dlTokenPayload{}, fmt.Errorf("dl token: decrypt: %w", err)
	}
	decoded, err := parseDLTokenPayload(payload)
	if err != nil {
		return dlTokenPayload{}, fmt.Errorf("dl token: parse: %w", err)
	}
	return decoded, nil
}

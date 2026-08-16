package torznabhttp

import (
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/spf13/pathologize"

	"github.com/autobrr/harbrr/internal/secrets"
)

const (
	dlTokenV2Prefix      = "v2;"
	maxDownloadNameBytes = 255 - len(".torrent")
)

type dlTokenPayload struct {
	categoryID int
	name       string
	link       string
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
	if strings.TrimSpace(title) == "" {
		return ""
	}
	name := pathologize.Clean(title)
	if name == cleanedDefaultName && !strings.EqualFold(strings.TrimSpace(title), cleanedDefaultName) {
		return ""
	}
	for len(name) > maxDownloadNameBytes {
		_, size := utf8.DecodeLastRuneInString(name)
		name = pathologize.Clean(name[:len(name)-size])
	}
	return name
}

func marshalDLTokenPayload(categoryID int, name, link string) string {
	return dlTokenV2Prefix + strconv.Itoa(categoryID) + ";" +
		base64.RawURLEncoding.EncodeToString([]byte(name)) + ";" + link
}

// parseDLTokenPayload strictly parses versioned payloads and accepts both unversioned
// legacy layouts. A version-looking payload never falls back to legacy parsing.
func parseDLTokenPayload(payload string) (dlTokenPayload, error) {
	if strings.HasPrefix(payload, dlTokenV2Prefix) {
		return parseDLTokenV2Payload(payload)
	}
	if hasDLTokenVersionPrefix(payload) {
		return dlTokenPayload{}, errors.New("dl token payload: unsupported version")
	}
	return parseLegacyDLTokenPayload(payload), nil
}

func parseDLTokenV2Payload(payload string) (dlTokenPayload, error) {
	parts := strings.SplitN(strings.TrimPrefix(payload, dlTokenV2Prefix), ";", 3)
	if len(parts) != 3 {
		return dlTokenPayload{}, errors.New("dl token payload: malformed v2")
	}
	categoryID, err := strconv.Atoi(parts[0])
	if err != nil || categoryID < 0 {
		return dlTokenPayload{}, errors.New("dl token payload: invalid category")
	}
	nameBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return dlTokenPayload{}, errors.New("dl token payload: invalid name")
	}
	// An unusable stem is dropped, never rejected: the payload is AEAD-authenticated
	// under our own keyring, so an unclean name means harbrr minted it wrong, not that
	// someone forged it. Failing the token here would turn a cosmetic filename problem
	// into a dead download; the empty stem falls through to the caller's fallback.
	name := string(nameBytes)
	if !utf8.ValidString(name) || len(name) > maxDownloadNameBytes || !pathologize.IsClean(name) {
		name = ""
	}
	if parts[2] == "" {
		return dlTokenPayload{}, errors.New("dl token payload: missing link")
	}
	return dlTokenPayload{categoryID: categoryID, name: name, link: parts[2]}, nil
}

func hasDLTokenVersionPrefix(payload string) bool {
	prefix, _, ok := strings.Cut(payload, ";")
	if !ok || len(prefix) < 2 || prefix[0] != 'v' {
		return false
	}
	for _, char := range prefix[1:] {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}

func parseLegacyDLTokenPayload(payload string) dlTokenPayload {
	prefix, rest, ok := strings.Cut(payload, ";")
	if !ok {
		return dlTokenPayload{link: payload}
	}
	id, err := strconv.Atoi(prefix)
	if err != nil {
		return dlTokenPayload{link: payload}
	}
	return dlTokenPayload{categoryID: id, link: rest}
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
	blob, err := kr.SealToken(dlTokenPurpose(indexerID), marshalDLTokenPayload(categoryID, downloadName(title), link))
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

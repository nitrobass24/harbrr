package torznab

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"strings"
	"unicode/utf8"
)

// xmlIndent is the per-level indent harbrr uses for its served XML. harbrr
// emits its OWN canonical, deterministic XML (goldens byte-compare harbrr
// output); parity with Jackett is structural — same elements, attributes,
// values and nesting that Sonarr/Radarr parse — not byte-identity with
// Jackett's AngleSharp/XDocument whitespace.
const xmlIndent = "  "

// marshalDocument renders v as a complete XML document: the canonical
// declaration, a newline, then the indented body. encoding/xml never emits the
// declaration itself, and renders an attribute-only element as <e></e> rather
// than <e/>; both are well-formed and parse identically for *arr consumers
// (recorded as a deliberate divergence in testdata/README.md).
func marshalDocument(root string, v any) ([]byte, error) {
	body, err := xml.MarshalIndent(v, "", xmlIndent)
	if err != nil {
		return nil, fmt.Errorf("torznab: marshaling %s document: %w", root, err)
	}
	var buf bytes.Buffer
	buf.Grow(len(xml.Header) + len(body) + 1)
	buf.WriteString(xml.Header) // <?xml version="1.0" encoding="UTF-8"?>\n
	buf.Write(body)
	buf.WriteByte('\n')
	return buf.Bytes(), nil
}

// sanitizeXMLText strips the code points Jackett removes via
// ResultPage.RemoveInvalidXMLChars before serialization, so a control byte
// scraped from a tracker page (e.g. 0x1A) never reaches the encoder. Go's
// encoding/xml would otherwise substitute U+FFFD; Jackett removes the rune
// outright, so harbrr removes it too for parity and clean output. Tab (0x09),
// newline (0x0A) and carriage return (0x0D) are preserved (valid XML).
//
// It walks bytes (not runes) so a genuine, well-formed U+FFFD (the 3-byte
// REPLACEMENT CHARACTER, which Jackett's regex does NOT strip) is preserved,
// while a malformed byte / lone surrogate (which decodes to RuneError with
// size 1) is stripped — matching Jackett's lone-surrogate handling exactly.
func sanitizeXMLText(s string) string {
	if s == "" || !hasInvalidXMLChar(s) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if !stripRune(r, size) {
			b.WriteString(s[i : i+size])
		}
		i += size
	}
	return b.String()
}

// hasInvalidXMLChar reports whether s contains any code point sanitizeXMLText
// would strip (the common-case fast path returns s unchanged).
func hasInvalidXMLChar(s string) bool {
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if stripRune(r, size) {
			return true
		}
		i += size
	}
	return false
}

// stripRune reports whether a decoded rune should be removed: an invalid UTF-8
// byte / lone surrogate (RuneError with a single-byte decode) or one of the
// disallowed XML code points (isInvalidXMLChar). A genuine U+FFFD decodes with
// size 3 and is kept.
func stripRune(r rune, size int) bool {
	if r == utf8.RuneError && size == 1 {
		return true
	}
	return isInvalidXMLChar(r)
}

// isInvalidXMLChar reports whether r is one of the code points Jackett's
// RemoveInvalidXMLChars regex strips: the disallowed C0/C1 control ranges, the
// byte-order mark, and the non-characters U+FFFE/U+FFFF.
func isInvalidXMLChar(r rune) bool {
	switch {
	case r >= 0x00 && r <= 0x08:
		return true
	case r == 0x0B || r == 0x0C:
		return true
	case r >= 0x0E && r <= 0x1F:
		return true
	case r >= 0x7F && r <= 0x9F:
		return true
	case r == 0xFEFF || r == 0xFFFE || r == 0xFFFF:
		return true
	default:
		return false
	}
}

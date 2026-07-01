package http

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"strings"
)

// DecodeErrorDetail renders a REDACTED, actionable one-line diagnostic from a
// json or xml decode failure, safe to embed in an always-on error message. Every
// rendered value is type/position metadata (Go type names, offsets, the stdlib's
// own message) — never a payload value — so the result is safe by construction and
// needs no redaction. The raw body is consulted only for the non-structured
// fallback, which reports its size and shape but NEVER echoes its bytes (a raw,
// possibly secret-bearing snippet is reserved for trace-level tracing elsewhere).
//
// It returns "" for a nil err so a caller can treat "" as "no detail available".
//
// Callers wrap the string into their sentinel at the call site, e.g.
//
//	fmt.Errorf("gazelle: decode browse response: %s: %w",
//	    apphttp.DecodeErrorDetail(err, body), search.ErrParseError)
//
// This lives in internal/http (not the search package that owns ErrParseError)
// because search already imports internal/http; a helper here that referenced
// ErrParseError would create an import cycle.
func DecodeErrorDetail(err error, body []byte) string {
	if err == nil {
		return ""
	}

	var typeErr *json.UnmarshalTypeError
	if errors.As(err, &typeErr) {
		return fmt.Sprintf("field %q: expected %s, got %s at offset %d",
			typeErr.Field, typeErr.Type, jsonKind(typeErr.Value), typeErr.Offset)
	}

	var jsonSyntax *json.SyntaxError
	if errors.As(err, &jsonSyntax) {
		// An HTML login wall decoded as JSON fails at offset 1 with a cryptic
		// "invalid character '<'"; prefer the shape/size summary so the operator
		// sees "it returned HTML, not JSON" directly.
		if looksNonStructured(body) {
			return nonStructuredDetail(body)
		}
		return fmt.Sprintf("invalid JSON at offset %d: %s", jsonSyntax.Offset, jsonSyntax.Error())
	}

	var xmlSyntax *xml.SyntaxError
	if errors.As(err, &xmlSyntax) {
		return fmt.Sprintf("invalid XML at line %d: %s", xmlSyntax.Line, xmlSyntax.Msg)
	}

	// Other structural xml decode errors (unsupported target type, tag-path mismatch)
	// carry only Go-type/tag metadata in their message, no payload.
	var xmlUnsupported *xml.UnsupportedTypeError
	var xmlTagPath *xml.TagPathError
	if errors.As(err, &xmlUnsupported) || errors.As(err, &xmlTagPath) {
		return err.Error()
	}

	return nonStructuredDetail(body)
}

// jsonKind reduces a json.UnmarshalTypeError.Value to its leading JSON-kind token.
// The stdlib formats a numeric mismatch as "number <literal>" (e.g. "number -5"),
// where the literal is response PAYLOAD; keeping only the first token ("number")
// preserves the actionable kind while never echoing a data value. Non-numeric kinds
// ("array", "object", "bool", "string") are single tokens and pass through unchanged.
func jsonKind(value string) string {
	if i := strings.IndexByte(value, ' '); i >= 0 {
		return value[:i]
	}
	return value
}

// nonStructuredDetail describes a body the decoder could not parse as the expected
// structure (e.g. an HTML login wall returned in place of JSON) WITHOUT echoing its
// bytes. It reports the size and a payload-free shape hint (a single leading
// structural byte class), enough for an operator to recognize "the tracker returned
// HTML, not JSON" while leaking nothing.
func nonStructuredDetail(body []byte) string {
	detail := fmt.Sprintf("body was not JSON; %d bytes", len(body))
	switch first, ok := leadingByte(body); {
	case !ok:
		return detail
	case first == '<':
		return detail + ", looks like HTML/XML"
	case first == '{' || first == '[':
		return detail + ", starts as JSON but is malformed"
	default:
		return detail
	}
}

// looksNonStructured reports whether body clearly is not a JSON document — its first
// non-whitespace byte is neither an object nor an array opener (an HTML page opens
// with '<', a plaintext error with a letter). Used to prefer the shape/size summary
// over the JSON decoder's cryptic first-byte complaint.
func looksNonStructured(body []byte) bool {
	first, ok := leadingByte(body)
	return ok && first != '{' && first != '['
}

// leadingByte returns the first non-whitespace byte of body, or ok=false when the
// body is empty or all whitespace.
func leadingByte(body []byte) (byte, bool) {
	trimmed := bytes.TrimLeft(body, " \t\r\n")
	if len(trimmed) == 0 {
		return 0, false
	}
	return trimmed[0], true
}

package encode

import (
	"golang.org/x/text/encoding"
	"golang.org/x/text/transform"
)

// DecodeBody transcodes a response body from the definition's declared charset
// to UTF-8, mirroring Jackett WebResult.ContentString (which decodes
// ContentBytes with the indexer Encoding — the def encoding taking first
// priority over the Content-Type charset). enc is nil for UTF-8/no-encoding
// defs, where the body is returned unchanged (a zero-cost no-op).
//
// This is the ONE transcoder both body-consuming stages call: the search stage
// decodes every parsed response through it, and the login stage decodes every
// response the moment it enters the executor, so a non-ASCII `login.error`
// selector matches the same text Jackett's checkForError sees.
//
// A single-byte charmap decoder never errors (every byte maps to a rune,
// invalid code points to U+FFFD), and every corpus encoding is single-byte; on
// the theoretical error path the best-effort output is returned rather than
// failing the request, matching .NET's GetString, which does not throw here.
func DecodeBody(enc encoding.Encoding, body []byte) []byte {
	if enc == nil {
		return body
	}
	// The error is deliberately dropped: on the theoretical error path the
	// best-effort output is exactly what we want to return anyway.
	out, _, _ := transform.Bytes(enc.NewDecoder(), body)
	return out
}

package search

import (
	"fmt"
	"strings"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/htmlindex"
	"golang.org/x/text/encoding/ianaindex"
	"golang.org/x/text/transform"
)

// ResolveEncoding maps a definition's declared `encoding:` name to the
// transcoder the request/response halves use, reproducing Jackett's
// `Encoding = Encoding.GetEncoding(Definition.Encoding)`.
//
// A nil return means "no transcoding" — an empty name or UTF-8, the common case
// (514 of 556 vendored defs), where both request encoding and response decoding
// are byte-identity no-ops. A non-empty, unresolvable name for a non-UTF-8 def
// is a loud construction error, never a silent UTF-8 fallback: a def that
// declares windows-1251 but whose charset we cannot honour would emit mojibake
// and mis-encoded searches, so it must fail at engine build.
//
// Resolution order matches .NET's codepage semantics: ianaindex.IANA first (it
// resolves iso-8859-1 to true Latin1 — codepage 28591 — exactly as
// Encoding.GetEncoding does, where htmlindex would fold it to windows-1252 per
// the WHATWG standard), then htmlindex as a fallback for the one corpus name
// IANA leaves unresolved (tis-620, which .NET and htmlindex both map to
// windows-874). Between them they cover every `encoding:` value the vendored
// corpus declares: windows-1250/1251/1252/1255/1256/874, iso-8859-1, iso-8859-2,
// and tis-620.
func ResolveEncoding(name string) (encoding.Encoding, error) {
	n := strings.TrimSpace(name)
	if n == "" || strings.EqualFold(n, "utf-8") || strings.EqualFold(n, "utf8") {
		return nil, nil //nolint:nilnil // nil enc + nil err is the documented "no transcoding" signal.
	}
	if enc, err := ianaindex.IANA.Encoding(n); err == nil && enc != nil {
		return enc, nil
	}
	if enc, err := htmlindex.Get(n); err == nil && enc != nil {
		return enc, nil
	}
	return nil, fmt.Errorf("unsupported definition encoding %q", n)
}

// decodeBody transcodes a response body from the definition's declared charset
// to UTF-8, mirroring Jackett WebResult.ContentString (which decodes ContentBytes
// with the indexer Encoding — the def encoding taking first priority over the
// Content-Type charset). enc is nil for UTF-8/no-encoding defs, where the body is
// returned unchanged (a zero-cost no-op). The transcoded bytes then feed the same
// goquery/JSON/XML parsers, so Cyrillic (and other non-Latin) titles land as
// correct UTF-8 instead of U+FFFD.
//
// A single-byte charmap decoder never errors (every byte maps to a rune, invalid
// code points to U+FFFD), and every corpus encoding is single-byte; on the
// theoretical error path the best-effort output is returned rather than failing
// the whole search, matching .NET's GetString, which does not throw here.
func decodeBody(enc encoding.Encoding, body []byte) []byte {
	if enc == nil {
		return body
	}
	// The error is deliberately dropped: on the theoretical error path the
	// best-effort output is exactly what we want to return anyway.
	out, _, _ := transform.Bytes(enc.NewDecoder(), body)
	return out
}

// encodeValue transcodes a request query/body value from UTF-8 to the
// definition's declared charset, returning the codepage bytes as a Go string.
// The existing percent-encoder (encode.WebUtilityEncode) is byte-oriented, so
// feeding it these bytes emits the codepage octets percent-escaped — exactly
// Jackett's GetQueryString(Encoding) / FormUrlEncodedContentWithEncoding on GET
// query values and POST bodies. enc is nil for UTF-8/no-encoding defs, where the
// value is returned unchanged.
//
// Unmappable runes (a char the charset cannot represent) are replaced with the
// charset's substitute via ReplaceUnsupported rather than erroring, matching
// .NET's Encoding.GetBytes fallback; the encoder then never fails for valid
// UTF-8 input.
func encodeValue(enc encoding.Encoding, v string) string {
	if enc == nil || v == "" {
		return v
	}
	out, _, err := transform.String(encoding.ReplaceUnsupported(enc.NewEncoder()), v)
	if err != nil {
		return v
	}
	return out
}

// codepageEncodePairs returns a copy of pairs with every key and value transcoded
// into the definition's charset (encodeValue), so the ordered encoder emits
// codepage query bytes. Applied ONLY to input pairs (GET query params, POST body,
// download.before inputs) — never to the search PATH, which Jackett always
// UTF-8-encodes via WebUtility.UrlEncode. nil enc returns pairs unchanged.
func codepageEncodePairs(enc encoding.Encoding, pairs []kv) []kv {
	if enc == nil {
		return pairs
	}
	out := make([]kv, len(pairs))
	for i, p := range pairs {
		out[i] = kv{key: encodeValue(enc, p.key), value: encodeValue(enc, p.value)}
	}
	return out
}

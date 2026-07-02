package torznabhttp

import (
	"net/http"

	tzn "github.com/autobrr/harbrr/internal/torznab"
)

// Torznab/Newznab error codes (the published spec; Jackett emits the same set).
const (
	codeInvalidAPIKey  = 100 // incorrect user credentials
	codeBadParameter   = 201 // incorrect/unsupported parameter or indexer
	codeNoSuchFunction = 202 // unknown t= value
	codeNotAvailable   = 203 // function not available for this indexer
	codeUnknownError   = 900 // unknown/internal error
)

// Content types: the feed (caps + results) is served as application/rss+xml
// (matching Jackett); error documents as application/xml.
const (
	contentTypeFeed  = "application/rss+xml; charset=utf-8"
	contentTypeError = "application/xml; charset=utf-8"
)

// writeXML writes a Torznab feed (caps or results) with the given status.
func writeXML(w http.ResponseWriter, status int, body []byte) {
	w.Header().Set("Content-Type", contentTypeFeed)
	w.WriteHeader(status)
	// G705: body is serialized Torznab XML with a fixed application/rss+xml content
	// type, consumed by XML parsers (Sonarr/Radarr) — not browser-rendered HTML, so
	// there is no XSS sink.
	_, _ = w.Write(body) //nolint:gosec // G705: XML feed, fixed non-HTML content type
}

// writeError writes a Torznab <error> document. The status code follows
// Jackett's split policy: credential (100) and indexer-resolution (201) gate
// failures return HTTP 200 with the error body so *arr clients parse the <error>
// code rather than treating the response as a hard transport failure; in-request
// errors (202/203) return 400; internal errors (900) return 500. description
// must be secret-free.
func writeError(w http.ResponseWriter, status, code int, description string) {
	w.Header().Set("Content-Type", contentTypeError)
	w.WriteHeader(status)
	_, _ = w.Write(tzn.MarshalError(code, description)) //nolint:gosec // G705: XML error doc, fixed non-HTML content type
}

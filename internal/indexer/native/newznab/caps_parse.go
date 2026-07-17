package newznab

import (
	"encoding/xml"
	"fmt"
	"strconv"
	"strings"

	apphttp "github.com/autobrr/harbrr/internal/http"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/mapper"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
)

// capsRoot is the <caps> document a Newznab server returns for ?t=caps. The parts harbrr
// models are decoded: <limits max= default=> (the upstream's advertised request-count
// limit, #250), <searching> mode children (each carries available + a supportedParams
// list), and the <categories> tree (<category id name> with nested <subcat id name>).
// <server> is purely informational and still not modelled.
type capsRoot struct {
	XMLName    xml.Name      `xml:"caps"`
	Limits     capsLimits    `xml:"limits"`
	Searching  capsSearching `xml:"searching"`
	Categories capsCatList   `xml:"categories"`
}

// capsLimits is the <limits max= default=> element. Attributes are decoded as strings
// (rather than int) because a server may omit either or both, and a bare `int` field
// would silently coerce a missing/blank attribute to 0 instead of "absent" — see
// limitsOrDefault, which treats a non-positive value as absent and falls back to
// Prowlarr's 100/100 default.
type capsLimits struct {
	Max     string `xml:"max,attr"`
	Default string `xml:"default,attr"`
}

// limitsOrDefault converts the parsed <limits> attributes to mapper.Limits, defaulting
// each of max/default to 100 when blank or not a positive integer — mirroring Prowlarr's
// IndexerCapabilities default (NewznabCapabilitiesProvider parses `<limits>` the same way).
func (l capsLimits) limitsOrDefault() mapper.Limits {
	return mapper.Limits{
		Max:     positiveIntOr(l.Max, defaultCapsLimit),
		Default: positiveIntOr(l.Default, defaultCapsLimit),
	}
}

// defaultCapsLimit is the fallback when the upstream omits <limits> or an attribute is
// blank/invalid, matching Prowlarr's IndexerCapabilities default.
const defaultCapsLimit = 100

// positiveIntOr parses raw as a positive int, returning fallback when raw is blank or not
// a positive integer.
func positiveIntOr(raw string, fallback int) int {
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || n <= 0 {
		return fallback
	}
	return n
}

// capsSearching collects every direct child of <searching> by element name. The element
// name (e.g. "tv-search", "audio-search") keys the mode; encoding/xml's `,any` captures all
// children regardless of the exact tag, so a server that emits an unexpected mode tag is
// tolerated (and ignored unless it maps to a known mode).
type capsSearching struct {
	Modes []capsMode `xml:",any"`
}

// capsMode is one <searching> child: its local element name is the wire mode tag, with
// available="yes|no" and supportedParams="q,season,ep,...".
type capsMode struct {
	XMLName         xml.Name
	Available       string `xml:"available,attr"`
	SupportedParams string `xml:"supportedParams,attr"`
}

// isAvailable reports whether the mode element advertises available="yes" (case-insensitive).
func (m capsMode) isAvailable() bool {
	return strings.EqualFold(strings.TrimSpace(m.Available), "yes")
}

// params splits supportedParams on "," and trims each, dropping blanks. The raw strings are
// stored verbatim — harbrr does not validate them at parse time.
func (m capsMode) params() []string {
	return splitParams(m.SupportedParams)
}

// capsCatList is the <categories> container.
type capsCatList struct {
	Categories []capsCategory `xml:"category"`
}

// capsCategory is one <category id name> parent plus its nested <subcat id name> children.
type capsCategory struct {
	ID      string       `xml:"id,attr"`
	Name    string       `xml:"name,attr"`
	Subcats []capsSubcat `xml:"subcat"`
}

// capsSubcat is one <subcat id name> under a parent category.
type capsSubcat struct {
	ID   string `xml:"id,attr"`
	Name string `xml:"name,attr"`
}

// parseCaps decodes a ?t=caps response body. A Newznab error envelope (<error code=".."
// description=".." />, returned even with HTTP 200) is detected first and classified exactly
// like a search error (auth -> login.ErrLoginFailed, rate limit -> RateLimitedError); a
// malformed body is an ErrParseError. The server-controlled description is value-scrubbed of
// the configured apikey as defense in depth (see toError).
func parseCaps(body []byte, apikey string) (*capsRoot, error) {
	if apiErr, ok := capsError(body); ok {
		return nil, apiErr.toError(apikey)
	}
	var root capsRoot
	if err := xml.Unmarshal(body, &root); err != nil {
		return nil, fmt.Errorf("newznab: decode caps response: %s: %w", apphttp.DecodeErrorDetail(err, body), search.ErrParseError)
	}
	if root.XMLName.Local != "caps" {
		return nil, fmt.Errorf("newznab: caps response root is <%s>, want <caps>: %w", root.XMLName.Local, search.ErrParseError)
	}
	return &root, nil
}

// capsError detects a Newznab <error> envelope in a caps response (which may be the document
// root). It reuses the search-side error structs so caps and search classify errors
// identically.
func capsError(body []byte) (*apiError, bool) {
	var feed rss
	if err := xml.Unmarshal(body, &feed); err != nil {
		return nil, false
	}
	if apiErr := feed.firstError(); apiErr != nil {
		return apiErr, true
	}
	return nil, false
}

// splitParams splits a comma-separated supportedParams string into trimmed, non-empty
// tokens.
func splitParams(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

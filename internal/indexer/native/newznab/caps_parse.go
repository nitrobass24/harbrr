package newznab

import (
	"encoding/xml"
	"fmt"
	"strings"

	apphttp "github.com/autobrr/harbrr/internal/http"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
)

// capsRoot is the <caps> document a Newznab server returns for ?t=caps. Only the parts
// harbrr models are decoded: <searching> mode children (each carries available + a
// supportedParams list) and the <categories> tree (<category id name> with nested
// <subcat id name>). <server> and <limits> are informational and intentionally not
// modelled (mapper.Capabilities has no Server/Limits field).
type capsRoot struct {
	XMLName    xml.Name      `xml:"caps"`
	Searching  capsSearching `xml:"searching"`
	Categories capsCatList   `xml:"categories"`
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

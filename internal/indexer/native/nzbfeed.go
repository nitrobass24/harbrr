package native

import (
	"encoding/xml"
	"fmt"
	"strconv"
	"strings"

	apphttp "github.com/autobrr/harbrr/internal/http"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/login"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
)

// The Newznab RSS wire layer, shared by the two drivers that speak it: the usenet
// newznab family and its torrent twin torznab. Both decode the same
// <rss><channel><item>* envelope, the same <error code description/> envelope, the same
// <enclosure>, and the same <ns:attr name value/> rows — they differ only in the
// attribute namespace URI, the family prefix on their errors, and which fields their
// item rows carry. So the envelope, the attr lookup, the error classification and the
// apiPath normalisation live here once, keyed by namespace URI + family prefix, and
// each driver keeps only its own item type and its own release mapping.

// FeedEnclosure is the <enclosure url length type/> element: the download link, a size
// fallback, and the MIME type that says which enclosure is the payload
// (application/x-nzb for newznab, application/x-bittorrent for torznab).
type FeedEnclosure struct {
	URL    string `xml:"url,attr"`
	Length string `xml:"length,attr"`
	Type   string `xml:"type,attr"`
}

// FeedAttr is a <newznab:attr name=".." value=".."/> (or <torznab:attr .../>) element.
// XMLName carries the resolved namespace so a parser can verify the element really is in
// its family's attribute namespace.
type FeedAttr struct {
	XMLName xml.Name
	Name    string `xml:"name,attr"`
	Value   string `xml:"value,attr"`
}

// inNamespace reports whether the attr element is in ns. Some minimal feeds omit the
// namespace binding (XMLName.Space == ""); those are accepted too, so a feed that only
// declares the default RSS namespace still parses — the attr name is still the
// disambiguator.
func (a *FeedAttr) inNamespace(ns string) bool {
	return a.XMLName.Space == ns || a.XMLName.Space == ""
}

// AttrValue returns the trimmed value of the first attr in ns with the given name
// (case-insensitive on the name). A missing attr yields "".
func AttrValue(attrs []FeedAttr, ns, name string) string {
	for i := range attrs {
		if a := &attrs[i]; a.inNamespace(ns) && strings.EqualFold(a.Name, name) {
			return strings.TrimSpace(a.Value)
		}
	}
	return ""
}

// AttrValues returns every non-blank trimmed value of the attrs in ns with the given
// name — the repeatable attrs (category, language).
func AttrValues(attrs []FeedAttr, ns, name string) []string {
	var out []string
	for i := range attrs {
		a := &attrs[i]
		if !a.inNamespace(ns) || !strings.EqualFold(a.Name, name) {
			continue
		}
		if v := strings.TrimSpace(a.Value); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// Feed is the <rss><channel><item>* envelope, generic over the family's own item type
// (the two families' item rows carry different element sets). The <error> element is
// decoded greedily — it can appear at rss or channel level — and no XMLName constraint
// is set so the same struct also decodes a bare <error> document root, which some
// servers return instead of nesting the envelope in rss.
type Feed[I any] struct {
	XMLName xml.Name
	Attrs   []xml.Attr     `xml:",any,attr"`
	Error   *APIError      `xml:"error"`
	Channel FeedChannel[I] `xml:"channel"`
}

// FeedChannel holds the result items and a channel-level error (some servers place
// <error> inside <channel>).
type FeedChannel[I any] struct {
	Error *APIError `xml:"error"`
	Items []I       `xml:"item"`
}

// FirstError returns the first <error> in the feed: a bare <error> document root (whose
// code/description ride the captured root attributes, since the child mapping does not
// match the root element itself), then a child <error> at rss or channel level.
func (f *Feed[I]) FirstError() *APIError {
	return FirstError(f.XMLName, f.Attrs, f.Error, f.Channel.Error)
}

// errorCodeAuthLow / errorCodeAuthHigh bound the Newznab/Torznab "incorrect
// credentials" code range (100-199), which is an auth failure (Prowlarr's
// TorznabRssParser.PreProcess). 200-299 is a bad/missing parameter; 300-399 is a
// content error; 900-999 is a generic/unknown error.
const (
	errorCodeAuthLow  = 100
	errorCodeAuthHigh = 199
)

// APIEnvelopeError maps a Newznab/Torznab <error> envelope to a Go error, prefixed with
// the calling driver's family. A 100-199 code, or a "Request limit reached" /
// apikey-related description, are classified for the registry's health recording: auth
// failures unwrap to login.ErrLoginFailed, rate limits to a RateLimitedError; every
// other code is a generic parse error. The description is server-controlled free text
// that reaches a persisted health event / webhook, so the configured apikey is
// value-scrubbed out of it as defense in depth: a misbehaving server that echoes the
// submitted apikey back in its description must not leak it (a bare "invalid key
// ABCD1234" would pass RedactError's key[=:]value anchor untouched).
//
// quotaCode is the family's own documented request-quota code, promoted to a
// QuotaExceededError; 0 means the family documents none, which is not a real error code
// and so disables the branch. It is a parameter, not a shared constant, because a quota
// code is a per-vendor fact: newznab has dognzb's 910, and no torznab-family site
// documents one, so a torznab 910 must stay the generic parse error it has always been.
func APIEnvelopeError(family string, e *APIError, apikey string, quotaCode int) error {
	desc := apphttp.ScrubValues(strings.TrimSpace(e.Description), []string{apikey})
	if strings.EqualFold(desc, "Request limit reached") {
		return &search.RateLimitedError{StatusCode: 0}
	}
	code, _ := strconv.Atoi(strings.TrimSpace(e.Code))
	if quotaCode != 0 && code == quotaCode {
		return &search.QuotaExceededError{Detail: fmt.Sprintf("%s: api error (code %d): %s", family, code, desc)}
	}
	if (code >= errorCodeAuthLow && code <= errorCodeAuthHigh) || MentionsAPIKey(desc) {
		return fmt.Errorf("%s: auth failed (code %s): %s: %w", family, e.Code, desc, login.ErrLoginFailed)
	}
	return fmt.Errorf("%s: api error (code %s): %s: %w", family, e.Code, desc, search.ErrParseError)
}

// NormalizeAPIPath resolves a configured Newznab/Torznab API path against the family's
// default: a blank value falls back to fallback (Prowlarr's NewznabSettings default
// "/api", which TorznabSettings inherits); a trailing slash is stripped; a missing
// leading slash is added so {base}{apiPath} joins correctly.
func NormalizeAPIPath(raw, fallback string) string {
	p := strings.TrimSpace(raw)
	if p == "" {
		p = fallback
	}
	p = strings.TrimRight(p, "/")
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return p
}

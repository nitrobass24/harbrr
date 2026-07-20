package native

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/normalizer"
)

// FlexInt unmarshals a JSON number or a JSON string into an int64. A malformed or
// out-of-range numeric (including a bare JSON float) degrades to 0 rather than failing
// the whole page — no adopting tracker's wire format sends a float in these fields, and a
// hostile/malformed response must not take down an otherwise-parseable page.
type FlexInt int64

func (n *FlexInt) UnmarshalJSON(b []byte) error {
	if len(b) == 0 || string(b) == "null" {
		*n = 0
		return nil
	}
	s := string(b)
	if b[0] == '"' {
		var str string
		if err := json.Unmarshal(b, &str); err != nil {
			return fmt.Errorf("native: decode numeric field: %w", err)
		}
		s = strings.TrimSpace(str)
	}
	if s == "" {
		*n = 0
		return nil
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		*n = 0 // a malformed numeric degrades to 0, never failing the whole page
		return nil
	}
	*n = FlexInt(v)
	return nil
}

// Int64 returns the decoded value as a plain int64.
func (n FlexInt) Int64() int64 { return int64(n) }

// FlexString unmarshals a JSON string or a JSON number into a string, preserving the
// number's literal text. Every adopter's wire format sends the field as a string but
// tolerates a bare number so a strict struct decode never rejects the body.
type FlexString string

func (s *FlexString) UnmarshalJSON(b []byte) error {
	if len(b) == 0 || string(b) == "null" {
		*s = ""
		return nil
	}
	if b[0] == '"' {
		var str string
		if err := json.Unmarshal(b, &str); err != nil {
			return fmt.Errorf("native: decode string field: %w", err)
		}
		*s = FlexString(str)
		return nil
	}
	*s = FlexString(b) // a bare JSON number: keep its literal text
	return nil
}

// Int64 parses the trimmed FlexString as a base-10 int64; a blank or unparseable value
// yields 0 (a malformed numeric must not fail the whole page — it degrades to 0).
func (s FlexString) Int64() int64 {
	n, err := strconv.ParseInt(s.Str(), 10, 64)
	if err != nil {
		return 0
	}
	return n
}

// Str returns the trimmed string value.
func (s FlexString) Str() string { return strings.TrimSpace(string(s)) }

// SortByPublishDateDesc orders releases by PublishDate descending (Prowlarr's terminal
// OrderByDescending(PublishDate)). PublishDate is UTC RFC3339, which sorts lexically in
// chronological order, so a plain string comparison is correct. The stable sort preserves
// input order for any tie (equal timestamps).
func SortByPublishDateDesc(rels []*normalizer.Release) {
	sort.SliceStable(rels, func(i, j int) bool {
		return rels[i].PublishDate > rels[j].PublishDate
	})
}

// SortByPublishDateDescLinkTiebreak is SortByPublishDateDesc with a Link tiebreak so the
// order is total and deterministic even when two rows share a timestamp.
func SortByPublishDateDescLinkTiebreak(rels []*normalizer.Release) {
	sort.SliceStable(rels, func(i, j int) bool {
		if rels[i].PublishDate != rels[j].PublishDate {
			return rels[i].PublishDate > rels[j].PublishDate
		}
		return rels[i].Link < rels[j].Link
	})
}

// APIError is the Newznab/Torznab error envelope: <error code=".." description=".." />.
// Both are attributes. newznab and torznab each alias their package-local apiError to
// this type (type apiError = native.APIError) so the wire-model structs stay
// package-private per docs/native-indexer-pattern.md while the decode/lookup logic is
// shared.
type APIError struct {
	Code        string `xml:"code,attr"`
	Description string `xml:"description,attr"`
}

// FirstError returns the first <error> found in an rss-shaped feed: a bare <error>
// document root (identified by xmlName), then a child <error> at rss or channel level. A
// bare root carries its code/description on the captured root attributes (the child
// mapping does not match the root element itself).
func FirstError(xmlName xml.Name, attrs []xml.Attr, rootErr, channelErr *APIError) *APIError {
	if strings.EqualFold(xmlName.Local, "error") {
		return APIErrorFromAttrs(attrs)
	}
	if rootErr != nil {
		return rootErr
	}
	return channelErr
}

// APIErrorFromAttrs builds an APIError from the attributes of a bare <error> root
// element.
func APIErrorFromAttrs(attrs []xml.Attr) *APIError {
	e := &APIError{}
	for _, a := range attrs {
		switch strings.ToLower(a.Name.Local) {
		case "code":
			e.Code = a.Value
		case "description":
			e.Description = a.Value
		}
	}
	return e
}

// MentionsAPIKey reports whether the error description references a missing/incorrect
// apikey, which Prowlarr promotes to an auth failure (e.g. code 200 "Missing parameter:
// apikey").
func MentionsAPIKey(desc string) bool {
	return strings.Contains(strings.ToLower(desc), "apikey")
}

// ParseInt64 parses s as a base-10 int64, returning 0 on blank/unparseable input.
func ParseInt64(s string) int64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0
	}
	return n
}

// TrimComments strips a trailing "#comments" fragment from a comments URL, yielding the
// details URL.
func TrimComments(comments string) string {
	return strings.TrimSuffix(strings.TrimSpace(comments), "#comments")
}

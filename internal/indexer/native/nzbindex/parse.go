package nzbindex

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	apphttp "github.com/autobrr/harbrr/internal/http"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/normalizer"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/indexer/native"
)

// apiResponse is the NZBIndex /api/search JSON envelope. `error` is returned with HTTP 200,
// so the body must be inspected even on success. Results live under data.content.
type apiResponse struct {
	Error        bool   `json:"error"`
	ErrorMessage string `json:"errorMessage"`
	Data         struct {
		Content []apiRow `json:"content"`
	} `json:"data"`
}

// apiRow is one release row. Only the fields harbrr maps are decoded; NZBIndex sends more
// (poster, complete, groups) that are not surfaced.
type apiRow struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Posted    int64  `json:"posted"`    // unix seconds
	Size      int64  `json:"size"`      // bytes
	FileCount int64  `json:"fileCount"` // number of files in the collection
}

// titleRe extracts the release title from the article `name`, reproducing Prowlarr's
// ParseTitleRegex: the quoted subject, with a trailing archive/media extension stripped.
// A `name` with no quoted subject (or a `:`/`/` inside it) does not match and is skipped.
var titleRe = regexp.MustCompile(`"([^:/]*?)(?:\.(?:rar|nfo|mkv|par2|001|nzb|url|zip|r[0-9]{2}))?"`)

// parseReleases decodes an NZBIndex search response into normalized releases. It detects the
// error envelope first (returned with HTTP 200), then maps each content row whose title
// parses to a *normalizer.Release. A malformed body is an ErrParseError.
func (d *driver) parseReleases(body []byte) ([]*normalizer.Release, error) {
	var resp apiResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("nzbindex: decode search response: %s: %w", apphttp.DecodeErrorDetail(err, body), search.ErrParseError)
	}
	if resp.Error {
		return nil, fmt.Errorf("nzbindex: api error: %s: %w", scrubSecret(resp.ErrorMessage, d.apikey), search.ErrParseError)
	}
	releases := make([]*normalizer.Release, 0, len(resp.Data.Content))
	for i := range resp.Data.Content {
		if rel := d.toRelease(&resp.Data.Content[i]); rel != nil {
			releases = append(releases, rel)
		}
	}
	native.TraceReleases(d.log, d.def.ID, releases)
	return releases, nil
}

// toRelease maps one content row to a normalized usenet release, or nil when the row has no
// id or its title does not parse (Prowlarr skips both). Seeders/factors stay zero (usenet
// has no ratio economy); the serializer omits them for a usenet feed. The details permalink
// is the stable dedup guid (passkey-free); the .nzb link is a public, keyless download.
func (d *driver) toRelease(row *apiRow) *normalizer.Release {
	id := strings.TrimSpace(row.ID)
	title := parseTitle(row.Name)
	if id == "" || title == "" {
		return nil
	}
	details := d.baseURL + "/collection/" + id
	return &normalizer.Release{
		Title:   title,
		Details: details,
		// The details permalink is the stable, passkey-free dedup identity. Route it
		// through RedactURLIdentity (query/userinfo secrets only, path preserved) as
		// defense in depth without collapsing the per-release id — see the Newznab guid
		// note for why the path must not be redacted.
		GUID:        apphttp.RedactURLIdentity(details),
		Link:        d.baseURL + "/api/download/" + id + ".nzb",
		Categories:  []int{categoryOther},
		Size:        row.Size,
		Files:       row.FileCount,
		PublishDate: formatPosted(row.Posted),
	}
}

// parseTitle extracts and trims the release title from an article name, returning "" when it
// does not match (the row is then skipped).
func parseTitle(name string) string {
	m := titleRe.FindStringSubmatch(name)
	if m == nil {
		return ""
	}
	return strings.TrimSpace(m[1])
}

// formatPosted renders a unix-seconds timestamp as RFC3339 (what the Torznab serializer's
// formatPubDate parses). A non-positive value yields "" so the serializer falls back to now.
func formatPosted(posted int64) string {
	if posted <= 0 {
		return ""
	}
	return time.Unix(posted, 0).UTC().Format(time.RFC3339)
}

// scrubSecret removes a non-empty secret value from s so a server echo cannot leak it,
// mirroring the sibling drivers' value scrub.
func scrubSecret(s, secret string) string {
	s = strings.TrimSpace(s)
	if secret == "" {
		return s
	}
	return strings.ReplaceAll(s, secret, "[redacted]")
}

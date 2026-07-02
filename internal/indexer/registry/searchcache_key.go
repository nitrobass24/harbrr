package registry

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strconv"
	"strings"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
)

// searchCacheSchemaVersion is the cache-key schema version. Bumping it changes
// every key (it is part of the canonical payload), so a payload-shape change
// invalidates all old entries without a data migration. v2 added the Mode field.
const searchCacheSchemaVersion = 2

// searchCacheKeyPayload is the canonical, schema-versioned shape hashed into a
// cache key. It carries the instance id plus every search.Query field that drives
// the tracker request. omitempty makes an empty field hash identically to a
// missing one (empty == missing), so a zero Query and an explicitly-blank Query
// share a key. Offset/Limit are set ONLY for a paging-capable instance (one whose
// driver forwards them upstream, so each page is a distinct outbound request and must
// be cached separately); for a non-paging instance both stay zero and omitempty drops
// them, leaving the key byte-identical to the pre-paging v2 form (one fetch still serves
// every locally-sliced page). Keying off these raw fields — never the engine's rendered
// queryMap/keywords — counts each input exactly once (no Year double-count, no
// derived sentinels).
type searchCacheKeyPayload struct {
	SchemaVersion int      `json:"v"`
	InstanceID    int64    `json:"instance_id"`
	Keywords      string   `json:"keywords,omitempty"`
	Categories    []string `json:"categories,omitempty"`

	IMDBID   string `json:"imdbid,omitempty"`
	TMDBID   string `json:"tmdbid,omitempty"`
	TVDBID   string `json:"tvdbid,omitempty"`
	TVMazeID string `json:"tvmazeid,omitempty"`
	TraktID  string `json:"traktid,omitempty"`
	DoubanID string `json:"doubanid,omitempty"`
	RageID   string `json:"rageid,omitempty"`

	Season string `json:"season,omitempty"`
	Ep     string `json:"ep,omitempty"`
	Year   string `json:"year,omitempty"`

	Artist    string `json:"artist,omitempty"`
	Album     string `json:"album,omitempty"`
	Label     string `json:"label,omitempty"`
	Track     string `json:"track,omitempty"`
	Author    string `json:"author,omitempty"`
	BookTitle string `json:"book_title,omitempty"`

	// Mode is the Torznab search mode (search.Query.Mode). It is in the key because a
	// native driver (AnimeBytes) maps it to a different search namespace, so the same
	// keywords under a different mode are a different outbound request.
	Mode string `json:"mode,omitempty"`

	// Offset and Limit page the cache key, but ONLY for a paging-capable instance: such a
	// driver forwards them upstream, so each page is a distinct outbound request and must
	// not share a cache entry. For a non-paging instance both are left zero (omitempty
	// drops them), preserving the v2 key byte-for-byte so no entry is invalidated.
	Offset int `json:"offset,omitempty"`
	Limit  int `json:"limit,omitempty"`
}

// buildSearchCacheKey is the single key helper used by both the cache-miss path
// and the singleflight/SWR path. It returns hex(sha256(json(canonical payload)))
// over the instance id and the request-driving search.Query fields.
//
// paged folds the query's offset/limit into the key ONLY for a paging-capable instance
// (its driver forwards them upstream, so each page is a distinct outbound request); for a
// non-paging instance paged is false and the offset/limit are dropped, keeping the key
// byte-identical to the pre-paging v2 form.
//
// Keywords are canonicalized with TrimSpace+ToLower: "Foo" and "foo" coalesce to
// one entry (a deliberate, documented divergence — the engine itself does not
// casefold, so the cached result for "foo" is served for "Foo"). Categories are
// canonicalized to a frozen, deduped, numerically-sorted order so a different
// cat= order or a duplicate cat cannot fork the cache; nil and empty []string
// canonicalize identically.
func buildSearchCacheKey(instanceID int64, q search.Query, paged bool) string {
	return keyWithSchemaVersion(searchCacheSchemaVersion, instanceID, q, paged)
}

// keyWithSchemaVersion is the version-parameterized core of buildSearchCacheKey.
// Production always calls it with searchCacheSchemaVersion; tests use it to prove
// a version bump changes every key.
func keyWithSchemaVersion(version int, instanceID int64, q search.Query, paged bool) string {
	payload := searchCacheKeyPayload{
		SchemaVersion: version,
		InstanceID:    instanceID,
		Keywords:      strings.ToLower(strings.TrimSpace(q.Keywords)),
		Categories:    canonicalCategories(q.Categories),
		IMDBID:        q.IMDBID,
		TMDBID:        q.TMDBID,
		TVDBID:        q.TVDBID,
		TVMazeID:      q.TVMazeID,
		TraktID:       q.TraktID,
		DoubanID:      q.DoubanID,
		RageID:        q.RageID,
		Season:        q.Season,
		Ep:            q.Ep,
		Year:          q.Year,
		Artist:        q.Artist,
		Album:         q.Album,
		Label:         q.Label,
		Track:         q.Track,
		Author:        q.Author,
		BookTitle:     q.BookTitle,
		Mode:          q.Mode,
	}
	if paged {
		payload.Offset = q.Offset
		payload.Limit = q.Limit
	}
	// json.Marshal of a fixed struct is deterministic (field order is the struct
	// order) and cannot error for these scalar/[]string fields.
	raw, _ := json.Marshal(payload)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// canonicalCategories returns a deduped copy of the tracker category ids in a
// frozen order: numerically ascending, with non-numeric (custom) ids sorted
// lexically AFTER all numeric ones. The input order is already deterministic per
// request, so sorting cannot manufacture a false hit; sorting just makes the
// canonical form stable and human-auditable. Returns nil for nil/empty input so
// nil and empty []string hash identically (both omit the categories segment).
func canonicalCategories(cats []string) []string {
	if len(cats) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(cats))
	out := make([]string, 0, len(cats))
	for _, c := range cats {
		if _, ok := seen[c]; ok {
			continue
		}
		seen[c] = struct{}{}
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool {
		ni, erri := strconv.Atoi(out[i])
		nj, errj := strconv.Atoi(out[j])
		switch {
		case erri == nil && errj == nil:
			// Tie-break on the original string when two ids are numerically equal
			// (e.g. "1" and "01") — sort.Slice is not stable, so without this the
			// canonical order, and thus the cache key, would vary between runs.
			if ni == nj {
				return out[i] < out[j]
			}
			return ni < nj
		case erri == nil:
			// numeric sorts before non-numeric (custom) ids.
			return true
		case errj == nil:
			return false
		default:
			return out[i] < out[j]
		}
	})
	return out
}

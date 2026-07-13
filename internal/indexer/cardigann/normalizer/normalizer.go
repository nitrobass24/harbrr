package normalizer

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/mapper"
)

// Release is harbrr's canonical, deterministic normalized release: the
// parity-comparison unit produced from one extracted+filtered field row. It
// mirrors Jackett's ReleaseInfo (after BaseIndexer.FixResults synthesis) with a
// FIXED field order and explicit JSON tags so Marshal is byte-deterministic.
//
// Size and the two volume factors deliberately carry NO omitempty: 0 is a
// meaningful value (a freeleech downloadvolumefactor of 0.0, a zero-byte
// release) and omitempty would silently drop it, corrupting parity.
type Release struct {
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	Details     string `json:"details,omitempty"`
	Comments    string `json:"comments,omitempty"`
	Link        string `json:"link,omitempty"`
	Magnet      string `json:"magnet,omitempty"`
	InfoHash    string `json:"infohash,omitempty"`

	// GUID is the indexer's canonical upstream release id (e.g. a Newznab <guid>),
	// when one is supplied. It is the invariant dedup identity — preferred over the
	// acquisition link by GUIDFor so *arr grab-history stays stable even when a
	// server issues volatile/time-limited download URLs. Empty for indexers that
	// expose no stable id; the link/magnet then serves as the guid (Jackett's
	// behavior). Must be passkey-free.
	GUID string `json:"guid,omitempty"`

	Size       int64 `json:"size"`
	Categories []int `json:"categories,omitempty"`

	Seeders  int64 `json:"seeders"`
	Leechers int64 `json:"leechers"`
	Peers    int64 `json:"peers"`
	Grabs    int64 `json:"grabs,omitempty"`
	Files    int64 `json:"files,omitempty"`

	PublishDate string `json:"publishDate,omitempty"`

	DownloadVolumeFactor float64 `json:"downloadVolumeFactor"`
	UploadVolumeFactor   float64 `json:"uploadVolumeFactor"`
	MinimumRatio         float64 `json:"minimumRatio,omitempty"`
	MinimumSeedTime      int64   `json:"minimumSeedTime,omitempty"`

	IMDBID   string `json:"imdbid,omitempty"`
	TMDBID   int64  `json:"tmdbid,omitempty"`
	TVDBID   int64  `json:"tvdbid,omitempty"`
	TVMazeID int64  `json:"tvmazeid,omitempty"`
	TraktID  int64  `json:"traktid,omitempty"`
	DoubanID int64  `json:"doubanid,omitempty"`
	RageID   int64  `json:"rageid,omitempty"`

	Genre     string `json:"genre,omitempty"`
	Year      int64  `json:"year,omitempty"`
	Poster    string `json:"poster,omitempty"`
	Author    string `json:"author,omitempty"`
	BookTitle string `json:"booktitle,omitempty"`
	Publisher string `json:"publisher,omitempty"`
	Album     string `json:"album,omitempty"`
	Artist    string `json:"artist,omitempty"`
	Label     string `json:"label,omitempty"`
	Track     string `json:"track,omitempty"`
}

// Normalizer turns extracted+filtered field rows into canonical Release
// objects. BaseURL resolves relative links/details/comments/posters (Jackett
// resolvePath); Categories maps tracker category ids/descriptions to newznab
// ids (Jackett MapTrackerCatToNewznab / MapTrackerCatDescToNewznab); Type is the
// definition's indexer type ("private"/"public"/"semi-private"), which gates
// public-magnet synthesis from an info hash (Jackett FixResults: not allowed for
// private sites).
type Normalizer struct {
	BaseURL    string
	Type       string
	Categories *mapper.CategoryMap
}

// Option configures a Normalizer.
type Option func(*Normalizer)

// WithBaseURL sets the base URL used to resolve relative release URLs.
func WithBaseURL(baseURL string) Option {
	return func(n *Normalizer) { n.BaseURL = baseURL }
}

// WithType sets the definition's indexer type ("private"/"public"/
// "semi-private"). Jackett only synthesises a public magnet from an info hash
// when the type is not "private".
func WithType(typ string) Option {
	return func(n *Normalizer) { n.Type = typ }
}

// WithCategoryMap sets the tracker<->newznab category map.
func WithCategoryMap(cm *mapper.CategoryMap) Option {
	return func(n *Normalizer) { n.Categories = cm }
}

// New constructs a Normalizer with the given options.
func New(opts ...Option) *Normalizer {
	n := &Normalizer{}
	for _, opt := range opts {
		opt(n)
	}
	return n
}

// handledFields is the set of STANDARD Cardigann base field names the Release
// model reads. It is the parity contract for the corpus field-coverage census:
// every non-intermediate field name a definition uses must appear here, or a
// corpus field would be silently dropped. Keep it in sync with Release.
var handledFields = map[string]struct{}{
	"title": {}, "description": {}, "details": {}, "comments": {},
	"download": {}, "magnet": {}, "infohash": {},
	"size": {}, "category": {}, "categorydesc": {},
	"seeders": {}, "leechers": {}, "files": {}, "grabs": {},
	"date": {}, "downloadvolumefactor": {}, "uploadvolumefactor": {},
	"minimumratio": {}, "minimumseedtime": {},
	"imdb": {}, "imdbid": {}, "tmdbid": {}, "tvdbid": {}, "tvmazeid": {},
	"traktid": {}, "doubanid": {}, "rageid": {},
	"genre": {}, "year": {}, "poster": {},
	"author": {}, "booktitle": {}, "publisher": {},
	"album": {}, "artist": {}, "label": {}, "track": {},
}

// HandledFields returns the set of STANDARD base field names the Release model
// handles. Exposed for the corpus field-coverage census.
func HandledFields() map[string]struct{} {
	out := make(map[string]struct{}, len(handledFields))
	for k := range handledFields {
		out[k] = struct{}{}
	}
	return out
}

// Default volume factor when a definition does not extract the field. Jackett's
// downstream consumers treat an unset factor as 1.0 (full count); harbrr makes
// that explicit so a real freeleech 0.0 is distinguishable from "absent".
const defaultVolumeFactor = 1.0

// Release builds a canonical Release from one row of extracted+filtered field
// values. It reads only the STANDARD base field names; intermediate Result
// keys ("_x", "x_y") are ignored. The required trio (title, size, seeders) plus
// at least one of category/categorydesc and one of download/magnet/infohash is
// validated; a missing requirement is a loud, secret-free error.
func (n *Normalizer) Release(fields map[string]string) (*Release, error) {
	r := &Release{
		DownloadVolumeFactor: defaultVolumeFactor,
		UploadVolumeFactor:   defaultVolumeFactor,
	}

	n.applyCore(r, fields)
	n.applyNumeric(r, fields)
	applyVolumeFactors(r, fields)
	n.applyCategories(r, fields)
	applyIDs(r, fields)
	applyMedia(r, fields)
	n.applyURLs(r, fields)
	synthesize(r, n.Type)

	if err := validate(r, fields); err != nil {
		return nil, err
	}
	return r, nil
}

// applyCore fills the plain string identity fields.
func (n *Normalizer) applyCore(r *Release, f map[string]string) {
	r.Title = f["title"]
	r.Description = f["description"]
	r.InfoHash = f["infohash"]
	r.PublishDate = f["date"]
}

// applyNumeric fills the lenient-coerced integer fields. Seeders/leechers are
// clamped at Jackett's 5,000,000 sanity cap (#6558); Peers = Seeders + Leechers.
func (n *Normalizer) applyNumeric(r *Release, f map[string]string) {
	r.Size = parseSize(f["size"])
	r.Seeders = clampPeers(coerceLong(f["seeders"]))
	r.Leechers = clampPeers(coerceLong(f["leechers"]))
	r.Peers = r.Seeders + r.Leechers
	r.Files = coerceLong(f["files"])
	r.Grabs = coerceLong(f["grabs"])
	r.MinimumSeedTime = coerceLong(f["minimumseedtime"])
	r.Year = coerceLong(f["year"])
}

// peersSanityCap mirrors Jackett's leechers/seeders < 5000000 guard (#6558):
// an absurd value is treated as 0 rather than propagated.
const peersSanityCap = 5_000_000

func clampPeers(v int64) int64 {
	if v < peersSanityCap {
		return v
	}
	return 0
}

// applyVolumeFactors overrides the 1.0 defaults only when the field is present,
// so a real freeleech 0.0 is preserved and an absent field stays 1.0.
func applyVolumeFactors(r *Release, f map[string]string) {
	if v, ok := f["downloadvolumefactor"]; ok {
		r.DownloadVolumeFactor = coerceDouble(v)
	}
	if v, ok := f["uploadvolumefactor"]; ok {
		r.UploadVolumeFactor = coerceDouble(v)
	}
	if v, ok := f["minimumratio"]; ok {
		r.MinimumRatio = coerceDouble(v)
	}
}

// applyCategories resolves tracker category id and description to sorted,
// de-duplicated newznab ids (Jackett accumulates both sources into one list).
func (n *Normalizer) applyCategories(r *Release, f map[string]string) {
	if n.Categories == nil {
		return
	}
	seen := map[int]struct{}{}
	add := func(ids []int) {
		for _, id := range ids {
			if _, dup := seen[id]; dup {
				continue
			}
			seen[id] = struct{}{}
			r.Categories = append(r.Categories, id)
		}
	}
	add(n.Categories.MapTrackerCatToNewznab(f["category"]))
	add(n.Categories.MapTrackerCatDescToNewznab(f["categorydesc"]))
	sort.Ints(r.Categories)
}

// applyIDs fills the external-id fields via first-digit-run extraction. IMDBID
// is canonicalised to Jackett's "tt0000000" feed form; the rest stay numeric.
func applyIDs(r *Release, f map[string]string) {
	r.IMDBID = formatIMDB(firstIMDB(f))
	r.TMDBID = firstIntRun(f["tmdbid"])
	r.TVDBID = firstIntRun(f["tvdbid"])
	r.TVMazeID = firstIntRun(f["tvmazeid"])
	r.TraktID = firstIntRun(f["traktid"])
	r.DoubanID = firstIntRun(f["doubanid"])
	r.RageID = firstIntRun(f["rageid"])
}

// firstIMDB reads whichever of the imdb/imdbid aliases is present.
func firstIMDB(f map[string]string) int64 {
	if v, ok := f["imdbid"]; ok {
		return firstIntRun(v)
	}
	return firstIntRun(f["imdb"])
}

// formatIMDB renders a numeric IMDb id as Jackett's "tt"+7-digit feed form; a
// zero id (absent) renders empty so it is omitted from JSON.
func formatIMDB(id int64) string {
	if id == 0 {
		return ""
	}
	return fmt.Sprintf("tt%07d", id)
}

// applyMedia fills the descriptive book/music/genre fields. Genre reproduces
// Jackett's delimiter split + underscore->space + comma join.
func applyMedia(r *Release, f map[string]string) {
	r.Genre = normalizeGenre(f["genre"])
	r.Author = f["author"]
	r.BookTitle = f["booktitle"]
	r.Publisher = f["publisher"]
	r.Album = f["album"]
	r.Artist = f["artist"]
	r.Label = f["label"]
	r.Track = f["track"]
}

// genreDelimiters mirrors Jackett's split set for the genre field.
const genreDelimiters = ", /)(.;[]\"|:"

// normalizeGenre splits on the Jackett delimiter set, drops empties, replaces
// '_' with ' ', and rejoins with ',' (de-duplicating like Genres.Union).
func normalizeGenre(s string) string {
	if s == "" {
		return ""
	}
	parts := strings.FieldsFunc(s, func(r rune) bool {
		return strings.ContainsRune(genreDelimiters, r)
	})
	seen := map[string]struct{}{}
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		g := strings.ReplaceAll(p, "_", " ")
		if _, dup := seen[g]; dup {
			continue
		}
		seen[g] = struct{}{}
		out = append(out, g)
	}
	return strings.Join(out, ",")
}

// applyURLs resolves the relative-capable URL fields against BaseURL. The
// download field maps to Link unless it is a magnet (handled in synthesize).
func (n *Normalizer) applyURLs(r *Release, f map[string]string) {
	if dl := f["download"]; dl != "" {
		if strings.HasPrefix(dl, "magnet:") {
			r.Magnet = dl
		} else {
			r.Link = resolveURL(n.BaseURL, dl)
		}
	}
	if m := f["magnet"]; m != "" {
		r.Magnet = m
	}
	r.Details = resolveURL(n.BaseURL, f["details"])
	r.Comments = resolveURL(n.BaseURL, f["comments"])
	r.Poster = resolveURL(n.BaseURL, f["poster"])
}

// synthesize reproduces BaseIndexer.FixResults: derive a magnet from an info
// hash (and vice versa) so the two stay consistent, and normalise the info hash
// extracted from a magnet. The info-hash->magnet direction is gated on a
// non-private indexer type ("generate magnet link from info hash (not allowed
// for private sites)"); the magnet->info-hash direction is unconditional.
func synthesize(r *Release, indexerType string) {
	if r.Magnet == "" && r.InfoHash != "" && indexerType != indexerTypePrivate {
		r.Magnet = FromInfoHash(r.InfoHash, r.Title)
	}
	if r.Magnet != "" && strings.TrimSpace(r.InfoHash) == "" {
		r.InfoHash = toInfoHash(r.Magnet)
	}
}

// indexerTypePrivate is Jackett's reserved indexer-type value that disables
// public-magnet synthesis from an info hash.
const indexerTypePrivate = "private"

// Required-field errors are intentionally field-name-only: a release URL can
// carry a passkey, so error strings never echo field values.
var (
	errNoTitle    = errors.New("normalizer: release missing required field: title")
	errNoSize     = errors.New("normalizer: release missing required field: size")
	errNoSeeders  = errors.New("normalizer: release missing required field: seeders")
	errNoCategory = errors.New("normalizer: release missing required field: one of category/categorydesc")
	errNoLink     = errors.New("normalizer: release missing required field: one of download/magnet/infohash")
)

// validate enforces Jackett's release-shape requirements: a title, a size, a
// seeders count, at least one category source, and at least one acquisition
// link. Failures are loud and never leak secret values.
func validate(r *Release, f map[string]string) error {
	if strings.TrimSpace(r.Title) == "" {
		return errNoTitle
	}
	if _, ok := f["size"]; !ok {
		return errNoSize
	}
	if _, ok := f["seeders"]; !ok {
		return errNoSeeders
	}
	if !hasAny(f, "category", "categorydesc") {
		return errNoCategory
	}
	if !hasAny(f, "download", "magnet", "infohash") && r.Magnet == "" {
		return errNoLink
	}
	return nil
}

// hasAny reports whether any of the named keys is present (non-empty) in f.
func hasAny(f map[string]string, keys ...string) bool {
	for _, k := range keys {
		if v, ok := f[k]; ok && strings.TrimSpace(v) != "" {
			return true
		}
	}
	return false
}

// Marshal renders releases as canonical, deterministic JSON: the fixed struct
// field order plus the ascending-sorted Categories make repeated marshals of
// the same input byte-identical. A nil slice marshals as an empty array.
func Marshal(releases []*Release) ([]byte, error) {
	if releases == nil {
		releases = []*Release{}
	}
	b, err := json.Marshal(releases)
	if err != nil {
		return nil, fmt.Errorf("normalizer: marshaling releases: %w", err)
	}
	return b, nil
}

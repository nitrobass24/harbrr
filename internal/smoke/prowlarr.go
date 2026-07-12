package smoke

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// This file is the smoke suite's Prowlarr *oracle*: the client that resolves and searches
// Prowlarr indexers, plus the name-reconciliation (aliases + normalized matching) needed to
// pair a harbrr indexer with its Prowlarr counterpart. It is deliberately separate from the
// parity engine (engine.go) — reconciling two apps' indexer naming is oracle configuration,
// not part of "what counts as a pass."

// prowlarrIndexer is one entry of Prowlarr's /api/v1/indexer list. harbrr and Prowlarr
// name indexers in different namespaces — harbrr's Name is a display name and its slug is
// the def id, while Prowlarr exposes both a user-set Name and the underlying DefinitionName
// (often an "-api" variant) — so matching needs all three fields.
type prowlarrIndexer struct {
	ID             int    `json:"id"`
	Name           string `json:"name"`
	DefinitionName string `json:"definitionName"`
}

// ProwlarrIndexerID resolves a Prowlarr indexer id for a harbrr indexer, matching on its
// display name and slug (see matchProwlarrIndexer). found is false when the oracle has no
// such indexer (the caller then skips the differential — a missing oracle is not a harbrr
// failure).
func ProwlarrIndexerID(ctx context.Context, c *http.Client, base, key, hName, hSlug string) (int, bool, error) {
	body, status, err := httpGet(ctx, c, base+"/api/v1/indexer", map[string]string{"X-Api-Key": key})
	if err != nil {
		return 0, false, err
	}
	if status != http.StatusOK {
		return 0, false, nil
	}
	var idx []prowlarrIndexer
	if err := json.Unmarshal(body, &idx); err != nil {
		return 0, false, fmt.Errorf("parse Prowlarr indexer list: %w", err)
	}
	id, ok := matchProwlarrIndexer(hName, hSlug, idx)
	return id, ok, nil
}

// prowlarrAliases maps a harbrr indexer slug to the name(s) Prowlarr uses for the same
// tracker, for the handful where the two upstreams disagree: harbrr vendors Jackett's defs
// byte-for-byte, while the smoke oracle (Prowlarr) ships its own independently-maintained
// definitions and native indexers, so the same tracker can carry a different name on each
// side. This table lives only in the smoke oracle — it never touches a vendored def — and
// its values are still matched exactly (normalized), so it adds no false-match risk. Add an
// entry whenever a live run reports a genuinely-same tracker as not-comparable.
var prowlarrAliases = map[string][]string{
	"btn":      {"BroadcasTheNet"}, // Jackett "BroadcastTheNet" vs Prowlarr's native "BroadcasTheNet" (typo)
	"filelist": {"FileList.io"},    // Jackett "FileList" vs Prowlarr's native "FileList.io"
}

// matchProwlarrIndexer pairs a harbrr indexer (display name + slug) with a Prowlarr indexer
// using only exact, normalized equality — never fuzzy/prefix matching: a wrong pair would
// make DiffPass compare two different trackers and emit a false FAILURE, which is worse than
// reporting not-comparable.
func matchProwlarrIndexer(hName, hSlug string, list []prowlarrIndexer) (int, bool) {
	name, slug := normKey(hName), normKey(hSlug)
	for _, i := range list {
		if prowlarrIndexerMatches(name, slug, hSlug, i) {
			return i.ID, true
		}
	}
	return 0, false
}

// prowlarrIndexerMatches reports whether one Prowlarr indexer is the same tracker as the
// harbrr indexer, by normalized name/slug equality plus the curated upstream aliases.
func prowlarrIndexerMatches(name, slug, hSlug string, i prowlarrIndexer) bool {
	pName, pDef := normKey(i.Name), normKey(i.DefinitionName)
	switch {
	case name != "" && name == pName, // harbrr display name == Prowlarr display name
		slug != "" && slug == pDef,                            // harbrr slug == Prowlarr def id
		slug != "" && slug == strings.TrimSuffix(pDef, "api"): // Prowlarr "-api" variant
		return true
	}
	for _, a := range prowlarrAliases[hSlug] {
		if na := normKey(a); na != "" && (na == pName || na == pDef) {
			return true
		}
	}
	return false
}

// normKey lowercases and strips everything but [a-z0-9] so cosmetic differences
// ("DigitalCore (API)" vs "digitalcore", "HD-Space" vs "HDspace") compare equal.
func normKey(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// ProwlarrSearch queries Prowlarr's search API for one indexer id. It returns the
// parsed results, the HTTP status, and any transport/parse error; as with HarbrrSearch
// a non-200 yields nil results and a nil error so the caller can skip on an oracle
// hiccup rather than fail harbrr.
func ProwlarrSearch(ctx context.Context, c *http.Client, base, key string, indexerID int, query string) ([]Result, int, error) {
	u := fmt.Sprintf("%s/api/v1/search?query=%s&indexerIds=%d&type=search",
		base, url.QueryEscape(query), indexerID)
	body, status, err := httpGet(ctx, c, u, map[string]string{"X-Api-Key": key})
	if err != nil {
		return nil, status, err
	}
	if status != http.StatusOK {
		return nil, status, nil
	}
	res, err := parseProwlarrResults(body)
	return res, status, err
}

// parseProwlarrResults decodes Prowlarr's /api/v1/search JSON array into the
// comparison Results, capturing the fields the field-level differential compares:
// size, seeders, publish date, the download/magnet URL, and the flattened category
// IDs. It is a pure function so the oracle-side decode is unit-testable, mirroring
// ParseTorznab on the harbrr side.
func parseProwlarrResults(body []byte) ([]Result, error) {
	var rels []struct {
		Title       string `json:"title"`
		Size        int64  `json:"size"`
		Seeders     *int   `json:"seeders"`
		PublishDate string `json:"publishDate"`
		DownloadURL string `json:"downloadUrl"`
		MagnetURL   string `json:"magnetUrl"`
		Categories  []struct {
			ID int `json:"id"`
		} `json:"categories"`
	}
	if err := json.Unmarshal(body, &rels); err != nil {
		return nil, fmt.Errorf("parse Prowlarr search: %w", err)
	}
	out := make([]Result, 0, len(rels))
	for _, r := range rels {
		res := Result{
			Title:       r.Title,
			Size:        r.Size,
			Seeders:     r.Seeders,
			PublishDate: parsePubDate(r.PublishDate),
			DownloadURL: firstNonEmpty(r.DownloadURL, r.MagnetURL),
		}
		for _, cat := range r.Categories {
			res.Categories = append(res.Categories, cat.ID)
		}
		out = append(out, res)
	}
	return out, nil
}

// firstNonEmpty returns the first non-empty string, or "".
func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}

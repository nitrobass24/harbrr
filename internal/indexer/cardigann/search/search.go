// Package search executes a search against a tracker, pages, and collects rows.
//
// One stage of the harbrr Cardigann engine pipeline. It is the executor half:
// it builds the per-path search request from the definition + query (request.go),
// runs it through the injected Doer, and parses the response body into normalized
// releases (fields.go), reproducing Jackett CardigannIndexer.PerformQuery /
// ParseFields / ParseRowFilters on saved bytes. It stays decoupled from the other
// stages by taking them as injected Deps. See AGENTS.md and docs/ideas.md.
package search

import (
	"fmt"
	stdhttp "net/http"
	"time"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/filter"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/login"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/normalizer"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/selector"
)

// responseTypeJSON is the Response.Type that selects the JSON parser; everything
// else (including the empty default) parses as HTML, matching Jackett.
const responseTypeJSON = "json"

// Doer is the narrow HTTP seam the executor drives, identical to login.Doer so a
// single client/replay transport serves both stages. No live network call ever
// happens in this package or its tests.
type Doer interface {
	Do(*stdhttp.Request) (*stdhttp.Response, error)
}

// Deps are the wired pipeline stages the search executor reuses. The engine
// (cardigann.NewEngine) builds and injects the per-def seams — the template
// context, the filter registry's date/language seams, the normalizer's
// base-URL/type/category map — once. The selector is the exception: ParseResults
// installs a fresh one per call, so the reused Engine holds no mutable selector
// state and concurrent searches cannot race on it.
type Deps struct {
	// Selector parses the body and extracts row/field values. ParseResults installs
	// a fresh instance per call (the field loop rebinds its EvalTemplate per row to
	// see the growing Result map), so it carries no state across parses. The engine
	// leaves it nil; do not rely on an injected value.
	Selector *selector.Engine
	// Filters applies each field's filter chain (Apply) with the date/language
	// seams already wired.
	Filters *filter.Registry
	// Normalizer turns the base field map into a canonical Release.
	Normalizer *normalizer.Normalizer
	// Config is the resolved .Config template namespace.
	Config map[string]string
	// BaseURL resolves relative request paths.
	BaseURL string
	// Clock supplies the reference time for the .Today template namespace
	// ({{ .Today.Year }} et al). The engine injects a deterministic clock in
	// tests; nil falls back to time.Now so .Today is never silently empty.
	Clock func() time.Time
}

// ParseResults is the offline extraction half: it parses body per the response
// type, splits it into rows, runs the per-row field loop IN DEFINITION ORDER
// (so a later field template can read .Result.<earlier>), applies the row
// filters against the query, and hands each surviving base-field map to the
// normalizer. No HTTP happens here; it is the deterministic core the engine and
// the parity harness replay saved bytes through.
func ParseResults(def *loader.Definition, body []byte, query Query, deps Deps) ([]*normalizer.Release, error) {
	// Install a fresh selector for this parse. Deps is taken by value, so this is
	// local to the call: the field loop rebinds EvalTemplate per row on THIS
	// instance, never on shared engine state, so concurrent searches on one reused
	// Engine cannot race on the selector.
	deps.Selector = selector.New()

	respType := responseType(def)
	doc, err := parseDocument(deps.Selector, body, respType)
	if err != nil {
		return nil, err
	}

	rows, err := doc.Rows(def.Search.Rows)
	if err != nil {
		return nil, fmt.Errorf("splitting rows: %w", err)
	}

	// Jackett's row loops are asymmetric: the HTML loop wraps each row in a
	// try/catch that drops the offending row and keeps parsing the rest, so one
	// malformed row never throws away the whole page; the JSON loop has no such
	// guard and a required-field miss aborts the entire parse. Mirror that here.
	skipBadRow := respType != responseTypeJSON

	releases := make([]*normalizer.Release, 0, len(rows))
	for i := range rows {
		rel, keep, err := parseRow(def, rows[i], query, deps)
		if err != nil {
			if skipBadRow {
				continue
			}
			return nil, err
		}
		if keep {
			releases = append(releases, rel)
		}
	}
	return releases, nil
}

// parseDocument parses body with the response-type-appropriate backend.
func parseDocument(eng *selector.Engine, body []byte, respType string) (*selector.Document, error) {
	if respType == responseTypeJSON {
		doc, err := eng.ParseJSON(body)
		if err != nil {
			return nil, fmt.Errorf("parsing JSON response: %w", err)
		}
		return doc, nil
	}
	doc, err := eng.ParseHTML(body)
	if err != nil {
		return nil, fmt.Errorf("parsing HTML response: %w", err)
	}
	return doc, nil
}

// responseType returns the effective response type for the search, reading the
// first path's Response.Type (the corpus carries Response on the path block).
// Defaults to HTML.
func responseType(def *loader.Definition) string {
	for i := range def.Search.Paths {
		if r := def.Search.Paths[i].Response; r != nil && r.Type != "" {
			return r.Type
		}
	}
	return ""
}

// Execute runs the full search: build the request(s) from the definition + query,
// drive each through the Doer (carrying the session cookies), and parse the first
// successful response into releases. The session may be nil (no login). It returns
// the normalized releases or a loud, secret-free error.
func Execute(def *loader.Definition, query Query, session *login.Session, doer Doer, deps Deps) ([]*normalizer.Release, error) {
	reqs, err := buildRequests(def, query, deps)
	if err != nil {
		return nil, err
	}

	var out []*normalizer.Release
	for i := range reqs {
		body, err := doRequest(doer, reqs[i], session)
		if err != nil {
			return nil, err
		}
		rels, err := ParseResults(def, body, query, deps)
		if err != nil {
			return nil, err
		}
		out = append(out, rels...)
	}
	return out, nil
}

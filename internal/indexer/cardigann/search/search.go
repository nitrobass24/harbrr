// Package search executes a search against a tracker, pages, and collects rows.
//
// One stage of the harbrr Cardigann engine pipeline. It is the executor half:
// it builds the per-path search request from the definition + query (request.go),
// runs it through the injected Doer, and parses the response body into normalized
// releases (fields.go), reproducing Jackett CardigannIndexer.PerformQuery /
// ParseFields / ParseRowFilters on saved bytes. It stays decoupled from the other
// stages by taking them as injected Deps. See AGENTS.md and docs/architecture.md.
package search

import (
	"context"
	"errors"
	"fmt"
	stdhttp "net/http"
	"strings"
	"time"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/filter"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/login"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/normalizer"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/selector"
)

// responseTypeJSON and responseTypeXML are the Response.Type values that select
// the JSON and XML parsers; everything else (including the empty default) parses
// as HTML, matching Jackett's CardigannIndexer response-mode switch.
const (
	responseTypeJSON = "json"
	responseTypeXML  = "xml"
)

// ErrParseError marks a failure to parse a tracker response (malformed markup, a
// bad selector, or a required-field miss). The registry classifies it into a
// parse_error health event. The engine degrades to empty on missing optional
// fields, so this fires only on a genuine parse/selector failure, never on a
// merely sparse page.
var ErrParseError = errors.New("search: response parse error")

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

// ParseResults is the offline extraction half: it parses body per respType
// (the producing path's Response.Type — "" parses as HTML, Jackett's default),
// splits it into rows, runs the per-row field loop IN DEFINITION ORDER
// (so a later field template can read .Result.<earlier>), applies the row
// filters against the query, and hands each surviving base-field map to the
// normalizer. No HTTP happens here; it is the deterministic core the engine and
// the parity harness replay saved bytes through.
func ParseResults(def *loader.Definition, body []byte, respType string, query Query, deps Deps) ([]*normalizer.Release, error) {
	// Install a fresh selector for this parse. Deps is taken by value, so this is
	// local to the call: the field loop rebinds EvalTemplate per row on THIS
	// instance, never on shared engine state, so concurrent searches on one reused
	// Engine cannot race on the selector.
	deps.Selector = selector.New()

	// Filter the keyword term before any row/field templating, so .Keywords and
	// the andmatch row filter see the same keywordsfilters-filtered value the
	// request was built with (Jackett sets .Keywords once in PerformQuery).
	query, err := applyKeywordsFilters(def, query, deps)
	if err != nil {
		return nil, err
	}

	doc, err := parseDocument(deps.Selector, body, respType)
	if err != nil {
		return nil, err
	}

	rowsBlock, err := renderRowsSelector(def.Search.Rows, query, deps)
	if err != nil {
		return nil, err
	}
	rows, err := doc.Rows(rowsBlock)
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

// parseDocument parses body with the response-type-appropriate backend: JSON,
// XML (a real XML parse, not HTML5), or HTML by default.
func parseDocument(eng *selector.Engine, body []byte, respType string) (*selector.Document, error) {
	switch respType {
	case responseTypeJSON:
		doc, err := eng.ParseJSON(body)
		if err != nil {
			return nil, fmt.Errorf("parsing JSON response: %w", err)
		}
		return doc, nil
	case responseTypeXML:
		doc, err := eng.ParseXML(body)
		if err != nil {
			return nil, fmt.Errorf("parsing XML response: %w", err)
		}
		return doc, nil
	default:
		doc, err := eng.ParseHTML(body)
		if err != nil {
			return nil, fmt.Errorf("parsing HTML response: %w", err)
		}
		return doc, nil
	}
}

// DefaultResponseType returns the definition's leading response type — the
// first path carrying a Response.Type — as the fallback for OFFLINE replay of a
// single saved body whose producing path is unknown (Engine.ParseResponseQuery
// without an explicit override). The live search path never uses it: Execute
// parses each response under its own path's type.
func DefaultResponseType(def *loader.Definition) string {
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
func Execute(ctx context.Context, def *loader.Definition, query Query, session *login.Session, doer Doer, deps Deps) ([]*normalizer.Release, error) {
	reqs, err := buildRequests(def, query, deps)
	if err != nil {
		return nil, err
	}

	var out []*normalizer.Release
	for i := range reqs {
		sr, err := doSearchRequest(ctx, doer, reqs[i], session)
		if err != nil {
			return nil, err
		}
		// Search requests are never auto-followed (Jackett's WebClient semantics):
		// a 3xx is followed manually only when the path opts in via followredirect,
		// is a logged-out signal when the def can re-login, and is parsed as-is
		// otherwise. See resolveRedirect.
		if isRedirectStatus(sr.status) {
			sr, err = resolveRedirect(ctx, doer, reqs[i], sr, def, session)
			if err != nil {
				return nil, err
			}
		}
		body := sr.body
		// Each response is handled under ITS path's response type (Jackett reads
		// SearchPath.Response per request); a mixed HTML+JSON def must never parse
		// one path's body with another path's type.
		respType := reqs[i].respType
		// Lazy login: a logged-out response (login.test selector absent) aborts the
		// parse so the engine can re-login and retry once. Checked before parsing,
		// matching Jackett's CheckIfLoginIsNeeded -> DoLogin order.
		if looksLoggedOut(def, body, respType, query, deps) {
			return nil, ErrSearchLoggedOut
		}
		// A JSON path's noResultsMessage short-circuits row parsing: Jackett checks
		// the raw body against it before JToken.Parse and `continue`s its SearchPath
		// loop, so a matching body is a graceful empty page — never a parse error —
		// while any remaining paths still run.
		if noResultsMatch(reqs[i], sr) {
			continue
		}
		rels, err := ParseResults(def, body, respType, query, deps)
		if err != nil {
			// Mark the parse boundary so the registry classifies it as parse_error
			// (multiple %w keeps both the sentinel and the underlying cause).
			return nil, fmt.Errorf("%w: %w", ErrParseError, err)
		}
		out = append(out, rels...)
	}
	return out, nil
}

// noResultsMatch reports whether a response body is the path's declared
// "no results" answer, reproducing Jackett's JSON-branch check: it applies only
// to json paths on HTTP 200 (Jackett's OK gate), and only when the definition
// sets noResultsMessage. A non-empty message matches the raw body by substring;
// the empty-string form (`noResultsMessage: ""` — czteam-api, superbits,
// digitalcore-api) matches an exactly-empty body, which several JSON APIs
// return for a zero-result query and which would otherwise EOF the JSON parse.
// A nil message (def doesn't declare one) never matches, so genuine parse
// errors still surface.
func noResultsMatch(br builtRequest, sr searchResponse) bool {
	if br.respType != responseTypeJSON || br.noResultsMessage == nil || sr.status != stdhttp.StatusOK {
		return false
	}
	if msg := *br.noResultsMessage; msg != "" {
		return strings.Contains(string(sr.body), msg)
	}
	return len(sr.body) == 0
}

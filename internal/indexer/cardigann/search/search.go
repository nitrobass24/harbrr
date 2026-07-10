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

	"golang.org/x/text/encoding"

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

// ErrTrackerError marks a tracker-authored error page: a Search.Error selector
// matched the HTML response (Jackett's checkForError on the search response), so
// the tracker reported a loud error (e.g. "Database error.", "no permission")
// on an HTTP 200 body rather than results. Jackett throws here and catches it in
// its generic parse-error path (OnParseError); harbrr deliberately treats it as a
// distinct tracker refusal rather than an ErrParseError, because the parse
// succeeded — the tracker refused — and a parse_error health event would misattribute
// the cause. classifyHealth recognizes none of the four health kinds for it, so it
// surfaces as a plain logged search failure carrying the tracker's message.
var ErrTrackerError = errors.New("search: tracker returned an error page")

// Doer is the narrow HTTP seam the executor drives, identical to login.Doer so a
// single client/replay transport serves both stages. No live network call ever
// happens in this package or its tests. The Doer owns the ONE cookie jar (see
// login.Doer's cookie contract); this package never writes Cookie headers.
type Doer interface {
	Do(*stdhttp.Request) (*stdhttp.Response, error)
}

// JarOwner is the optional Doer capability reporting the cookie jar the Doer
// applies to outgoing requests. A wrapper around an *http.Client (the registry's
// paced client) implements it so the engine can seed login cookies into the SAME
// jar the transport uses — keeping exactly one jar on the wire. A bare
// *http.Client needs no method: the engine reads its Jar field directly.
type JarOwner interface {
	CookieJar() stdhttp.CookieJar
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
	// Encoding is the definition's declared charset transcoder (ResolveEncoding),
	// nil for UTF-8/no-encoding defs. When set, response bodies are decoded to
	// UTF-8 before parsing and request query/body values are codepage-encoded,
	// reproducing Jackett's Encoding.GetEncoding(Definition.Encoding).
	Encoding encoding.Encoding
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

	// Decode a non-UTF-8 body to UTF-8 before parsing (Jackett WebResult.ContentString
	// with the def Encoding). This is the shared offline core, so both the live search
	// (Execute passes the raw body) and offline replay (Engine.ParseResponseQuery) get
	// correct UTF-8 selection. A UTF-8/no-encoding def is a no-op.
	body = decodeBody(deps.Encoding, body)

	doc, err := parseDocument(deps.Selector, body, respType)
	if err != nil {
		return nil, err
	}

	// checkForError: on the HTML branch only, a Search.Error selector matching the
	// parsed document is a tracker-authored error page (an error served with HTTP
	// 200 while logged in). Jackett calls checkForError(response, Search.Error)
	// AFTER parsing the document and BEFORE the rows selector, and only in its HTML
	// branch (the JSON and XML branches skip it). Mirror that placement and scope.
	if err := checkSearchError(def, doc, respType, deps.Selector, deps.Config); err != nil {
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
		// Jackett runs the dateheaders backfill after the row survives its filters,
		// before the release is collected; a kept row with no PublishDate looks back
		// for its date header, which may also drop the row (see backfillDateHeader).
		if err == nil && keep {
			err = backfillDateHeader(def, rows[i], rel, query, deps, respType)
		}
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

// checkSearchError reproduces Jackett's checkForError(response, Search.Error) on
// the search response. It runs only on the HTML branch (respType is neither json
// nor xml, matching Jackett, whose JSON and XML branches never call it) and only
// when the definition declares Search.Error selectors. The shared selector helper
// evaluates each error block against the document root; the first match yields a
// loud, message-bearing ErrTrackerError formatted as Jackett's "Error: <message>".
// A match is a tracker refusal, not a parse failure — see ErrTrackerError.
//
// The extracted message is server-controlled free text lifted from the tracker's
// RESPONSE body, so a page that echoes a submitted secret (passkey/apikey/rsskey in
// the search request) would leak it into the returned error / log sink. It is
// value-scrubbed of the configured credentials — derived from the loader's IsSecret
// classifier over the def's settings, the SAME mechanism the login stage uses (see
// login.SecretConfigValues) — before it is wrapped.
func checkSearchError(def *loader.Definition, doc *selector.Document, respType string, eng *selector.Engine, config map[string]string) error {
	if respType == responseTypeJSON || respType == responseTypeXML || len(def.Search.Error) == 0 {
		return nil
	}
	msg, matched, err := eng.CheckErrorBlocks(doc.Root(), def.Search.Error)
	if err != nil {
		return fmt.Errorf("evaluating search error selectors: %w", err)
	}
	if matched {
		scrubbed := login.ScrubSecrets(msg, login.SecretConfigValues(def.Settings, config))
		return fmt.Errorf("%w: Error: %s", ErrTrackerError, scrubbed)
	}
	return nil
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
		// The logged-out and no-results checks run against Jackett's ContentString
		// (the decoded body), so decode here too for a non-UTF-8 def. ParseResults
		// decodes the raw body itself, so it receives the raw sr.body below — never
		// a double-transcode (both decodes read the same raw source). No-op for UTF-8.
		decoded := decodeBody(deps.Encoding, body)
		// Lazy login: a logged-out response (login.test selector absent) aborts the
		// parse so the engine can re-login and retry once. Checked before parsing,
		// matching Jackett's CheckIfLoginIsNeeded -> DoLogin order. The gate uses the
		// redirect-resolved response's WIRE Content-Type (sr.contentType), not the
		// def's declared respType — Jackett reads WebResult.Headers["Content-Type"].
		if looksLoggedOut(def, decoded, sr.contentType, query, deps) {
			return nil, ErrSearchLoggedOut
		}
		// A JSON path's noResultsMessage short-circuits row parsing: Jackett checks
		// the raw body against it before JToken.Parse and `continue`s its SearchPath
		// loop, so a matching body is a graceful empty page — never a parse error —
		// while any remaining paths still run.
		if noResultsMatch(reqs[i], sr.status, decoded) {
			continue
		}
		rels, err := ParseResults(def, body, respType, query, deps)
		if err != nil {
			// A tracker-authored error page (Search.Error matched) is not a parse
			// failure: surface it as-is so it is NOT misclassified as parse_error.
			if errors.Is(err, ErrTrackerError) {
				return nil, err
			}
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
// errors still surface. body is the decoded (UTF-8) response, matching Jackett's
// ContentString substring check.
func noResultsMatch(br builtRequest, status int, body []byte) bool {
	if br.respType != responseTypeJSON || br.noResultsMessage == nil || status != stdhttp.StatusOK {
		return false
	}
	if msg := *br.noResultsMessage; msg != "" {
		return strings.Contains(string(body), msg)
	}
	return len(body) == 0
}

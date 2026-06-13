// Package parity holds the differential/golden harness that pins harbrr's
// Cardigann engine output to Jackett's on the same saved bytes. It is the gate
// the engine must pass — see docs/ideas.md "Definition of done" and AGENTS.md.
//
// A case is a directory under testdata/ holding a case.yml spec plus the files
// it references: a definition (or a vendored-def id), one or more saved
// response bodies, and a golden.json. The harness runs the real engine over the
// saved bytes (offline — no network) and byte-compares the canonical JSON it
// produces against the golden.
//
// Oracle (project decision): goldens are NOT captured from a live Jackett.
// They are either ported from Jackett's own test assertions (golden_source
// jackett-port) or hand-derived from documented Jackett semantics
// (golden_source hand-derived). Each case records its provenance so the gate is
// honest about where each expected value came from.
package parity

import (
	"bytes"
	"encoding/json"
	"fmt"
	stdhttp "net/http"
	"net/http/cookiejar"
	"os"
	"path/filepath"
	"time"

	yaml "go.yaml.in/yaml/v3"

	"github.com/autobrr/harbrr/internal/indexer/cardigann"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
)

// Mode selects how a case drives the engine.
const (
	// ModeParse extracts releases from a single saved response body with no HTTP
	// (Engine.ParseResponseQuery). Use for pure selection/normalization parity.
	// The search mode also pins request construction.
	ModeParse = "parse"
	// ModeSearch drives the full online path (login + request building + parse)
	// against a replay Doer that asserts the engine issued exactly the requests
	// the case declares, so the case pins request construction too.
	ModeSearch = "search"
)

// Provenance records where a golden's expected values came from. A bare fixture
// count is theatre; provenance makes the gate auditable.
const (
	// SourceJackettPort means the expected values are Jackett's own test
	// assertions, ported verbatim (the authoritative offline oracle).
	SourceJackettPort = "jackett-port"
	// SourceHandDerived means the values were computed by hand from documented
	// Jackett semantics (CardigannIndexer.cs), with the reasoning recorded in the
	// case description.
	SourceHandDerived = "hand-derived"
)

// defaultClock is the reference time used when a case omits clock. Fixed so
// date-defaulting templates and relative-date parsing are reproducible.
var defaultClock = time.Date(2024, time.June, 12, 0, 0, 0, 0, time.UTC)

// Case is one parity fixture spec (case.yml). All file references are resolved
// relative to the case directory.
type Case struct {
	// Name is the human label (defaults to the directory name).
	Name string `yaml:"name"`
	// Description explains what the case proves; for hand-derived goldens it
	// carries the derivation reasoning.
	Description string `yaml:"description"`
	// Archetype tags the compatibility-matrix row(s) this case covers, so the
	// success-criteria gate can assert every archetype is exercised.
	Archetype string `yaml:"archetype"`
	// GoldenSource is the provenance of the golden (jackett-port|hand-derived).
	GoldenSource string `yaml:"golden_source"`
	// Mode is parse or search (defaults to parse).
	Mode string `yaml:"mode"`

	// Definition is the def file in the case directory; mutually exclusive with
	// VendorDef.
	Definition string `yaml:"definition"`
	// VendorDef is a vendored definition id loaded via the loader instead of a
	// case-local file.
	VendorDef string `yaml:"vendor_def"`

	// ResponseType overrides the definition's response type ("json"|"") when
	// non-empty (parse mode).
	ResponseType string `yaml:"response_type"`
	// BaseURL overrides the definition's base URL (defaults to the def's first
	// link).
	BaseURL string `yaml:"base_url"`
	// Clock is the RFC3339 reference time injected into the engine; empty uses
	// defaultClock.
	Clock string `yaml:"clock"`

	// Config is the resolved .Config template namespace (tracker settings).
	Config map[string]string `yaml:"config"`
	// Query is the search request the engine is driven with.
	Query CaseQuery `yaml:"query"`

	// Response is the saved body file for parse mode.
	Response string `yaml:"response"`
	// Steps is the ordered HTTP exchange for search mode: each step's request is
	// asserted (method + full URL) and its saved body served.
	Steps []CaseStep `yaml:"steps"`

	// Golden is the expected-output file (defaults to golden.json).
	Golden string `yaml:"golden"`
}

// CaseStep is one expected request/response exchange in search mode. The replay
// transport asserts the engine issued exactly this method + URL (including any
// login probe/request the def implies), then serves Response with Status.
type CaseStep struct {
	// Method is the expected HTTP method (GET/POST), compared case-insensitively.
	Method string `yaml:"method"`
	// URL is the exact expected request URL (request-construction parity).
	URL string `yaml:"url"`
	// Response is the saved body file served for this step.
	Response string `yaml:"response"`
	// Status is the served HTTP status (defaults to 200).
	Status int `yaml:"status"`
	// Note documents the step's role (e.g. "login probe"); harness-ignored.
	Note string `yaml:"note"`
}

// CaseQuery is the subset of the engine Query a case can set, with explicit yaml
// keys so the spec is self-documenting (the engine Query has no yaml tags).
type CaseQuery struct {
	Keywords   string   `yaml:"keywords"`
	Categories []string `yaml:"categories"`
	IMDBID     string   `yaml:"imdbid"`
	TMDBID     string   `yaml:"tmdbid"`
	TVDBID     string   `yaml:"tvdbid"`
	TVMazeID   string   `yaml:"tvmazeid"`
	TraktID    string   `yaml:"traktid"`
	DoubanID   string   `yaml:"doubanid"`
	RageID     string   `yaml:"rageid"`
	Season     string   `yaml:"season"`
	Ep         string   `yaml:"ep"`
	Year       string   `yaml:"year"`
	Artist     string   `yaml:"artist"`
	Album      string   `yaml:"album"`
	Label      string   `yaml:"label"`
	Track      string   `yaml:"track"`
	Author     string   `yaml:"author"`
	BookTitle  string   `yaml:"booktitle"`
}

// toEngine converts the case query into the engine Query.
func (q CaseQuery) toEngine() cardigann.Query {
	return cardigann.Query{
		Keywords:   q.Keywords,
		Categories: q.Categories,
		IMDBID:     q.IMDBID,
		TMDBID:     q.TMDBID,
		TVDBID:     q.TVDBID,
		TVMazeID:   q.TVMazeID,
		TraktID:    q.TraktID,
		DoubanID:   q.DoubanID,
		RageID:     q.RageID,
		Season:     q.Season,
		Ep:         q.Ep,
		Year:       q.Year,
		Artist:     q.Artist,
		Album:      q.Album,
		Label:      q.Label,
		Track:      q.Track,
		Author:     q.Author,
		BookTitle:  q.BookTitle,
	}
}

// Load reads and validates case.yml from dir. Unknown keys are rejected so a
// typo in a spec fails loud rather than silently doing nothing.
func Load(dir string) (*Case, error) {
	data, err := os.ReadFile(filepath.Join(dir, "case.yml")) //nolint:gosec // dir is a test-fixture path under testdata/, supplied by the harness.
	if err != nil {
		return nil, fmt.Errorf("reading case spec: %w", err)
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	var c Case
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("decoding case spec: %w", err)
	}
	if c.Name == "" {
		c.Name = filepath.Base(dir)
	}
	if err := c.validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

// validate enforces the invariants the harness relies on, so a malformed case is
// a loud failure rather than a silent skip.
func (c *Case) validate() error {
	switch c.mode() {
	case ModeParse:
		if c.Response == "" {
			return fmt.Errorf("case %q: parse mode requires response", c.Name)
		}
	case ModeSearch:
		if len(c.Steps) == 0 {
			return fmt.Errorf("case %q: search mode requires steps", c.Name)
		}
	default:
		return fmt.Errorf("case %q: unknown mode %q", c.Name, c.Mode)
	}
	if (c.Definition == "") == (c.VendorDef == "") {
		return fmt.Errorf("case %q: set exactly one of definition / vendor_def", c.Name)
	}
	if c.GoldenSource != SourceJackettPort && c.GoldenSource != SourceHandDerived {
		return fmt.Errorf("case %q: golden_source must be %q or %q", c.Name, SourceJackettPort, SourceHandDerived)
	}
	if c.Archetype == "" {
		return fmt.Errorf("case %q: archetype is required (success-criteria coverage gate)", c.Name)
	}
	return nil
}

// mode returns the effective mode, defaulting to parse.
func (c *Case) mode() string {
	if c.Mode == "" {
		return ModeParse
	}
	return c.Mode
}

// GoldenFile returns the golden filename, defaulting to golden.json.
func (c *Case) GoldenFile() string {
	if c.Golden == "" {
		return "golden.json"
	}
	return c.Golden
}

// Run executes the case against the real engine and returns the canonical,
// indented JSON to compare against the golden. No network call happens.
func (c *Case) Run(dir string) ([]byte, error) {
	def, err := c.loadDef(dir)
	if err != nil {
		return nil, err
	}
	clock, err := c.clockFn()
	if err != nil {
		return nil, err
	}
	opts := []cardigann.Option{cardigann.WithClock(clock)}
	if c.Config != nil {
		opts = append(opts, cardigann.WithConfig(c.Config))
	}
	if c.BaseURL != "" {
		opts = append(opts, cardigann.WithBaseURL(c.BaseURL))
	}

	switch c.mode() {
	case ModeParse:
		return c.runParse(dir, def, opts)
	case ModeSearch:
		return c.runSearch(dir, def, opts)
	default:
		return nil, fmt.Errorf("case %q: unknown mode %q", c.Name, c.Mode)
	}
}

// runSearch drives the full online path against the replay transport, wrapped in
// a real *http.Client with a cookie jar so the production login->search cookie
// flow is exercised. The replay asserts the engine issued exactly the case's
// declared requests; done() surfaces any mismatch or unconsumed step as a loud
// error rather than a silent pass.
func (c *Case) runSearch(dir string, def *loader.Definition, opts []cardigann.Option) ([]byte, error) {
	rep := newReplay(dir, c.Steps)
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, fmt.Errorf("building cookie jar: %w", err)
	}
	client := &stdhttp.Client{Transport: rep, Jar: jar}

	eng, err := cardigann.NewEngine(def, append(opts, cardigann.WithDoer(client))...)
	if err != nil {
		return nil, fmt.Errorf("building engine: %w", err)
	}
	releases, searchErr := eng.Search(c.Query.toEngine())
	// A replay fault is the precise cause; the engine wraps transport errors, so
	// consult the replay first and prefer its (redacted) reason.
	if replayErr := rep.done(); replayErr != nil {
		return nil, replayErr
	}
	if searchErr != nil {
		return nil, fmt.Errorf("search mode: %w", searchErr)
	}
	return canonical(eng, releases)
}

// runParse extracts releases from a single saved body (no HTTP).
func (c *Case) runParse(dir string, def *loader.Definition, opts []cardigann.Option) ([]byte, error) {
	eng, err := cardigann.NewEngine(def, opts...)
	if err != nil {
		return nil, fmt.Errorf("building engine: %w", err)
	}
	body, err := os.ReadFile(filepath.Join(dir, c.Response)) //nolint:gosec // case-fixture path under testdata/.
	if err != nil {
		return nil, fmt.Errorf("reading response body: %w", err)
	}
	releases, err := eng.ParseResponseQuery(body, c.ResponseType, c.Query.toEngine())
	if err != nil {
		return nil, fmt.Errorf("parse mode: %w", err)
	}
	return canonical(eng, releases)
}

// loadDef resolves the case's definition from a case-local file or a vendored id.
func (c *Case) loadDef(dir string) (*loader.Definition, error) {
	if c.VendorDef != "" {
		def, err := loader.New("").Load(c.VendorDef)
		if err != nil {
			return nil, fmt.Errorf("loading vendored def %q: %w", c.VendorDef, err)
		}
		return def, nil
	}
	data, err := os.ReadFile(filepath.Join(dir, c.Definition)) //nolint:gosec // case-fixture path under testdata/.
	if err != nil {
		return nil, fmt.Errorf("reading definition: %w", err)
	}
	def, err := loader.Parse(data)
	if err != nil {
		return nil, fmt.Errorf("parsing definition: %w", err)
	}
	return def, nil
}

// clockFn builds the deterministic clock from the case's RFC3339 clock, or the
// package default when unset.
func (c *Case) clockFn() (func() time.Time, error) {
	t := defaultClock
	if c.Clock != "" {
		parsed, err := time.Parse(time.RFC3339, c.Clock)
		if err != nil {
			return nil, fmt.Errorf("case %q: parsing clock %q: %w", c.Name, c.Clock, err)
		}
		t = parsed
	}
	return func() time.Time { return t }, nil
}

// canonical renders releases as the engine's canonical JSON, re-indented for
// readable goldens and diffs. json.Indent only adds whitespace, so the byte
// content (field order, nil-vs-empty) the normalizer guarantees is preserved.
func canonical(eng *cardigann.Engine, releases []*cardigann.Release) ([]byte, error) {
	compact, err := eng.ResultsJSON(releases)
	if err != nil {
		return nil, fmt.Errorf("marshaling results: %w", err)
	}
	var buf bytes.Buffer
	if err := json.Indent(&buf, compact, "", "  "); err != nil {
		return nil, fmt.Errorf("indenting results: %w", err)
	}
	buf.WriteByte('\n')
	return buf.Bytes(), nil
}

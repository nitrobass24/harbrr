package search

import (
	"errors"
	"testing"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/internal/selector"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
)

// noResultsDef builds a minimal JSON-path definition whose single search path
// declares the given noResultsMessage (nil = absent). No fields block: the
// graceful cases never reach ParseResults, and the error cases fail in the
// JSON parse before any row/field work.
func noResultsDef(msg *string) *loader.Definition {
	return &loader.Definition{
		Links: []string{"https://nrm.invalid/"},
		Search: loader.Search{
			Paths: []loader.SearchPathBlock{{
				Path:     "/api",
				Response: &loader.ResponseBlock{Type: "json", NoResultsMessage: msg},
			}},
			Rows: loader.RowsBlock{Selector: "results"},
		},
	}
}

// TestNoResultsMatch pins the exact Jackett condition: json paths only, HTTP
// 200 only, nil message never matches, a non-empty message matches by
// substring, and the empty-string form matches ONLY an exactly-empty body
// (whitespace does not match — Jackett compares results == string.Empty).
func TestNoResultsMatch(t *testing.T) {
	t.Parallel()

	empty, msg := "", "No results found"
	tests := []struct {
		name     string
		respType string
		message  *string
		status   int
		body     string
		want     bool
	}{
		{"empty form, empty body", responseTypeJSON, &empty, 200, "", true},
		{"empty form, whitespace body", responseTypeJSON, &empty, 200, " ", false},
		{"empty form, non-empty body", responseTypeJSON, &empty, 200, "garbage", false},
		{"message contained in body", responseTypeJSON, &msg, 200, `Sorry — No results found.`, true},
		{"message absent from body", responseTypeJSON, &msg, 200, "something else", false},
		{"nil message never matches", responseTypeJSON, nil, 200, "", false},
		{"html path never matches", "", &empty, 200, "", false},
		{"xml path never matches", responseTypeXML, &empty, 200, "", false},
		{"non-200 never matches", responseTypeJSON, &empty, 302, "", false},
		{"201 is not OK (Jackett gates on exactly 200)", responseTypeJSON, &empty, 201, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			br := builtRequest{respType: tt.respType, noResultsMessage: tt.message}
			if got := noResultsMatch(br, tt.status, []byte(tt.body)); got != tt.want {
				t.Errorf("noResultsMatch = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestExecute_NoResultsMessage drives Execute end-to-end: a body matching the
// path's noResultsMessage is a graceful ZERO-RELEASE page (nil error — no
// parse_error health event), while a non-matching unparseable body and a def
// without the message still surface ErrParseError. czteam-api, superbits, and
// digitalcore-api all use the empty-string form, whose empty body used to EOF
// the JSON parse.
func TestExecute_NoResultsMessage(t *testing.T) {
	t.Parallel()

	empty, msg := "", "No results found"
	tests := []struct {
		name      string
		message   *string
		body      string
		wantParse bool // expect ErrParseError
	}{
		{"empty form intercepts empty body", &empty, "", false},
		{"empty form does not mask a garbage body", &empty, "garbage", true},
		{"message form intercepts a matching unparseable body", &msg, "Sorry, No results found today", false},
		{"message form does not mask a non-matching body", &msg, "garbage", true},
		{"absent message keeps the parse error on an empty body", nil, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			doer := &redirectDoer{t: t, steps: []redirectStep{
				{wantMethod: "GET", wantURL: "https://nrm.invalid/api", body: tt.body},
			}}
			rels, err := Execute(t.Context(), noResultsDef(tt.message), Query{Keywords: "x"}, nil, doer, selector.New(), testDeps("https://nrm.invalid/", nil))
			if tt.wantParse {
				if !errors.Is(err, ErrParseError) {
					t.Fatalf("Execute error = %v, want ErrParseError", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Execute: %v, want graceful empty page", err)
			}
			if len(rels) != 0 {
				t.Errorf("releases = %d, want 0", len(rels))
			}
		})
	}
}

// TestExecute_NoResultsMessageContinuesPaths proves the short-circuit mirrors
// Jackett's `continue`: a matching first path skips ONLY that path — the
// remaining paths still run (both requests are issued).
func TestExecute_NoResultsMessageContinuesPaths(t *testing.T) {
	t.Parallel()

	empty := ""
	def := noResultsDef(&empty)
	def.Search.Paths = append(def.Search.Paths, loader.SearchPathBlock{
		Path:     "/api2",
		Response: &loader.ResponseBlock{Type: "json", NoResultsMessage: &empty},
	})
	doer := &redirectDoer{t: t, steps: []redirectStep{
		{wantMethod: "GET", wantURL: "https://nrm.invalid/api", body: ""},
		{wantMethod: "GET", wantURL: "https://nrm.invalid/api2", body: ""},
	}}
	rels, err := Execute(t.Context(), def, Query{Keywords: "x"}, nil, doer, selector.New(), testDeps("https://nrm.invalid/", nil))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("releases = %d, want 0", len(rels))
	}
	if len(doer.requests) != 2 {
		t.Errorf("requests issued = %d, want 2 (a matching path continues, not aborts)", len(doer.requests))
	}
}

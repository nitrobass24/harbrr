package search

import (
	"bytes"
	"strings"
	"testing"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/internal/selector"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/normalizer"
)

// privetCp1251 is the Russian word "Привет" ("hello") in windows-1251, the
// single-byte-per-char form a cp1251 tracker sends. Its UTF-8 form is the
// multi-byte D0.9F.. sequence asserted against below. Built here (not decoded
// from the wire) so the test is an independent oracle for the transcoders.
var (
	privetCp1251 = []byte{0xCF, 0xF0, 0xE8, 0xE2, 0xE5, 0xF2}
	privetUTF8   = "Привет"
)

// TestResolveEncoding pins the resolver against the exact set of `encoding:`
// values the vendored corpus declares: UTF-8/empty resolve to nil (no
// transcoding), every non-UTF-8 name resolves to a non-nil encoding, and an
// unknown name is a loud error (never a silent UTF-8 fallback).
func TestResolveEncoding(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		wantNil bool
		wantErr bool
	}{
		{"", true, false},
		{"UTF-8", true, false},
		{"utf-8", true, false},
		{"windows-1251", false, false},
		{"windows-1250", false, false},
		{"windows-1252", false, false},
		{"windows-1255", false, false},
		{"windows-1256", false, false},
		{"windows-874", false, false},
		{"iso-8859-1", false, false},
		{"ISO-8859-1", false, false},
		{"ISO-8859-2", false, false},
		{"tis-620", false, false}, // IANA leaves this nil; htmlindex fallback resolves it
		{"definitely-not-a-charset", false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			enc, err := ResolveEncoding(tt.name)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ResolveEncoding(%q) err = nil, want error", tt.name)
				}
				return
			}
			if err != nil {
				t.Fatalf("ResolveEncoding(%q): %v", tt.name, err)
			}
			if (enc == nil) != tt.wantNil {
				t.Errorf("ResolveEncoding(%q) nil = %v, want %v", tt.name, enc == nil, tt.wantNil)
			}
		})
	}
}

// TestDecodeBody proves a windows-1251 body decodes to correct UTF-8 (not
// U+FFFD), and a nil encoding (UTF-8/no-encoding def) returns the bytes
// unchanged.
func TestDecodeBody(t *testing.T) {
	t.Parallel()

	enc, err := ResolveEncoding("windows-1251")
	if err != nil {
		t.Fatalf("ResolveEncoding: %v", err)
	}
	if got := string(decodeBody(enc, privetCp1251)); got != privetUTF8 {
		t.Errorf("decodeBody(cp1251) = %q, want %q", got, privetUTF8)
	}
	if bytes.ContainsRune(decodeBody(enc, privetCp1251), '�') {
		t.Error("decoded body still contains U+FFFD mojibake")
	}
	// nil encoding: identity, even for bytes that are not valid UTF-8.
	if got := decodeBody(nil, privetCp1251); string(got) != string(privetCp1251) {
		t.Errorf("decodeBody(nil) mutated bytes: % X", got)
	}
}

// codepageQueryDef inlines the keyword into BOTH the path template and a query
// input, so one built request exposes the request-side asymmetry: the path is
// UTF-8-encoded (Jackett's WebUtility.UrlEncode) while the query VALUE is
// codepage-encoded (GetQueryString(Encoding)).
const codepageQueryDef = `---
id: cp1251req
name: CP1251 Request Fixture
description: codepage query encoding
language: ru-RU
type: public
encoding: windows-1251
links:
  - https://cp.invalid/
caps:
  categorymappings:
    - {id: 1, cat: Movies}
  modes:
    search: [q]
search:
  path: /browse/{{ .Keywords }}
  inputs:
    q: "{{ .Keywords }}"
  rows:
    selector: div.row
  fields:
    category:
      text: Movies
    title:
      selector: a.title
    download:
      selector: a.title
      attribute: href
    size:
      selector: span.size
    seeders:
      selector: span.seeders
`

// TestBuildRequests_CodepageQuery is the request-side precision check: under a
// windows-1251 def a Cyrillic keyword's QUERY value is cp1251-percent-encoded
// (%CF%F0..), while the same keyword substituted into the PATH stays UTF-8
// (%D0%9F..). This is the exact Jackett split (query = GetQueryString(Encoding),
// path = WebUtility.UrlEncode). Fails before the fix (the query value was UTF-8).
func TestBuildRequests_CodepageQuery(t *testing.T) {
	t.Parallel()

	def, err := loader.Parse([]byte(codepageQueryDef))
	if err != nil {
		t.Fatalf("loader.Parse: %v", err)
	}
	enc, err := ResolveEncoding(def.Encoding)
	if err != nil {
		t.Fatalf("ResolveEncoding: %v", err)
	}
	deps := Deps{Config: nil, BaseURL: "https://cp.invalid/", Encoding: enc}

	reqs, err := buildRequests(def, Query{Keywords: privetUTF8}, deps)
	if err != nil {
		t.Fatalf("buildRequests: %v", err)
	}
	if len(reqs) != 1 {
		t.Fatalf("reqs = %d, want 1", len(reqs))
	}
	got := reqs[0].url

	const (
		cp1251Escaped = "%CF%F0%E8%E2%E5%F2"                   // cp1251 bytes, query value
		utf8Escaped   = "%D0%9F%D1%80%D0%B8%D0%B2%D0%B5%D1%82" // UTF-8 bytes, path segment
	)
	if !strings.Contains(got, "q="+cp1251Escaped) {
		t.Errorf("query value not cp1251-encoded.\n got: %s\nwant q=%s", got, cp1251Escaped)
	}
	if !strings.Contains(got, "/browse/"+utf8Escaped) {
		t.Errorf("path value not UTF-8-encoded (must NOT be codepage-mangled).\n got: %s\nwant /browse/%s", got, utf8Escaped)
	}
}

// codepageBodyDef parses a windows-1251 HTML body into a single release.
const codepageBodyDef = `---
id: cp1251resp
name: CP1251 Response Fixture
description: codepage response decoding
language: ru-RU
type: public
encoding: windows-1251
links:
  - https://cp.invalid/
caps:
  categorymappings:
    - {id: 1, cat: Movies}
  modes:
    search: [q]
search:
  path: /browse
  inputs:
    q: "{{ .Keywords }}"
  rows:
    selector: div.row
  fields:
    category:
      text: Movies
    title:
      selector: a.title
    download:
      selector: a.title
      attribute: href
    size:
      selector: span.size
    seeders:
      selector: span.seeders
`

// TestParseResults_CodepageBody is the response-side check: a windows-1251 body
// carrying cp1251 Cyrillic title bytes parses to the correct UTF-8 title when the
// def encoding is wired, and to mojibake (NOT the correct title) when it is not —
// the fail-before/pass-after boundary for the 42 non-UTF-8 defs.
func TestParseResults_CodepageBody(t *testing.T) {
	t.Parallel()

	def, err := loader.Parse([]byte(codepageBodyDef))
	if err != nil {
		t.Fatalf("loader.Parse: %v", err)
	}
	prefix := []byte(`<html><body><div class="row"><a class="title" href="/dl/1">`)
	suffix := []byte(`</a><span class="size">1 GB</span><span class="seeders">5</span></div></body></html>`)
	body := make([]byte, 0, len(prefix)+len(privetCp1251)+len(suffix))
	body = append(body, prefix...)
	body = append(body, privetCp1251...)
	body = append(body, suffix...)

	newDeps := func(name string) Deps {
		enc, rerr := ResolveEncoding(name)
		if rerr != nil {
			t.Fatalf("ResolveEncoding(%q): %v", name, rerr)
		}
		return Deps{
			Filters:    NewFilterRegistry(),
			Normalizer: normalizer.New(normalizer.WithBaseURL("https://cp.invalid/")),
			BaseURL:    "https://cp.invalid/",
			Encoding:   enc,
		}
	}

	// With the def encoding wired: correct UTF-8, no U+FFFD.
	rels, err := ParseResults(def, body, "", Query{Keywords: privetUTF8}, selector.New(), newDeps("windows-1251"))
	if err != nil {
		t.Fatalf("ParseResults: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("releases = %d, want 1", len(rels))
	}
	if rels[0].Title != privetUTF8 {
		t.Errorf("title = %q, want %q", rels[0].Title, privetUTF8)
	}

	// Without it (nil encoding): the cp1251 bytes are mis-read as UTF-8 → mojibake,
	// proving the fix is what produces the correct title above.
	relsUTF8, err := ParseResults(def, body, "", Query{Keywords: privetUTF8}, selector.New(), newDeps("UTF-8"))
	if err != nil {
		t.Fatalf("ParseResults (utf-8): %v", err)
	}
	if len(relsUTF8) == 1 && relsUTF8[0].Title == privetUTF8 {
		t.Error("title parsed correctly WITHOUT the def encoding — the decode is not what fixed it")
	}
}

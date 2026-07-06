package registry_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/rs/zerolog"

	"github.com/autobrr/harbrr/internal/database"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/normalizer"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/indexer/registry"
	"github.com/autobrr/harbrr/internal/secrets"
)

// freeleechDefYAML is a tracker whose freeleech filter is a ROWS-SELECTOR (the riskiest
// pattern): `{{ if .Config.freeleech }}:has(.free)`. It also stamps downloadvolumefactor
// per-row from the FL marker, independent of the setting — the corpus invariant the
// serve-time filter relies on. If the engine were NOT built with freeleech cleared, the
// selector would drop non-free rows at PARSE time and the bypass feed could never see
// them; the e2e test asserts it does.
const freeleechDefYAML = `---
id: fltracker
name: FL Tracker
description: Freeleech registry test fixture
language: en-US
type: private
encoding: UTF-8
links:
  - https://html.invalid/
caps:
  categorymappings:
    - {id: 1, cat: Movies}
  modes:
    search: [q]
settings:
  - name: freeleech
    type: checkbox
    label: Filter freeleech only
    default: false
search:
  path: /browse.php
  inputs:
    q: "{{ .Keywords }}"
  rows:
    selector: "table.results > tbody > tr{{ if .Config.freeleech }}:has(img.free){{ end }}"
  fields:
    title:
      selector: a.title
    download:
      selector: a.dl
      attribute: href
    category:
      selector: td.cat
      attribute: data-cat
    size:
      selector: td.size
    seeders:
      selector: td.seeders
    downloadvolumefactor:
      case:
        img.free: "0"
        "*": "1"
`

// freeleechBodyHTML has one freeleech row (carries img.free) and one normal row.
const freeleechBodyHTML = `<!DOCTYPE html><html><body>
<table class="results"><tbody>
<tr><td class="cat" data-cat="1"></td>
<td><a class="title" href="/d?id=1">Free Movie 1080p</a><img class="free"></td>
<td><a class="dl" href="/dl?id=1">dl</a></td>
<td class="size">2.5 GB</td><td class="seeders">42</td></tr>
<tr><td class="cat" data-cat="1"></td>
<td><a class="title" href="/d?id=2">Paid Movie 1080p</a></td>
<td><a class="dl" href="/dl?id=2">dl</a></td>
<td class="size">700 MB</td><td class="seeders">5</td></tr>
</tbody></table></body></html>`

// newFreeleechRegistry builds a registry serving freeleechBodyHTML through the FL def.
func newFreeleechRegistry(t *testing.T, doer search.Doer) *registry.Registry {
	t.Helper()
	db, err := database.Open(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	dropin := t.TempDir()
	if err := os.WriteFile(filepath.Join(dropin, "fltracker.yml"), []byte(freeleechDefYAML), 0o600); err != nil {
		t.Fatalf("write def: %v", err)
	}
	keyring, err := secrets.OpenKeyring(secrets.KeyringOptions{EncryptionKey: testHexKey}, zerolog.Nop())
	if err != nil {
		t.Fatalf("open keyring: %v", err)
	}
	return registry.New(
		db, loader.New(dropin), keyring,
		registry.WithClock(fixedClock),
		registry.WithDoerFactory(func(registry.ClientParams) (search.Doer, error) { return doer, nil }),
	)
}

// TestFreeleechServeTimeFilter proves the Phase-11 model end to end: with freeleech=True
// configured, the honor feed returns FL-only while the bypass feed (FreeleechBypass)
// returns the FULL catalog — which is only possible because the engine fetched everything
// (the def's rows-selector freeleech filter was cleared at build) and the narrowing happens
// at serve time on downloadVolumeFactor.
func TestFreeleechServeTimeFilter(t *testing.T) {
	t.Parallel()

	reg := newFreeleechRegistry(t, &replayDoer{body: freeleechBodyHTML})
	ctx := context.Background()
	if _, err := reg.Add(ctx, registry.AddParams{
		Slug: "fl", DefinitionID: "fltracker",
		Settings: map[string]string{"freeleech": "True"},
	}); err != nil {
		t.Fatalf("Add: %v", err)
	}

	idx, ok := reg.Indexer(ctx, "fl")
	if !ok {
		t.Fatal("Indexer(fl) not resolved")
	}

	honor, err := idx.Search(ctx, search.Query{})
	if err != nil {
		t.Fatalf("honor Search: %v", err)
	}
	if len(honor) != 1 || honor[0].Title != "Free Movie 1080p" {
		t.Fatalf("honor feed = %v, want [Free Movie 1080p] only", titlesOf(honor))
	}

	bypass, err := idx.Search(ctx, search.Query{FreeleechBypass: true})
	if err != nil {
		t.Fatalf("bypass Search: %v", err)
	}
	if len(bypass) != 2 {
		t.Fatalf("bypass feed = %v, want both rows (proves engine fetched the full catalog)", titlesOf(bypass))
	}
}

func titlesOf(rels []*normalizer.Release) []string {
	out := make([]string, 0, len(rels))
	for _, r := range rels {
		out = append(out, r.Title)
	}
	return out
}

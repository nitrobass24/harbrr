package registry_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/rs/zerolog"

	"github.com/autobrr/harbrr/internal/database/dbtest"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/indexer/registry"
	"github.com/autobrr/harbrr/internal/secrets"
)

// andMatchDefYAML is a plain andmatch-filtered tracker: no settings of its own, so
// the punctuation opt-in can only arrive as the RESERVED instance setting.
const andMatchDefYAML = `---
id: andmatchtracker
name: AndMatch Tracker
description: andmatch punctuation registry test fixture
language: en-US
type: public
encoding: UTF-8
links:
  - https://html.invalid/
caps:
  categorymappings:
    - {id: 1, cat: Movies}
  modes:
    search: [q]
search:
  path: /browse.php
  inputs:
    q: "{{ .Keywords }}"
  rows:
    selector: "table.results > tbody > tr"
    filters:
      - name: andmatch
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
`

// andMatchBodyHTML holds the apostrophe title an *arr client's stripped query
// cannot match token-for-token, plus an already-stripped row that matches either
// way (so a green default assertion is not just an empty result set).
const andMatchBodyHTML = `<!DOCTYPE html><html><body>
<table class="results"><tbody>
<tr><td class="cat" data-cat="1"></td>
<td><a class="title" href="/d?id=1">Venise n'est pas en Italie 2019 1080p</a></td>
<td><a class="dl" href="/dl?id=1">dl</a></td>
<td class="size">2.5 GB</td><td class="seeders">42</td></tr>
<tr><td class="cat" data-cat="1"></td>
<td><a class="title" href="/d?id=2">Venise nest pas en Italie 2019 720p</a></td>
<td><a class="dl" href="/dl?id=2">dl</a></td>
<td class="size">700 MB</td><td class="seeders">5</td></tr>
</tbody></table></body></html>`

// TestAndMatchFoldPunctuationSetting proves the plumbing end to end: the reserved
// "andmatch_fold_punctuation" instance setting reaches the engine's row filter, and
// an instance without it keeps the default (Jackett-identical) verdict.
// autobrr/harbrr#394.
func TestAndMatchFoldPunctuationSetting(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		slug     string
		settings map[string]string
		want     []string
	}{
		{
			name: "default drops the apostrophe row", slug: "am-off", settings: nil,
			want: []string{"Venise nest pas en Italie 2019 720p"},
		},
		{
			name: "opt-in keeps the fetched apostrophe row", slug: "am-on",
			settings: map[string]string{"andmatch_fold_punctuation": "true"},
			want: []string{
				"Venise n'est pas en Italie 2019 1080p",
				"Venise nest pas en Italie 2019 720p",
			},
		},
		{
			name: "persisted false is off", slug: "am-false",
			settings: map[string]string{"andmatch_fold_punctuation": "false"},
			want:     []string{"Venise nest pas en Italie 2019 720p"},
		},
	}

	reg := newAndMatchRegistry(t, &replayDoer{body: andMatchBodyHTML})
	ctx := context.Background()

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := reg.Add(ctx, registry.AddParams{
				Slug: tc.slug, DefinitionID: "andmatchtracker", Settings: tc.settings,
			}); err != nil {
				t.Fatalf("Add: %v", err)
			}
			idx, ok := reg.Indexer(ctx, tc.slug)
			if !ok {
				t.Fatalf("Indexer(%s) not resolved", tc.slug)
			}
			rels, err := idx.Search(ctx, search.Query{Keywords: "Venise nest pas en Italie"})
			if err != nil {
				t.Fatalf("Search: %v", err)
			}
			got := titlesOf(rels)
			if len(got) != len(tc.want) {
				t.Fatalf("titles = %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("titles = %v, want %v", got, tc.want)
				}
			}
		})
	}
}

// newAndMatchRegistry builds a registry serving andMatchBodyHTML through the
// andmatch def.
func newAndMatchRegistry(t *testing.T, doer search.Doer) *registry.Registry {
	t.Helper()
	db := dbtest.OpenMigrated(t)
	dropin := t.TempDir()
	if err := os.WriteFile(filepath.Join(dropin, "andmatchtracker.yml"), []byte(andMatchDefYAML), 0o600); err != nil {
		t.Fatalf("write def: %v", err)
	}
	keyring, err := secrets.OpenKeyring(secrets.KeyringOptions{EncryptionKey: testHexKey}, zerolog.Nop())
	if err != nil {
		t.Fatalf("open keyring: %v", err)
	}
	return registry.New(
		db, loader.New(dropin), keyring, nil,
		registry.WithClock(fixedClock),
		registry.WithDoerFactory(func(registry.ClientParams) (search.Doer, error) { return doer, nil }),
	)
}

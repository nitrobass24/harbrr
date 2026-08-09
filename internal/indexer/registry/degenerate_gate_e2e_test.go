package registry_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/rs/zerolog"

	"github.com/autobrr/harbrr/internal/database/dbtest"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/indexer/core"
	"github.com/autobrr/harbrr/internal/indexer/registry"
	"github.com/autobrr/harbrr/internal/secrets"
)

// degenerateDefYAML is the shape the gate exists for: a definition whose own
// keywordsfilters strip everything outside `[a-zA-Z0-9 ]`, so a CJK query reaches the
// tracker as nothing but the year Radarr appended.
const degenerateDefYAML = `---
id: degeneratetracker
name: Degenerate Tracker
description: degenerate-query gate registry test fixture
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
  keywordsfilters:
    - name: re_replace
      args: ["[^a-zA-Z0-9 ]+", ""]
    - name: trim
  rows:
    selector: "table.results > tbody > tr"
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

// degenerateBodyHTML is what the tracker answers with when it is asked "2016" —
// everything it has. The whole point of the gate is not to pay for this.
const degenerateBodyHTML = `<!DOCTYPE html><html><body>
<table class="results"><tbody>
<tr><td class="cat" data-cat="1"></td>
<td><a class="title" href="/d?id=1">Some Unrelated Movie 2016 1080p</a></td>
<td><a class="dl" href="/dl?id=1">dl</a></td>
<td class="size">2.5 GB</td><td class="seeders">42</td></tr>
</tbody></table></body></html>`

// TestDegenerateQueryGate proves the gate end to end (autobrr/harbrr#394): the
// reserved "degenerate_query_gate" instance setting reaches the registry adapter,
// an unconfigured instance still sends the useless query exactly as before, and a
// gated search is a SKIP — no request, no health event, no budget unit spent.
func TestDegenerateQueryGate(t *testing.T) {
	t.Parallel()

	// A CJK title plus the year Radarr appends: the filters leave "2016".
	const cjk = "君の名は"

	// The three pass-through cases share one shape — a query that MUST reach the
	// tracker exactly once — and differ only in why. The skip case stays a sequential
	// lifecycle below: it asserts budget state ACROSS searches.
	passthrough := []struct {
		name     string
		slug     string
		settings map[string]string
		query    search.Query
		wantRels int
	}{
		{
			"default sends the query the filters degraded", "dg-off", nil,
			search.Query{Keywords: cjk, Year: "2016"},
			1,
		},
		{
			"auto leaves an answerable query alone", "dg-latin",
			map[string]string{"degenerate_query_gate": "auto"},
			search.Query{Keywords: "Some Unrelated Movie", Year: "2016"},
			1,
		},
		{
			"auto never gates an RSS poll", "dg-rss",
			map[string]string{"degenerate_query_gate": "auto"},
			search.Query{},
			1,
		},
	}
	for _, tc := range passthrough {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			reg, doer := newDegenerateRegistry(t)
			idx := addDegenerateIndexer(t, reg, tc.slug, tc.settings)

			rels, err := idx.Search(context.Background(), tc.query)
			if err != nil {
				t.Fatalf("Search: %v", err)
			}
			if len(rels) != tc.wantRels {
				t.Fatalf("releases = %d, want %d", len(rels), tc.wantRels)
			}
			if got := len(doer.reqs); got != 1 {
				t.Fatalf("requests = %d, want 1 (this query must reach the tracker)", got)
			}
		})
	}

	t.Run("auto skips without asking the tracker", func(t *testing.T) {
		t.Parallel()
		reg, doer := newDegenerateRegistry(t)
		idx := addDegenerateIndexer(t, reg, "dg-auto", map[string]string{
			"degenerate_query_gate": "auto",
			// One query per day: if the gate spent it, the live search below could not run.
			"query_limit": "1",
		})
		ctx := context.Background()

		_, err := idx.Search(ctx, search.Query{Keywords: cjk, Year: "2016"})
		if !errors.Is(err, core.ErrDegenerateQuery) {
			t.Fatalf("Search err = %v, want ErrDegenerateQuery", err)
		}
		if got := len(doer.reqs); got != 0 {
			t.Fatalf("requests = %d, want 0 (a gated search never reaches the tracker)", got)
		}

		// Skipped, not failed: nothing was recorded against the indexer's health.
		st, err := reg.Status(ctx, "dg-auto")
		if err != nil {
			t.Fatalf("Status: %v", err)
		}
		if len(st.Events) != 0 {
			t.Fatalf("health events = %+v, want none", st.Events)
		}
		if st.Status == "failing" {
			t.Error("status = failing, want a gated skip to leave health untouched")
		}

		// The budget unit is still there for a query that can actually succeed.
		if _, err := idx.Search(ctx, search.Query{Keywords: "Some Unrelated Movie", Year: "2016"}); err != nil {
			t.Fatalf("live search after a gated skip: %v (the gate spent budget)", err)
		}
		if got := len(doer.reqs); got != 1 {
			t.Fatalf("requests = %d, want 1", got)
		}
	})
}

// addDegenerateIndexer adds an instance of the degenerate def and resolves it.
func addDegenerateIndexer(t *testing.T, reg *registry.Registry, slug string, settings map[string]string) core.Indexer {
	t.Helper()
	ctx := context.Background()
	if _, err := reg.Add(ctx, registry.AddParams{
		Slug: slug, DefinitionID: "degeneratetracker", Settings: settings,
	}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	idx, ok := reg.Indexer(ctx, slug)
	if !ok {
		t.Fatalf("Indexer(%s) not resolved", slug)
	}
	return idx
}

// newDegenerateRegistry builds a registry serving degenerateBodyHTML through the
// degenerate def, returning the doer so a test can count what actually went out.
func newDegenerateRegistry(t *testing.T) (*registry.Registry, *replayDoer) {
	t.Helper()
	db := dbtest.OpenMigrated(t)
	dropin := t.TempDir()
	if err := os.WriteFile(filepath.Join(dropin, "degeneratetracker.yml"), []byte(degenerateDefYAML), 0o600); err != nil {
		t.Fatalf("write def: %v", err)
	}
	keyring, err := secrets.OpenKeyring(secrets.KeyringOptions{EncryptionKey: testHexKey}, zerolog.Nop())
	if err != nil {
		t.Fatalf("open keyring: %v", err)
	}
	doer := &replayDoer{body: degenerateBodyHTML}
	reg := registry.New(
		db, loader.New(dropin), keyring, nil,
		registry.WithClock(fixedClock),
		registry.WithDoerFactory(func(registry.ClientParams) (search.Doer, error) { return doer, nil }),
	)
	return reg, doer
}

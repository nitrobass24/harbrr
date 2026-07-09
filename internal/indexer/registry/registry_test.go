package registry_test

import (
	"context"
	"errors"
	"io"
	stdhttp "net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/autobrr/harbrr/internal/database"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/indexer/registry"
	"github.com/autobrr/harbrr/internal/secrets"
)

// defYAML is a minimal HTML-scrape definition (no login) with a secret apikey
// setting and a plaintext sort setting, written to a temp drop-in dir so the
// loader resolves it by id.
const defYAML = `---
id: testtracker
name: Test Tracker
description: Registry test fixture
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
  - name: apikey
    type: text
    label: API Key
  - name: sort
    type: text
    label: Sort
search:
  path: /browse.php
  inputs:
    q: "{{ .Keywords }}"
  rows:
    selector: table.results > tbody > tr
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
    leechers:
      selector: td.leechers
`

// bodyHTML is the saved search response the replay Doer serves. The synthetic
// passkey lives only in this test file (allowlisted for secret scanning).
const bodyHTML = `<!DOCTYPE html><html><body>
<table class="results"><tbody>
<tr><td class="cat" data-cat="1"></td>
<td><a class="title" href="/d?id=1">Big Buck Bunny 1080p</a></td>
<td><a class="dl" href="/dl?id=1&amp;passkey=NOTREALSECRET00">dl</a></td>
<td class="size">2.5 GB</td><td class="seeders">42</td><td class="leechers">7</td></tr>
<tr><td class="cat" data-cat="1"></td>
<td><a class="title" href="/d?id=2">Unrelated Release</a></td>
<td><a class="dl" href="/dl?id=2&amp;passkey=NOTREALSECRET00">dl</a></td>
<td class="size">700 MB</td><td class="seeders">5</td><td class="leechers">1</td></tr>
</tbody></table></body></html>`

// replayDoer serves a fixed body and records requests; no network.
type replayDoer struct {
	body string
	mu   sync.Mutex
	reqs []*stdhttp.Request
}

func (d *replayDoer) Do(req *stdhttp.Request) (*stdhttp.Response, error) {
	d.mu.Lock()
	d.reqs = append(d.reqs, req)
	d.mu.Unlock()
	return &stdhttp.Response{
		StatusCode: stdhttp.StatusOK,
		Header:     stdhttp.Header{},
		Body:       io.NopCloser(strings.NewReader(d.body)),
		Request:    req,
	}, nil
}

// newRegistry builds a registry over an in-memory DB, an inline-key keyring, and a
// drop-in loader holding defYAML, with the replay Doer injected.
func newRegistry(t *testing.T, doer search.Doer) (*registry.Registry, *database.DB) {
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
	if err := os.WriteFile(filepath.Join(dropin, "testtracker.yml"), []byte(defYAML), 0o600); err != nil {
		t.Fatalf("write def: %v", err)
	}

	keyring, err := secrets.OpenKeyring(secrets.KeyringOptions{EncryptionKey: testHexKey}, zerolog.Nop())
	if err != nil {
		t.Fatalf("open keyring: %v", err)
	}

	opts := []registry.Option{registry.WithClock(fixedClock)}
	if doer != nil {
		opts = append(opts, registry.WithDoerFactory(func(registry.ClientParams) (search.Doer, error) { return doer, nil }))
	}
	return registry.New(db, loader.New(dropin), keyring, opts...), db
}

// testHexKey is a synthetic 32-byte AES key for tests only (AGENTS.md allows
// synthetic secrets in *_test.go).
const testHexKey = "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20"

func fixedClock() time.Time { return time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC) }

func TestAddPersistsSecretEncrypted(t *testing.T) {
	t.Parallel()

	reg, db := newRegistry(t, nil)
	ctx := context.Background()

	inst, err := reg.Add(ctx, registry.AddParams{
		Slug: "tt", DefinitionID: "testtracker",
		Settings: map[string]string{"apikey": "PLAINTEXT-APIKEY-1", "sort": "seeders"},
	})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	// Raw DB inspection: the secret is encrypted (not plaintext), key_id set,
	// is_secret=1; the plaintext setting is stored as-is.
	rows, err := db.QueryContext(ctx,
		"SELECT name, value, value_encrypted, key_id, is_secret FROM indexer_settings WHERE instance_id=? ORDER BY name", inst.ID)
	if err != nil {
		t.Fatalf("query settings: %v", err)
	}
	defer rows.Close()

	got := map[string]struct {
		value, enc, keyID string
		secret            bool
	}{}
	for rows.Next() {
		var name string
		var value, enc, keyID *string
		var isSecret int
		if err := rows.Scan(&name, &value, &enc, &keyID, &isSecret); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got[name] = struct {
			value, enc, keyID string
			secret            bool
		}{deref(value), deref(enc), deref(keyID), isSecret != 0}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate settings: %v", err)
	}

	api := got["apikey"]
	if !api.secret {
		t.Error("apikey not marked secret")
	}
	if api.value != "" {
		t.Error("apikey stored a plaintext value column")
	}
	if api.enc == "" || strings.Contains(api.enc, "PLAINTEXT-APIKEY-1") {
		t.Errorf("apikey value_encrypted missing or contains plaintext: %q", api.enc)
	}
	if api.keyID == "" {
		t.Error("apikey key_id not stored")
	}
	if sort := got["sort"]; sort.secret || sort.value != "seeders" {
		t.Errorf("sort setting = %+v, want plaintext seeders", sort)
	}
}

func TestGetRedactsSecrets(t *testing.T) {
	t.Parallel()

	reg, _ := newRegistry(t, nil)
	ctx := context.Background()
	if _, err := reg.Add(ctx, registry.AddParams{
		Slug: "tt", DefinitionID: "testtracker",
		Settings: map[string]string{"apikey": "SECRETVAL", "sort": "seeders"},
	}); err != nil {
		t.Fatalf("Add: %v", err)
	}

	_, views, err := reg.Get(ctx, "tt")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	for _, v := range views {
		switch v.Name {
		case "apikey":
			if v.Value != secrets.Redacted || !v.Secret {
				t.Errorf("apikey view = %+v, want redacted secret", v)
			}
		case "sort":
			if v.Value != "seeders" || v.Secret {
				t.Errorf("sort view = %+v, want plaintext seeders", v)
			}
		}
	}
}

func TestUpdateRedactedPreservesSecret(t *testing.T) {
	t.Parallel()

	reg, db := newRegistry(t, nil)
	ctx := context.Background()
	inst, err := reg.Add(ctx, registry.AddParams{
		Slug: "tt", DefinitionID: "testtracker",
		Settings: map[string]string{"apikey": "ORIGINAL-SECRET", "sort": "seeders"},
	})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	origEnc := settingEnc(t, db, inst.ID, "apikey")

	// Submitting <redacted> for apikey must keep the stored ciphertext; sort changes.
	newSort := "leechers"
	if err := reg.Update(ctx, "tt", registry.UpdateParams{
		Settings: map[string]string{"apikey": secrets.Redacted, "sort": newSort},
	}); err != nil {
		t.Fatalf("Update: %v", err)
	}

	if got := settingEnc(t, db, inst.ID, "apikey"); got != origEnc {
		t.Error("apikey ciphertext changed after <redacted> update (should be preserved)")
	}
	_, views, err := reg.Get(ctx, "tt")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	for _, v := range views {
		if v.Name == "sort" && v.Value != "leechers" {
			t.Errorf("sort = %q, want leechers (updated)", v.Value)
		}
	}
}

func TestResolveAndSearch(t *testing.T) {
	t.Parallel()

	doer := &replayDoer{body: bodyHTML}
	reg, _ := newRegistry(t, doer)
	ctx := context.Background()
	if _, err := reg.Add(ctx, registry.AddParams{
		Slug: "tt", DefinitionID: "testtracker",
		Settings: map[string]string{"apikey": "SECRETVAL", "sort": "seeders"},
	}); err != nil {
		t.Fatalf("Add: %v", err)
	}

	idx, ok := reg.Indexer(ctx, "tt")
	if !ok {
		t.Fatal("Indexer(tt) not resolved")
	}
	if idx.Capabilities() == nil {
		t.Fatal("nil capabilities")
	}
	if info := idx.Info(); info.ID != "tt" || info.Type != "private" {
		t.Errorf("info = %+v, want id=tt type=private", info)
	}

	releases, err := idx.Search(ctx, search.Query{Keywords: "bunny"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	// andmatch keeps only the row whose title contains "bunny".
	if len(releases) != 1 {
		t.Fatalf("releases = %d, want 1", len(releases))
	}
	if releases[0].Title != "Big Buck Bunny 1080p" {
		t.Errorf("title = %q", releases[0].Title)
	}
	if len(doer.reqs) == 0 {
		t.Fatal("no request issued (engine did not drive the Doer)")
	}
}

func TestIndexerDisabledNotResolved(t *testing.T) {
	t.Parallel()

	reg, _ := newRegistry(t, &replayDoer{body: bodyHTML})
	ctx := context.Background()
	if _, err := reg.Add(ctx, registry.AddParams{Slug: "tt", DefinitionID: "testtracker"}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := reg.SetEnabled(ctx, "tt", false); err != nil {
		t.Fatalf("SetEnabled: %v", err)
	}
	if _, ok := reg.Indexer(ctx, "tt"); ok {
		t.Error("disabled indexer resolved, want not found")
	}
	// Missing slug also not resolved.
	if _, ok := reg.Indexer(ctx, "nope"); ok {
		t.Error("unknown indexer resolved")
	}
}

// TestAddPersistsProtocolFromDefinition proves Add derives the instance protocol
// from the definition's EffectiveProtocol. The testtracker def is torrent-only
// (no protocol field), so it must persist "torrent" both on the returned
// instance and after a round-trip through GetBySlug.
func TestAddPersistsProtocolFromDefinition(t *testing.T) {
	t.Parallel()

	reg, db := newRegistry(t, nil)
	ctx := context.Background()

	inst, err := reg.Add(ctx, registry.AddParams{Slug: "tt", DefinitionID: "testtracker"})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if inst.Protocol != "torrent" {
		t.Errorf("returned instance protocol = %q, want torrent", inst.Protocol)
	}

	got, err := database.Instances{}.GetBySlug(ctx, db, "tt")
	if err != nil {
		t.Fatalf("GetBySlug: %v", err)
	}
	if got.Protocol != "torrent" {
		t.Errorf("persisted protocol = %q, want torrent", got.Protocol)
	}
}

func TestAddRejectsUnknownDefAndDuplicate(t *testing.T) {
	t.Parallel()

	reg, _ := newRegistry(t, nil)
	ctx := context.Background()

	if _, err := reg.Add(ctx, registry.AddParams{Slug: "x", DefinitionID: "does-not-exist"}); err == nil {
		t.Error("Add with unknown definition succeeded")
	}
	if _, err := reg.Add(ctx, registry.AddParams{Slug: "tt", DefinitionID: "testtracker"}); err != nil {
		t.Fatalf("first Add: %v", err)
	}
	if _, err := reg.Add(ctx, registry.AddParams{Slug: "tt", DefinitionID: "testtracker"}); err == nil {
		t.Error("duplicate slug Add succeeded")
	}
}

// TestAddRejectsReservedSlug proves Add rejects a slug that collides with a
// static /api/indexers/ sibling segment (e.g. "stats") as ErrInvalid (→ 400),
// while a normal slug still succeeds. Guards U14-F3: an indexer slugged "stats"
// would otherwise be shadowed by GET /api/indexers/stats (allIndexerStats).
func TestAddRejectsReservedSlug(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		slug    string
		wantErr error
	}{
		{name: "reserved stats", slug: "stats", wantErr: registry.ErrInvalid},
		{name: "normal slug", slug: "notstats", wantErr: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			reg, _ := newRegistry(t, nil)
			_, err := reg.Add(context.Background(), registry.AddParams{Slug: tt.slug, DefinitionID: "testtracker"})
			switch {
			case tt.wantErr == nil && err != nil:
				t.Fatalf("Add(%q) = %v, want success", tt.slug, err)
			case tt.wantErr != nil && !errors.Is(err, tt.wantErr):
				t.Fatalf("Add(%q) err = %v, want %v", tt.slug, err, tt.wantErr)
			}
		})
	}
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func settingEnc(t *testing.T, db *database.DB, instanceID int64, name string) string {
	t.Helper()
	var enc *string
	err := db.QueryRowContext(context.Background(),
		"SELECT value_encrypted FROM indexer_settings WHERE instance_id=? AND name=?", instanceID, name).Scan(&enc)
	if err != nil {
		t.Fatalf("query setting %q: %v", name, err)
	}
	return deref(enc)
}

// TestRegistryTestAction exercises the management Test action through the
// registry: a configured no-login def authenticates trivially (CheckTest with no
// login block returns logged-in), and an unknown slug surfaces ErrNotFound. The
// engine is built fresh and uncached, so this never touches a cached session.
func TestRegistryTestAction(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	reg, _ := newRegistry(t, &replayDoer{body: bodyHTML})
	if _, err := reg.Add(ctx, registry.AddParams{Slug: "tt", DefinitionID: "testtracker"}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := reg.Test(ctx, "tt"); err != nil {
		t.Errorf("Test(tt) = %v, want nil (no-login def authenticates trivially)", err)
	}
	if err := reg.Test(ctx, "missing"); !errors.Is(err, database.ErrNotFound) {
		t.Errorf("Test(missing) = %v, want database.ErrNotFound", err)
	}
}

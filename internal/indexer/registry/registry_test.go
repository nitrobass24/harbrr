package registry_test

import (
	"context"
	"errors"
	"io"
	stdhttp "net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/autobrr/harbrr/internal/database"
	"github.com/autobrr/harbrr/internal/domain"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/indexer/core"
	"github.com/autobrr/harbrr/internal/indexer/native/catalog"
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

// newRegistryDeps opens a migrated in-memory DB with the testtracker drop-in def + an
// inline-key keyring — the pieces both registry constructors below assemble.
func newRegistryDeps(t *testing.T) (*database.DB, *loader.Loader, *secrets.Keyring) {
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
	return db, loader.New(dropin), keyring
}

// newRegistry builds a registry over an in-memory DB, an inline-key keyring, and a
// drop-in loader holding defYAML, with the replay Doer injected. Caching is OFF.
func newRegistry(t *testing.T, doer search.Doer) (*registry.Registry, *database.DB) {
	t.Helper()
	db, ldr, keyring := newRegistryDeps(t)
	opts := []registry.Option{registry.WithClock(fixedClock)}
	if doer != nil {
		opts = append(opts, registry.WithDoerFactory(func(registry.ClientParams) (search.Doer, error) { return doer, nil }))
	}
	return registry.New(db, ldr, keyring, catalog.All(), opts...), db
}

// newCachingRegistry is newRegistry with the search cache enabled, so reg.Indexer returns
// the REAL flattened *indexerAdapter wired to the cache — the production serve shape. Used
// by the driver-backed paging tests so they exercise the actual served value, not a scaffold.
func newCachingRegistry(t *testing.T, doer search.Doer) (*registry.Registry, *database.DB) {
	t.Helper()
	db, ldr, keyring := newRegistryDeps(t)
	sc := registry.NewSearchCacheForTest(db, fixedClock)
	opts := []registry.Option{registry.WithClock(fixedClock), registry.WithSearchCache(sc)}
	if doer != nil {
		opts = append(opts, registry.WithDoerFactory(func(registry.ClientParams) (search.Doer, error) { return doer, nil }))
	}
	return registry.New(db, ldr, keyring, catalog.All(), opts...), db
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

// TestAddDefaultsPriorityAndPersistsMinSeeders proves Add defaults an unset (0) priority
// to 25 and persists an explicit priority/minSeeders through to the returned instance.
func TestAddDefaultsPriorityAndPersistsMinSeeders(t *testing.T) {
	t.Parallel()

	reg, _ := newRegistry(t, nil)
	ctx := context.Background()

	def, err := reg.Add(ctx, registry.AddParams{Slug: "tt-default", DefinitionID: "testtracker"})
	if err != nil {
		t.Fatalf("Add (default priority): %v", err)
	}
	if def.Priority != 25 || def.MinSeeders != 0 {
		t.Errorf("default add priority/minSeeders = %d/%d, want 25/0", def.Priority, def.MinSeeders)
	}

	explicit, err := reg.Add(ctx, registry.AddParams{
		Slug: "tt-explicit", DefinitionID: "testtracker", Priority: 5, MinSeeders: 3,
	})
	if err != nil {
		t.Fatalf("Add (explicit priority): %v", err)
	}
	if explicit.Priority != 5 || explicit.MinSeeders != 3 {
		t.Errorf("explicit add priority/minSeeders = %d/%d, want 5/3", explicit.Priority, explicit.MinSeeders)
	}
}

// TestAddRejectsInvalidPriorityAndMinSeeders proves the registry rejects an out-of-range
// priority or a negative minSeeders as ErrInvalid, never silently clamping or storing them.
func TestAddRejectsInvalidPriorityAndMinSeeders(t *testing.T) {
	t.Parallel()

	reg, _ := newRegistry(t, nil)
	ctx := context.Background()

	if _, err := reg.Add(ctx, registry.AddParams{Slug: "tt-badprio", DefinitionID: "testtracker", Priority: 51}); !errors.Is(err, registry.ErrInvalid) {
		t.Errorf("Add(priority=51) err = %v, want ErrInvalid", err)
	}
	if _, err := reg.Add(ctx, registry.AddParams{Slug: "tt-badseed", DefinitionID: "testtracker", MinSeeders: -1}); !errors.Is(err, registry.ErrInvalid) {
		t.Errorf("Add(minSeeders=-1) err = %v, want ErrInvalid", err)
	}
}

// TestUpdatePriorityAndMinSeeders proves Update's tri-state pointer semantics: a nil field
// leaves the instance's current priority/minSeeders untouched; a present field replaces it.
func TestUpdatePriorityAndMinSeeders(t *testing.T) {
	t.Parallel()

	reg, _ := newRegistry(t, nil)
	ctx := context.Background()
	if _, err := reg.Add(ctx, registry.AddParams{Slug: "tt", DefinitionID: "testtracker", Priority: 20, MinSeeders: 2}); err != nil {
		t.Fatalf("Add: %v", err)
	}

	// A metadata-only patch (nil Priority/MinSeeders) must not disturb either value.
	newName := "Renamed"
	if err := reg.Update(ctx, "tt", registry.UpdateParams{Name: &newName}); err != nil {
		t.Fatalf("Update (name only): %v", err)
	}
	got, _, err := reg.Get(ctx, "tt")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Priority != 20 || got.MinSeeders != 2 {
		t.Errorf("after name-only update priority/minSeeders = %d/%d, want 20/2 (unchanged)", got.Priority, got.MinSeeders)
	}

	newPriority, newMinSeeders := 8, 6
	if err := reg.Update(ctx, "tt", registry.UpdateParams{Priority: &newPriority, MinSeeders: &newMinSeeders}); err != nil {
		t.Fatalf("Update (priority+minSeeders): %v", err)
	}
	got, _, err = reg.Get(ctx, "tt")
	if err != nil {
		t.Fatalf("Get after update: %v", err)
	}
	if got.Priority != 8 || got.MinSeeders != 6 {
		t.Errorf("after update priority/minSeeders = %d/%d, want 8/6", got.Priority, got.MinSeeders)
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
		// "all" names the aggregate feed in the same {slug} position (#400): an indexer
		// holding it would make the aggregate unreachable AND its grabs ambiguous.
		{name: "reserved all", slug: core.AggregateSlug, wantErr: registry.ErrInvalid},
		// Uppercase never reaches the reserved check — slugPattern rejects it first —
		// which is how the reservation is case-insensitive for free.
		{name: "reserved all uppercase", slug: "ALL", wantErr: registry.ErrInvalid},
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

// TestResolveAggregateSlug: core.AggregateSlug fans out to every ENABLED instance in
// slug order, a disabled one drops out, and a real slug still resolves to exactly
// itself — the seam every per-indexer feed keeps going through unchanged (#400).
func TestResolveAggregateSlug(t *testing.T) {
	t.Parallel()

	reg, _ := newRegistry(t, nil)
	ctx := context.Background()
	for _, slug := range []string{"beta", "alpha", "gamma"} {
		if _, err := reg.Add(ctx, registry.AddParams{Slug: slug, DefinitionID: "testtracker"}); err != nil {
			t.Fatalf("Add(%q): %v", slug, err)
		}
	}
	if err := reg.SetEnabled(ctx, "gamma", false); err != nil {
		t.Fatalf("SetEnabled: %v", err)
	}

	members, err := reg.Resolve(ctx, core.AggregateSlug)
	if err != nil {
		t.Fatalf("Resolve(all): %v", err)
	}
	if want := []string{"alpha", "beta"}; !slices.Equal(memberIDs(members), want) {
		t.Errorf("Resolve(all) = %v, want %v (enabled only, slug order)", memberIDs(members), want)
	}

	single, err := reg.Resolve(ctx, "beta")
	if err != nil || len(single) != 1 || single[0].ID != "beta" || single[0].Indexer == nil {
		t.Errorf("Resolve(beta) = %v, %v; want exactly one live [beta]", single, err)
	}
	if _, err := reg.Resolve(ctx, "nope"); !errors.Is(err, core.ErrNoSuchFeed) {
		t.Errorf("Resolve of an unknown slug = %v, want ErrNoSuchFeed", err)
	}
	// The aggregate slug names no indexer, which is what makes all/dl unservable.
	if _, ok := reg.Indexer(ctx, core.AggregateSlug); ok {
		t.Error("Indexer(all) must be !ok")
	}
}

// memberIDs is the resolved member set as a slug list, for order assertions.
func memberIDs(members []core.MemberOutcome) []string {
	out := make([]string, 0, len(members))
	for _, m := range members {
		out = append(out, m.ID)
	}
	return out
}

// TestResolveAggregateEmpty: an aggregate over zero enabled instances resolves to an
// empty set with no error — a valid empty feed, never an "indexer not supported" error.
func TestResolveAggregateEmpty(t *testing.T) {
	t.Parallel()

	reg, _ := newRegistry(t, nil)
	members, err := reg.Resolve(context.Background(), core.AggregateSlug)
	if err != nil || len(members) != 0 {
		t.Errorf("Resolve(all) = %v, %v; want empty and no error", members, err)
	}
}

// newProfile creates a sync profile named name selecting the given instance slugs, and
// returns the feed slug that addresses it.
func newProfile(t *testing.T, db *database.DB, name string, slugs ...string) string {
	t.Helper()
	ctx := context.Background()
	id, err := database.SyncProfiles{}.InsertProfile(ctx, db, domain.SyncProfile{Name: name})
	if err != nil {
		t.Fatalf("insert profile %q: %v", name, err)
	}
	ids := make([]int64, 0, len(slugs))
	for _, slug := range slugs {
		inst, err := database.Instances{}.GetBySlug(ctx, db, slug)
		if err != nil {
			t.Fatalf("get instance %q: %v", slug, err)
		}
		ids = append(ids, inst.ID)
	}
	if err := (database.SyncProfiles{}).ReplaceProfileIndexers(ctx, db, id, ids); err != nil {
		t.Fatalf("replace profile indexers: %v", err)
	}
	return core.ProfileSlugPrefix + name
}

// TestResolveProfileSlug: 'profile:<name>' resolves to exactly that profile's members,
// in the same slug order the aggregate uses — and a DISABLED member is still selected,
// ledgered as such, because the operator put it in the profile.
func TestResolveProfileSlug(t *testing.T) {
	t.Parallel()

	reg, db := newRegistry(t, nil)
	ctx := context.Background()
	for _, slug := range []string{"beta", "alpha", "gamma", "delta"} {
		if _, err := reg.Add(ctx, registry.AddParams{Slug: slug, DefinitionID: "testtracker"}); err != nil {
			t.Fatalf("Add(%q): %v", slug, err)
		}
	}
	if err := reg.SetEnabled(ctx, "gamma", false); err != nil {
		t.Fatalf("SetEnabled: %v", err)
	}
	slug := newProfile(t, db, "my movies", "gamma", "beta", "alpha")

	members, err := reg.Resolve(ctx, slug)
	if err != nil {
		t.Fatalf("Resolve(%q): %v", slug, err)
	}
	if want := []string{"alpha", "beta", "gamma"}; !slices.Equal(memberIDs(members), want) {
		t.Errorf("Resolve(%q) = %v, want %v (profile members only, slug order)", slug, memberIDs(members), want)
	}
	for _, m := range members {
		switch m.ID {
		case "gamma":
			if m.Reason != core.SkipDisabled || m.Indexer != nil {
				t.Errorf("disabled member = %+v, want a %q skip with no engine", m, core.SkipDisabled)
			}
		default:
			if !m.OK() || m.Indexer == nil {
				t.Errorf("member %q = %+v, want live", m.ID, m)
			}
		}
	}
}

// TestResolveProfileNotFound: an unknown profile is the same not-found an unknown
// indexer slug is, and a profile feed slug resolves no INDEXER, so profile:x/dl 404s.
func TestResolveProfileNotFound(t *testing.T) {
	t.Parallel()

	reg, _ := newRegistry(t, nil)
	ctx := context.Background()
	if _, err := reg.Resolve(ctx, core.ProfileSlugPrefix+"nope"); !errors.Is(err, core.ErrNoSuchFeed) {
		t.Errorf("Resolve of an unknown profile = %v, want ErrNoSuchFeed", err)
	}
	if _, ok := reg.Indexer(ctx, core.ProfileSlugPrefix+"nope"); ok {
		t.Error("Indexer(profile:...) must be !ok, so profile:x/dl can never serve a download")
	}
}

// TestResolveProfileEmpty: a profile selecting nothing is an empty member set with no
// error — an empty feed with an empty (but present) ledger, not a not-found.
func TestResolveProfileEmpty(t *testing.T) {
	t.Parallel()

	reg, db := newRegistry(t, nil)
	slug := newProfile(t, db, "empty")
	members, err := reg.Resolve(context.Background(), slug)
	if err != nil || len(members) != 0 {
		t.Errorf("Resolve(%q) = %v, %v; want empty and no error", slug, members, err)
	}
}

// TestResolveUnbuildableMemberIsLedgered is the ledger-completeness gate (#400): an
// enabled instance whose adapter cannot BUILD must still come back as a member — with a
// constant reason and no engine — on BOTH aggregate slug forms. Dropping it silently is
// what broke the ledger's "absence is visible" promise.
func TestResolveUnbuildableMemberIsLedgered(t *testing.T) {
	t.Parallel()

	reg, db := newRegistry(t, nil)
	ctx := context.Background()
	if _, err := reg.Add(ctx, registry.AddParams{Slug: "alpha", DefinitionID: "testtracker"}); err != nil {
		t.Fatalf("Add(alpha): %v", err)
	}
	// Inserted straight through the store: Add validates the definition exists, and
	// "the definition vanished under a stored instance" is exactly the state under test.
	if _, err := (database.Instances{}).Insert(ctx, db, domain.IndexerInstance{
		Slug: "broken", DefinitionID: "gone-from-disk", Name: "Broken", Enabled: true, Protocol: "torrent", Priority: 25,
	}); err != nil {
		t.Fatalf("insert broken instance: %v", err)
	}
	profile := newProfile(t, db, "both", "alpha", "broken")

	for _, slug := range []string{core.AggregateSlug, profile} {
		members, err := reg.Resolve(ctx, slug)
		if err != nil {
			t.Fatalf("Resolve(%q): %v", slug, err)
		}
		if want := []string{"alpha", "broken"}; !slices.Equal(memberIDs(members), want) {
			t.Fatalf("Resolve(%q) = %v, want %v", slug, memberIDs(members), want)
		}
		broken := members[1]
		if broken.Reason != core.SkipUnavailable || broken.Indexer != nil {
			t.Errorf("Resolve(%q) broken member = %+v, want a %q skip with no engine", slug, broken, core.SkipUnavailable)
		}
		if broken.Name != "Broken" {
			t.Errorf("a member that never built must take its ledger name from the stored row, got %q", broken.Name)
		}
	}
}

// TestResolveWholeListFailure: when the instance store cannot be read, Resolve must
// FAIL rather than answer "no members" — an empty feed would report every configured
// indexer as absent without saying so.
func TestResolveWholeListFailure(t *testing.T) {
	t.Parallel()

	reg, db := newRegistry(t, nil)
	ctx := context.Background()
	if _, err := reg.Add(ctx, registry.AddParams{Slug: "alpha", DefinitionID: "testtracker"}); err != nil {
		t.Fatalf("Add(alpha): %v", err)
	}
	slug := newProfile(t, db, "mine", "alpha")
	if err := db.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}

	for _, feed := range []string{core.AggregateSlug, slug} {
		members, err := reg.Resolve(ctx, feed)
		if err == nil {
			t.Errorf("Resolve(%q) = %v, nil; want an error when the store is unreadable", feed, members)
		}
		if errors.Is(err, core.ErrNoSuchFeed) {
			t.Errorf("Resolve(%q) = %v; a read failure must not masquerade as not-found", feed, err)
		}
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

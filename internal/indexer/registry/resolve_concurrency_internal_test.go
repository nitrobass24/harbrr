package registry

import (
	"context"
	"errors"
	stdhttp "net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/autobrr/harbrr/internal/database"
	"github.com/autobrr/harbrr/internal/domain"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/secrets"
)

// noNetDoer is a build-only Doer: resolve builds the engine but this test never runs
// a search, so Do is never called. It exists so the doer factory can return a valid
// Doer while it lands the mid-build invalidate.
type noNetDoer struct{}

func (noNetDoer) Do(*stdhttp.Request) (*stdhttp.Response, error) {
	return nil, errors.New("no network in test")
}

// TestResolveRejectsStaleEngineOnConcurrentInvalidation pins Registry.resolve against
// the persistently-stale-engine bug it exists to close (U8R-F3): resolve builds the
// engine OUTSIDE the lock from the settings present at build time, then re-locks and
// (pre-fix) double-checks only PRESENCE before installing. An invalidate that lands
// DURING the build deletes a key that isn't cached yet (the in-flight resolve hasn't
// installed it) — a no-op — so resolve then installs the engine built from the
// now-superseded settings. That stale engine persists until the next mutation.
//
// The interleaving is made deterministic without goroutines by using the existing
// doer-factory seam as the hook (mirroring U8R-F1's injected keyring hook): the
// factory is called inside buildAdapter AFTER the instance's settings have been read
// into ClientParams.Cfg and BEFORE resolve re-locks to install — exactly the window
// the finding describes. On the first build only, the factory mutates the "sort"
// setting in the DB and fires the table case's invalidation action, landing it inside
// the build window; then resolve finishes.
//
// The factory records the "sort" value each build observed. build reads settings
// fresh each time, so the value of the CURRENTLY-SERVED engine is the last recorded
// build. Pre-fix: the first resolve caches the OLD engine, the second resolve returns
// it from cache and never rebuilds, so the last (only) observed build is "OLD" — the
// stale engine is served. Post-fix: the generation bump makes the first resolve skip
// the install, so the second resolve rebuilds and reads "NEW" — fresh.
//
// Two cases exercise the two invalidation entry points that both must close this
// window: per-slug invalidate(slug) (the original U8R-F3 finding) and InvalidateAll
// (the global-epoch counterpart fired by a global proxy/solver resource change).
func TestResolveRejectsStaleEngineOnConcurrentInvalidation(t *testing.T) {
	tests := []struct {
		name   string
		action func(reg *Registry) // the mid-build invalidation this case fires
	}{
		{"per-slug invalidate", func(reg *Registry) { reg.invalidate("stale") }},
		{"InvalidateAll (global epoch)", func(reg *Registry) { reg.InvalidateAll() }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()

			db, err := database.Open(":memory:")
			if err != nil {
				t.Fatalf("open db: %v", err)
			}
			t.Cleanup(func() { _ = db.Close() })
			if err := db.Migrate(ctx); err != nil {
				t.Fatalf("migrate: %v", err)
			}
			dropin := t.TempDir()
			if err := os.WriteFile(filepath.Join(dropin, "mamtest.yml"), []byte(mamDefYAML), 0o600); err != nil {
				t.Fatalf("write def: %v", err)
			}
			kr, err := secrets.OpenKeyring(secrets.KeyringOptions{EncryptionKey: resolveTestKey}, zerolog.Nop())
			if err != nil {
				t.Fatalf("keyring: %v", err)
			}
			clock := func() time.Time { return time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC) }

			var (
				mu       sync.Mutex // guards observed under -race across builds
				observed []string   // the "sort" value each build read, in build order
				once     sync.Once  // fires the mid-build invalidation on the first build only
				reg      *Registry
				instID   int64
			)

			reg = New(
				db, loader.New(dropin), kr, nil, WithClock(clock),
				WithDoerFactory(func(p ClientParams) (search.Doer, error) {
					mu.Lock()
					observed = append(observed, p.Cfg["sort"])
					mu.Unlock()
					once.Do(func() {
						// Settings are already read into p.Cfg and the engine is about to be
						// built from them; resolve has NOT re-locked to install. Mutate the
						// config and fire the invalidation here to land it inside resolve's
						// build window — the exact U8R-F3 interleaving.
						if err := (database.Instances{}).UpsertSetting(ctx, db, instID, domain.IndexerSetting{Name: "sort", Value: "NEW"}); err != nil {
							t.Errorf("mid-build mutate sort: %v", err)
						}
						tt.action(reg)
					})
					return noNetDoer{}, nil
				}),
			)

			inst, err := reg.Add(ctx, AddParams{
				Slug: "stale", DefinitionID: "mamtest",
				Settings: map[string]string{"mam_id": "session", "apikey": "key", "sort": "OLD"},
			})
			if err != nil {
				t.Fatalf("Add: %v", err)
			}
			instID = inst.ID

			// First resolve: build reads sort="OLD"; the factory then mutates sort→"NEW"
			// and fires the invalidation mid-build. Pre-fix this installs the OLD (stale)
			// engine.
			if _, err := reg.resolve(ctx, "stale"); err != nil {
				t.Fatalf("first resolve: %v", err)
			}
			// Second resolve: post-fix the cache is empty (the stale engine was never
			// installed), so it rebuilds and reads sort="NEW". Pre-fix it returns the
			// cached OLD engine and never rebuilds.
			if _, err := reg.resolve(ctx, "stale"); err != nil {
				t.Fatalf("second resolve: %v", err)
			}

			mu.Lock()
			defer mu.Unlock()
			if len(observed) == 0 {
				t.Fatalf("no builds observed")
			}
			if last := observed[len(observed)-1]; last != "NEW" {
				t.Fatalf("served engine built from superseded settings: observed builds = %v; want the last build to reflect the post-invalidation sort=%q (U8R-F3: a mid-build invalidation left a stale engine cached)", observed, "NEW")
			}
		})
	}
}

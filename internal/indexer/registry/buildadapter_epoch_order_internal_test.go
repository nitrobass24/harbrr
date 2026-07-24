package registry

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/autobrr/harbrr/internal/database"
	"github.com/autobrr/harbrr/internal/database/dbinterface"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/secrets"
)

// settingsQueryHookQuerier wraps a dbinterface.Querier and, on the FIRST QueryContext
// whose SQL selects from indexer_settings, invokes hook (when armed) BEFORE
// forwarding — the seam for landing an invalidation exactly at the point buildAdapter
// reads Settings, immediately after its epoch snapshot. hook starts nil so setup
// traffic (Add, which never queries indexer_settings) can never consume the one-shot
// fire before the test arms it.
type settingsQueryHookQuerier struct {
	dbinterface.Querier
	fired atomic.Bool
	hook  func()
}

func (w *settingsQueryHookQuerier) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	if w.hook != nil && strings.Contains(query, "FROM indexer_settings") && w.fired.CompareAndSwap(false, true) {
		w.hook()
	}
	return w.Querier.QueryContext(ctx, query, args...) //nolint:wrapcheck // passthrough test double.
}

// TestBuildAdapterSnapshotsEpochBeforeSettingsRead is the build-order regression for
// Change 1 (registry.go): buildAdapter must capture the instance's invalidation epoch
// BEFORE it reads Settings, not after the whole build completes (the prior ordering).
// This test lands an invalidation at the earliest possible instant after the epoch
// snapshot — exactly when the Settings SELECT itself runs — and asserts the built
// adapter still carries the PRE-bump epoch, proving the snapshot already happened.
//
// DEVIATION FROM THE PLAN (flagged per the brief): the plan's primary shape for this
// test drives a full resolve()+Search() round trip through the built adapter and
// asserts its write-back is dropped while a fresh resolve's write-back stores. Doing
// that needs a live-search-shaped fixture for the Cardigann engine (an HTML/JSON
// response matching the definition's search block) layered on top of this ordering
// seam — a second harness, not just more wrapper. Given the plan's own authorized
// fallback ("if too contorted, assert buildAdapter's returned adapter carries the
// pre-bump epoch"), this test takes that shape directly: it proves the fact the full
// round trip would have relied on, at its source, rather than via a store side
// effect. The write-back consequence itself (a stale-epoch store gets dropped) is
// already covered by the existing epoch regression suite
// (searchcache_epoch_regression_test.go) and this PR's new TOCTOU test alongside it.
func TestBuildAdapterSnapshotsEpochBeforeSettingsRead(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	rawDB, err := database.Open(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = rawDB.Close() })
	if err := rawDB.Migrate(ctx); err != nil {
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

	// The cache reads/writes through the RAW handle (never through the wrapper), so
	// its own queries can never trip the settings hook.
	sc := newSearchCache(rawDB, cacheTuning{enabled: true, ttl: keywordTTL, cleanup: time.Hour}, clock, zerolog.Nop())
	wrapped := &settingsQueryHookQuerier{Querier: rawDB}

	reg := New(wrapped, loader.New(dropin), kr, nil, WithClock(clock), WithSearchCache(sc),
		WithDoerFactory(func(ClientParams) (search.Doer, error) { return noNetDoer{}, nil }))

	inst, err := reg.Add(ctx, AddParams{
		Slug: "stale", DefinitionID: "mamtest",
		Settings: map[string]string{"mam_id": "session", "apikey": "key", "sort": "OLD"},
	})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	instID := inst.ID

	// Arm the hook only now, after Add's own settings-table writes are done — its
	// first fire lands squarely inside buildAdapter's Settings() call below.
	wrapped.hook = func() { sc.bumpInstanceEpoch(instID) }

	a, err := reg.buildAdapter(ctx, "stale")
	if err != nil {
		t.Fatalf("buildAdapter: %v", err)
	}

	if !wrapped.fired.Load() {
		t.Fatal("the settings-query hook never fired; the test didn't exercise the intended window")
	}
	if got := sc.instanceEpoch(instID); got != 1 {
		t.Fatalf("instanceEpoch = %d, want 1 (the hook's bump should have landed)", got)
	}
	if a.builtEpoch != 0 {
		t.Fatalf("builtEpoch = %d, want 0 (pre-bump): the epoch snapshot must happen BEFORE the "+
			"settings read, so a bump landing exactly at the settings query cannot poison the "+
			"adapter with a fresh-looking epoch over what would otherwise be stale settings", a.builtEpoch)
	}
}

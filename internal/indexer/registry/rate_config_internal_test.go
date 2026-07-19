package registry

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/autobrr/harbrr/internal/database"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/secrets"
)

// TestRateDefaultSeed proves a fresh Resolver's RateDefault is the hardcoded
// defaultRateInterval (autobrr/harbrr#104) — an untouched system behaves exactly
// like before this knob existed.
func TestRateDefaultSeed(t *testing.T) {
	t.Parallel()
	reg, _, _ := newResolveRegistry(t)
	if got := reg.RateDefault(); got != defaultRateInterval {
		t.Errorf("seed RateDefault() = %v, want %v", got, defaultRateInterval)
	}
}

// TestRateDefaultRoundTrip proves SetRateDefault swaps the live value and persists
// it (LoadRateDefaultOverride after resetting the in-memory copy restores it),
// mirroring TestSearchCacheConfigRoundTrip.
func TestRateDefaultRoundTrip(t *testing.T) {
	t.Parallel()
	reg, _, _ := newResolveRegistry(t)
	ctx := context.Background()

	want := 7 * time.Second
	if err := reg.SetRateDefault(ctx, want.String()); err != nil {
		t.Fatalf("SetRateDefault: %v", err)
	}
	if got := reg.RateDefault(); got != want {
		t.Fatalf("after SetRateDefault RateDefault() = %v, want %v", got, want)
	}

	// Reset the in-memory value to the seed; LoadRateDefaultOverride must restore
	// the persisted value from app_settings.
	reg.rateDefault.Store(int64(defaultRateInterval))
	if err := reg.LoadRateDefaultOverride(ctx); err != nil {
		t.Fatalf("LoadRateDefaultOverride: %v", err)
	}
	if got := reg.RateDefault(); got != want {
		t.Errorf("after LoadRateDefaultOverride RateDefault() = %v, want persisted %v", got, want)
	}
}

// TestLoadRateDefaultOverrideIgnoresBadValues proves a missing/malformed/non-positive
// stored value is ignored (the seed stands) — operator config must never brick startup.
func TestLoadRateDefaultOverrideIgnoresBadValues(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	for _, stored := range []string{"not-a-duration", "0s", "-5s"} {
		reg, _, db := newResolveRegistry(t)
		if err := (database.AppSettings{}).Set(ctx, db, keyRateDefaultInterval, stored, time.Now()); err != nil {
			t.Fatalf("seed app_settings: %v", err)
		}
		if err := reg.LoadRateDefaultOverride(ctx); err != nil {
			t.Fatalf("LoadRateDefaultOverride(%q): %v", stored, err)
		}
		if got := reg.RateDefault(); got != defaultRateInterval {
			t.Errorf("LoadRateDefaultOverride(%q) RateDefault() = %v, want seed %v unchanged", stored, got, defaultRateInterval)
		}
	}

	// A missing key is a no-op too.
	reg, _, _ := newResolveRegistry(t)
	if err := reg.LoadRateDefaultOverride(ctx); err != nil {
		t.Fatalf("LoadRateDefaultOverride (missing key): %v", err)
	}
	if got := reg.RateDefault(); got != defaultRateInterval {
		t.Errorf("LoadRateDefaultOverride (missing key) RateDefault() = %v, want seed %v", got, defaultRateInterval)
	}
}

// TestSetRateDefaultRejectsInvalid proves a non-positive/unparseable duration wraps
// ErrInvalid (so the API layer answers 400) and leaves the live value untouched.
func TestSetRateDefaultRejectsInvalid(t *testing.T) {
	t.Parallel()
	reg, _, _ := newResolveRegistry(t)
	ctx := context.Background()
	before := reg.RateDefault()

	for _, bad := range []string{"", "not-a-duration", "0s", "-1s"} {
		if err := reg.SetRateDefault(ctx, bad); err == nil || !errors.Is(err, ErrInvalid) {
			t.Errorf("SetRateDefault(%q) err = %v, want ErrInvalid", bad, err)
		}
	}
	if got := reg.RateDefault(); got != before {
		t.Errorf("RateDefault changed after rejected SetRateDefault calls: %v != %v", got, before)
	}
}

// TestSetRateDefaultInvalidatesLiveAdapters proves the live plumb-through
// (autobrr/harbrr#104): SetRateDefault reaches an ALREADY-CACHED adapter's paced
// client on the next resolve, without a restart, via the same InvalidateAll
// mechanism a global proxy/solver resource edit already uses.
func TestSetRateDefaultInvalidatesLiveAdapters(t *testing.T) {
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

	var mu sync.Mutex
	var observed []time.Duration
	reg := New(db, loader.New(dropin), kr, nil, WithDoerFactory(func(p ClientParams) (search.Doer, error) {
		mu.Lock()
		observed = append(observed, p.RateInterval)
		mu.Unlock()
		return noNetDoer{}, nil
	}))

	if _, err := reg.Add(ctx, AddParams{
		Slug: "ratetest", DefinitionID: "mamtest",
		Settings: map[string]string{"mam_id": "session", "apikey": "key"},
	}); err != nil {
		t.Fatalf("Add: %v", err)
	}

	if _, err := reg.resolve(ctx, "ratetest"); err != nil {
		t.Fatalf("first resolve: %v", err)
	}
	if _, ok := reg.cache["ratetest"]; !ok {
		t.Fatalf("adapter was not cached after first resolve")
	}

	next := 9 * time.Second
	if err := reg.SetRateDefault(ctx, next.String()); err != nil {
		t.Fatalf("SetRateDefault: %v", err)
	}
	if _, ok := reg.cache["ratetest"]; ok {
		t.Fatalf("SetRateDefault must invalidate every cached adapter (InvalidateAll), but %q is still cached", "ratetest")
	}

	if _, err := reg.resolve(ctx, "ratetest"); err != nil {
		t.Fatalf("second resolve: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(observed) != 2 {
		t.Fatalf("observed builds = %v, want 2 (one per resolve)", observed)
	}
	if observed[0] != defaultRateInterval {
		t.Errorf("first build RateInterval = %v, want seed %v", observed[0], defaultRateInterval)
	}
	if observed[1] != next {
		t.Errorf("second build RateInterval = %v, want the newly-set global default %v — SetRateDefault did not reach the live adapter", observed[1], next)
	}
}

// TestPerIndexerRateOverrideLiveAfterUpdate proves the OTHER half of the live
// plumb-through: a per-indexer "rate_interval" override change is already live via
// the existing Manager.Update -> invalidate(slug) path, with no new mechanism
// needed. It also pins the corrected formula: the override REPLACES the global
// default (it is not max()'d against it), so lowering it below the (unrelated)
// global default still takes effect.
func TestPerIndexerRateOverrideLiveAfterUpdate(t *testing.T) {
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

	var mu sync.Mutex
	var observed []time.Duration
	reg := New(db, loader.New(dropin), kr, nil, WithDoerFactory(func(p ClientParams) (search.Doer, error) {
		mu.Lock()
		observed = append(observed, p.RateInterval)
		mu.Unlock()
		return noNetDoer{}, nil
	}))
	// Global default (seed, 1s) is well above the override used below, so a stale
	// three-way max() would wrongly serve the global default instead of the override.
	if err := reg.SetRateDefault(ctx, "20s"); err != nil {
		t.Fatalf("SetRateDefault: %v", err)
	}

	if _, err := reg.Add(ctx, AddParams{
		Slug: "ratetest2", DefinitionID: "mamtest",
		Settings: map[string]string{"mam_id": "session", "apikey": "key", "rate_interval": "3s"},
	}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if _, err := reg.resolve(ctx, "ratetest2"); err != nil {
		t.Fatalf("first resolve: %v", err)
	}

	if err := reg.Update(ctx, "ratetest2", UpdateParams{Settings: map[string]string{"rate_interval": "6s"}}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if _, ok := reg.cache["ratetest2"]; ok {
		t.Fatalf("Update must invalidate the slug's cached adapter, but it is still cached")
	}
	if _, err := reg.resolve(ctx, "ratetest2"); err != nil {
		t.Fatalf("second resolve: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(observed) != 2 {
		t.Fatalf("observed builds = %v, want 2", observed)
	}
	if observed[0] != 3*time.Second {
		t.Errorf("first build RateInterval = %v, want the override 3s (replacing the 20s global default)", observed[0])
	}
	if observed[1] != 6*time.Second {
		t.Errorf("second build RateInterval = %v, want the updated override 6s — the settings change did not go live", observed[1])
	}
}

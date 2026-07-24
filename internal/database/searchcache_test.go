package database_test

import (
	"context"
	"testing"
	"time"

	"github.com/autobrr/harbrr/internal/database"
)

// sampleEntry builds a fully-populated cache entry bound to instanceID, expiring
// ttl after cachedAt. results_json carries an opaque (pretend secret-bearing) blob
// the store persists verbatim.
func sampleEntry(key string, instanceID int64, cachedAt time.Time, ttl time.Duration) database.SearchCacheEntry {
	return database.SearchCacheEntry{
		CacheKey:     key,
		InstanceID:   instanceID,
		ResultsJSON:  []byte(`[{"title":"Example","link":"http://tracker/dl?passkey=secret"}]`),
		TotalResults: 1,
		CachedAt:     cachedAt,
		LastUsedAt:   cachedAt,
		ExpiresAt:    cachedAt.Add(ttl),
	}
}

func TestSearchCacheStoreFetchRoundTrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openMigrated(t, ":memory:")
	store := database.SearchCacheStore{}
	instID := insertInstance(t, db, "tracker-a")
	now := time.Now().UTC().Truncate(time.Second)

	want := sampleEntry("key-1", instID, now, time.Hour)
	if err := store.Store(ctx, db, want); err != nil {
		t.Fatalf("Store: %v", err)
	}

	got, found, err := store.Fetch(ctx, db, "key-1", now.Add(time.Minute))
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if !found {
		t.Fatal("Fetch: entry not found")
	}
	if got.CacheKey != want.CacheKey || got.InstanceID != want.InstanceID || got.TotalResults != want.TotalResults {
		t.Errorf("scalar mismatch: got %+v", got)
	}
	if string(got.ResultsJSON) != string(want.ResultsJSON) {
		t.Errorf("results_json mismatch: got %q", got.ResultsJSON)
	}
	if !got.CachedAt.Equal(want.CachedAt) || !got.ExpiresAt.Equal(want.ExpiresAt) {
		t.Errorf("timestamp mismatch: cachedAt=%v expiresAt=%v", got.CachedAt, got.ExpiresAt)
	}
	if got.HitCount != 0 {
		t.Errorf("fresh entry HitCount=%d, want 0", got.HitCount)
	}
}

func TestSearchCacheFetchExpired(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openMigrated(t, ":memory:")
	store := database.SearchCacheStore{}
	instID := insertInstance(t, db, "tracker-a")
	now := time.Now().UTC().Truncate(time.Second)

	if err := store.Store(ctx, db, sampleEntry("key-1", instID, now, time.Minute)); err != nil {
		t.Fatalf("Store: %v", err)
	}

	_, found, err := store.Fetch(ctx, db, "key-1", now.Add(2*time.Minute))
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if found {
		t.Fatal("expired entry should not be returned")
	}
}

func TestSearchCacheStoreRejectsNonPositiveTTL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openMigrated(t, ":memory:")
	store := database.SearchCacheStore{}
	instID := insertInstance(t, db, "tracker-a")
	now := time.Now().UTC().Truncate(time.Second)

	e := sampleEntry("key-1", instID, now, 0)
	e.ExpiresAt = now // == cached_at
	if err := store.Store(ctx, db, e); err == nil {
		t.Fatal("Store should reject expires_at <= cached_at")
	}
}

func TestSearchCacheCascadeDelete(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openMigrated(t, ":memory:")
	store := database.SearchCacheStore{}
	instID := insertInstance(t, db, "tracker-a")
	now := time.Now().UTC().Truncate(time.Second)

	if err := store.Store(ctx, db, sampleEntry("key-1", instID, now, time.Hour)); err != nil {
		t.Fatalf("Store: %v", err)
	}
	if err := (database.Instances{}).Delete(ctx, db, "tracker-a"); err != nil {
		t.Fatalf("Delete instance: %v", err)
	}

	_, found, err := store.Fetch(ctx, db, "key-1", now)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if found {
		t.Fatal("cache row should cascade-delete with its instance (PRAGMA foreign_keys off?)")
	}
}

func TestSearchCacheDelete(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		seed  bool // whether to Store a row at "key-1" before deleting it
		key   string
		after bool // whether "key-1" should still be found after Delete
	}{
		{name: "existing row is removed", seed: true, key: "key-1", after: false},
		{name: "absent key is a no-op, not an error", seed: false, key: "missing", after: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			db := openMigrated(t, ":memory:")
			store := database.SearchCacheStore{}
			instID := insertInstance(t, db, "tracker-a")
			now := time.Now().UTC().Truncate(time.Second)

			if tt.seed {
				if err := store.Store(ctx, db, sampleEntry("key-1", instID, now, time.Hour)); err != nil {
					t.Fatalf("Store: %v", err)
				}
			}

			if err := store.Delete(ctx, db, tt.key); err != nil {
				t.Fatalf("Delete: %v", err)
			}

			_, found, err := store.Fetch(ctx, db, "key-1", now)
			if err != nil {
				t.Fatalf("Fetch: %v", err)
			}
			if found != tt.after {
				t.Errorf("Fetch found=%v after Delete(%q), want %v", found, tt.key, tt.after)
			}
		})
	}
}

func TestSearchCacheTouchIncrements(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openMigrated(t, ":memory:")
	store := database.SearchCacheStore{}
	instID := insertInstance(t, db, "tracker-a")
	now := time.Now().UTC().Truncate(time.Second)

	if err := store.Store(ctx, db, sampleEntry("key-1", instID, now, time.Hour)); err != nil {
		t.Fatalf("Store: %v", err)
	}

	for i := int64(1); i <= 3; i++ {
		if err := store.Touch(ctx, db, "key-1", now.Add(time.Duration(i)*time.Minute)); err != nil {
			t.Fatalf("Touch: %v", err)
		}
		got, _, err := store.Fetch(ctx, db, "key-1", now.Add(time.Hour/2))
		if err != nil {
			t.Fatalf("Fetch: %v", err)
		}
		if got.HitCount != i {
			t.Errorf("after %d touches HitCount=%d, want %d", i, got.HitCount, i)
		}
	}
}

func TestSearchCacheReStorePreservesHitCount(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openMigrated(t, ":memory:")
	store := database.SearchCacheStore{}
	instID := insertInstance(t, db, "tracker-a")
	now := time.Now().UTC().Truncate(time.Second)

	if err := store.Store(ctx, db, sampleEntry("key-1", instID, now, time.Hour)); err != nil {
		t.Fatalf("Store: %v", err)
	}
	if err := store.Touch(ctx, db, "key-1", now.Add(time.Minute)); err != nil {
		t.Fatalf("Touch: %v", err)
	}

	// A SWR refresh write-back: re-Store with fresh payload/timestamps.
	refreshed := sampleEntry("key-1", instID, now.Add(time.Hour/2), time.Hour)
	refreshed.TotalResults = 5
	if err := store.Store(ctx, db, refreshed); err != nil {
		t.Fatalf("re-Store: %v", err)
	}

	got, _, err := store.Fetch(ctx, db, "key-1", now.Add(time.Hour/2+time.Minute))
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if got.HitCount != 1 {
		t.Errorf("re-Store should preserve HitCount: got %d, want 1", got.HitCount)
	}
	if got.TotalResults != 5 {
		t.Errorf("re-Store should refresh payload: TotalResults=%d, want 5", got.TotalResults)
	}
}

func TestSearchCacheCleanupExpired(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openMigrated(t, ":memory:")
	store := database.SearchCacheStore{}
	instID := insertInstance(t, db, "tracker-a")
	now := time.Now().UTC().Truncate(time.Second)

	if err := store.Store(ctx, db, sampleEntry("fresh", instID, now, time.Hour)); err != nil {
		t.Fatalf("Store fresh: %v", err)
	}
	if err := store.Store(ctx, db, sampleEntry("stale", instID, now, time.Minute)); err != nil {
		t.Fatalf("Store stale: %v", err)
	}

	n, err := store.CleanupExpired(ctx, db, now.Add(2*time.Minute))
	if err != nil {
		t.Fatalf("CleanupExpired: %v", err)
	}
	if n != 1 {
		t.Errorf("CleanupExpired purged %d, want 1", n)
	}
	if _, found, _ := store.Fetch(ctx, db, "fresh", now.Add(3*time.Minute)); !found {
		t.Error("fresh entry should survive cleanup")
	}
}

func TestSearchCacheFlush(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openMigrated(t, ":memory:")
	store := database.SearchCacheStore{}
	instID := insertInstance(t, db, "tracker-a")
	now := time.Now().UTC().Truncate(time.Second)

	for _, k := range []string{"a", "b", "c"} {
		if err := store.Store(ctx, db, sampleEntry(k, instID, now, time.Hour)); err != nil {
			t.Fatalf("Store %q: %v", k, err)
		}
	}

	n, err := store.Flush(ctx, db)
	if err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if n != 3 {
		t.Errorf("Flush purged %d, want 3", n)
	}
}

func TestSearchCacheInvalidateByInstance(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openMigrated(t, ":memory:")
	store := database.SearchCacheStore{}
	instA := insertInstance(t, db, "tracker-a")
	instB := insertInstance(t, db, "tracker-b")
	now := time.Now().UTC().Truncate(time.Second)

	if err := store.Store(ctx, db, sampleEntry("a1", instA, now, time.Hour)); err != nil {
		t.Fatalf("Store a1: %v", err)
	}
	if err := store.Store(ctx, db, sampleEntry("a2", instA, now, time.Hour)); err != nil {
		t.Fatalf("Store a2: %v", err)
	}
	if err := store.Store(ctx, db, sampleEntry("b1", instB, now, time.Hour)); err != nil {
		t.Fatalf("Store b1: %v", err)
	}

	n, err := store.InvalidateByInstance(ctx, db, instA)
	if err != nil {
		t.Fatalf("InvalidateByInstance: %v", err)
	}
	if n != 2 {
		t.Errorf("InvalidateByInstance purged %d, want 2", n)
	}
	if _, found, _ := store.Fetch(ctx, db, "b1", now.Add(time.Minute)); !found {
		t.Error("other instance's entry should survive invalidation")
	}
}

// TestSearchCacheExpireAll proves ExpireAll marks every currently-live entry
// expired WITHOUT deleting it — Fetch stops serving it but FetchAny still finds
// it — while leaving an already-expired row's expires_at (and thus its reap-grace
// clock) untouched.
func TestSearchCacheExpireAll(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openMigrated(t, ":memory:")
	store := database.SearchCacheStore{}
	instID := insertInstance(t, db, "tracker-a")
	now := time.Now().UTC().Truncate(time.Second)

	if err := store.Store(ctx, db, sampleEntry("live", instID, now, time.Hour)); err != nil {
		t.Fatalf("Store live: %v", err)
	}
	staleExpiresAt := now.Add(-time.Hour) // cachedAt now-2h + 1h ttl
	if err := store.Store(ctx, db, sampleEntry("stale", instID, now.Add(-2*time.Hour), time.Hour)); err != nil {
		t.Fatalf("Store stale: %v", err)
	}

	n, err := store.ExpireAll(ctx, db, now)
	if err != nil {
		t.Fatalf("ExpireAll: %v", err)
	}
	if n != 1 {
		t.Fatalf("ExpireAll affected %d rows, want 1 (only the live entry)", n)
	}

	if _, found, err := store.Fetch(ctx, db, "live", now); err != nil {
		t.Fatalf("Fetch live: %v", err)
	} else if found {
		t.Error("live entry should be expired (Fetch) after ExpireAll")
	}
	got, found, err := store.FetchAny(ctx, db, "live")
	if err != nil {
		t.Fatalf("FetchAny live: %v", err)
	}
	if !found {
		t.Fatal("live entry should still be readable via FetchAny after ExpireAll (expire, not delete)")
	}
	if !got.ExpiresAt.Equal(now) {
		t.Errorf("live entry expires_at = %v, want %v (set to now)", got.ExpiresAt, now)
	}

	gotStale, found, err := store.FetchAny(ctx, db, "stale")
	if err != nil {
		t.Fatalf("FetchAny stale: %v", err)
	}
	if !found {
		t.Fatal("already-expired entry should survive ExpireAll (it must not delete)")
	}
	if !gotStale.ExpiresAt.Equal(staleExpiresAt) {
		t.Errorf("already-expired entry's expires_at = %v, want unchanged %v", gotStale.ExpiresAt, staleExpiresAt)
	}
}

func TestSearchCacheStats(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openMigrated(t, ":memory:")
	store := database.SearchCacheStore{}
	instID := insertInstance(t, db, "tracker-a")
	now := time.Now().UTC().Truncate(time.Second)

	// Empty cache: zero counts, nil timestamps.
	empty, err := store.Stats(ctx, db)
	if err != nil {
		t.Fatalf("Stats empty: %v", err)
	}
	if empty.Entries != 0 || empty.TotalHits != 0 || empty.ApproxSizeBytes != 0 {
		t.Errorf("empty stats: %+v", empty)
	}
	if empty.Oldest != nil || empty.Newest != nil || empty.LastUsed != nil {
		t.Errorf("empty stats should have nil timestamps: %+v", empty)
	}

	older := sampleEntry("old", instID, now.Add(-time.Hour), 2*time.Hour)
	newer := sampleEntry("new", instID, now, time.Hour)
	if err := store.Store(ctx, db, older); err != nil {
		t.Fatalf("Store old: %v", err)
	}
	if err := store.Store(ctx, db, newer); err != nil {
		t.Fatalf("Store new: %v", err)
	}
	if err := store.Touch(ctx, db, "old", now); err != nil {
		t.Fatalf("Touch: %v", err)
	}

	s, err := store.Stats(ctx, db)
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if s.Entries != 2 {
		t.Errorf("Entries=%d, want 2", s.Entries)
	}
	if s.TotalHits != 1 {
		t.Errorf("TotalHits=%d, want 1", s.TotalHits)
	}
	wantSize := int64(len(older.ResultsJSON) + len(newer.ResultsJSON))
	if s.ApproxSizeBytes != wantSize {
		t.Errorf("ApproxSizeBytes=%d, want %d", s.ApproxSizeBytes, wantSize)
	}
	if s.Oldest == nil || !s.Oldest.Equal(older.CachedAt) {
		t.Errorf("Oldest=%v, want %v", s.Oldest, older.CachedAt)
	}
	if s.Newest == nil || !s.Newest.Equal(newer.CachedAt) {
		t.Errorf("Newest=%v, want %v", s.Newest, newer.CachedAt)
	}
}

func TestSearchCacheStatsByInstance(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openMigrated(t, ":memory:")
	store := database.SearchCacheStore{}
	instA := insertInstance(t, db, "tracker-a")
	instB := insertInstance(t, db, "tracker-b")
	now := time.Now().UTC().Truncate(time.Second)

	// Empty cache yields no rows.
	rows, err := store.StatsByInstance(ctx, db)
	if err != nil {
		t.Fatalf("StatsByInstance empty: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("empty StatsByInstance returned %d rows, want 0", len(rows))
	}

	// instA: two entries, three served hits; instB: one entry, no hits.
	a1 := sampleEntry("a1", instA, now, time.Hour)
	a2 := sampleEntry("a2", instA, now, time.Hour)
	b1 := sampleEntry("b1", instB, now, time.Hour)
	for _, e := range []database.SearchCacheEntry{a1, a2, b1} {
		if err := store.Store(ctx, db, e); err != nil {
			t.Fatalf("Store %s: %v", e.CacheKey, err)
		}
	}
	if err := store.BumpHits(ctx, db, "a1", 3, now); err != nil {
		t.Fatalf("BumpHits: %v", err)
	}

	rows, err = store.StatsByInstance(ctx, db)
	if err != nil {
		t.Fatalf("StatsByInstance: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("StatsByInstance returned %d rows, want 2", len(rows))
	}
	// Ordered by instance_id, so instA first.
	if rows[0].InstanceID != instA || rows[0].Entries != 2 || rows[0].HitsSaved != 3 {
		t.Errorf("instA stats = %+v, want entries=2 hitsSaved=3", rows[0])
	}
	if rows[0].ApproxSizeBytes != int64(len(a1.ResultsJSON)+len(a2.ResultsJSON)) {
		t.Errorf("instA size = %d, want %d", rows[0].ApproxSizeBytes, len(a1.ResultsJSON)+len(a2.ResultsJSON))
	}
	if rows[1].InstanceID != instB || rows[1].Entries != 1 || rows[1].HitsSaved != 0 {
		t.Errorf("instB stats = %+v, want entries=1 hitsSaved=0", rows[1])
	}
}

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

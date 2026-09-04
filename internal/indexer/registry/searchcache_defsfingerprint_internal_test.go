package registry

import (
	"context"
	"testing"
	"time"

	"github.com/autobrr/harbrr/internal/database"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
)

// fpA/fpB stand in for two different content hashes of one definition; the check
// only ever compares them for equality, so their shape is irrelevant.
const (
	fpA = "aaaaaaaaaaaaaaaa"
	fpB = "bbbbbbbbbbbbbbbb"
)

// TestEnsureDefsFingerprints_FirstBootStoresWithoutExpiry proves an absent stored
// map with no legacy fingerprint either (a true first boot) just persists the
// computed map — there is nothing to compare against yet, so nothing is expired.
func TestEnsureDefsFingerprints_FirstBootStoresWithoutExpiry(t *testing.T) {
	t.Parallel()
	sc, instID, _ := testCache(t, keywordTTL, 0)
	ctx := context.Background()
	sc.storeBestEffort(ctx, cacheOp{instanceID: instID, q: search.Query{Keywords: "x"}, key: "k"}, relSet("A"))

	if err := sc.EnsureDefsFingerprints(ctx, map[string]string{"fakedef": fpA}); err != nil {
		t.Fatalf("EnsureDefsFingerprints: %v", err)
	}

	assertServes(t, sc, "k", true, "live entry should still serve after a first-boot fingerprint store")
	stored, found, err := database.AppSettings{}.Get(ctx, sc.db, keyCacheDefsFingerprints)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !found || stored != `{"fakedef":"`+fpA+`"}` {
		t.Errorf("stored fingerprints = %q, found=%v, want the JSON map", stored, found)
	}
}

// TestEnsureDefsFingerprints_UnchangedIsNoOp proves a re-check with the SAME map
// (an ordinary restart with no def-content change) never expires anything.
func TestEnsureDefsFingerprints_UnchangedIsNoOp(t *testing.T) {
	t.Parallel()
	sc, instID, _ := testCache(t, keywordTTL, 0)
	ctx := context.Background()
	sc.storeBestEffort(ctx, cacheOp{instanceID: instID, q: search.Query{Keywords: "x"}, key: "k"}, relSet("A"))
	fps := map[string]string{"fakedef": fpA, "otherdef": fpB}

	if err := sc.EnsureDefsFingerprints(ctx, fps); err != nil {
		t.Fatalf("first EnsureDefsFingerprints: %v", err)
	}
	if err := sc.EnsureDefsFingerprints(ctx, fps); err != nil {
		t.Fatalf("second EnsureDefsFingerprints: %v", err)
	}

	assertServes(t, sc, "k", true, "live entry should still serve; no definition changed")
}

// TestEnsureDefsFingerprints_ExpiresOnlyTheChangedDefsInstances is the #388 defect
// fix: an edit to ONE definition expires only the rows of the instances backed by
// it — every other indexer keeps serving from cache — and the expired rows are
// expired, not deleted (FetchAny still reads them).
func TestEnsureDefsFingerprints_ExpiresOnlyTheChangedDefsInstances(t *testing.T) {
	t.Parallel()
	sc, changedInst, otherInst := twoInstanceCache(t)
	ctx := context.Background()
	sc.storeBestEffort(ctx, cacheOp{instanceID: changedInst, q: search.Query{Keywords: "x"}, key: "changed"}, relSet("A"))
	sc.storeBestEffort(ctx, cacheOp{instanceID: otherInst, q: search.Query{Keywords: "y"}, key: "other"}, relSet("B"))

	if err := sc.EnsureDefsFingerprints(ctx, map[string]string{"fakedef": fpA, "otherdef": fpA}); err != nil {
		t.Fatalf("seed fingerprints: %v", err)
	}
	if err := sc.EnsureDefsFingerprints(ctx, map[string]string{"fakedef": fpB, "otherdef": fpA}); err != nil {
		t.Fatalf("changed fingerprints: %v", err)
	}

	assertServes(t, sc, "changed", false, "the changed definition's instance must stop serving its cached rows")
	assertServes(t, sc, "other", true, "an unchanged definition's instance must keep serving its cached rows")
	if _, found, err := sc.store.FetchAny(ctx, sc.db, "changed"); err != nil || !found {
		t.Errorf("expired row should still be readable via FetchAny (expire, not delete): found=%v err=%v", found, err)
	}
	stored, _, err := database.AppSettings{}.Get(ctx, sc.db, keyCacheDefsFingerprints)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if stored != `{"fakedef":"`+fpB+`","otherdef":"`+fpA+`"}` {
		t.Errorf("stored fingerprints = %q, want the new map", stored)
	}
}

// TestEnsureDefsFingerprints_AddedAndRemovedDefs proves an appearing definition
// touches nobody's cache (nothing was cached under it), while a DISAPPEARING one
// expires its instances' rows — those rows were shaped by a definition that is
// gone.
func TestEnsureDefsFingerprints_AddedAndRemovedDefs(t *testing.T) {
	t.Parallel()
	sc, fakedefInst, otherdefInst := twoInstanceCache(t)
	ctx := context.Background()
	sc.storeBestEffort(ctx, cacheOp{instanceID: fakedefInst, q: search.Query{Keywords: "x"}, key: "fakedef-row"}, relSet("A"))
	sc.storeBestEffort(ctx, cacheOp{instanceID: otherdefInst, q: search.Query{Keywords: "y"}, key: "otherdef-row"}, relSet("B"))

	if err := sc.EnsureDefsFingerprints(ctx, map[string]string{"fakedef": fpA, "otherdef": fpA}); err != nil {
		t.Fatalf("seed fingerprints: %v", err)
	}
	// "newdef" appears; "otherdef" disappears; "fakedef" is untouched.
	if err := sc.EnsureDefsFingerprints(ctx, map[string]string{"fakedef": fpA, "newdef": fpB}); err != nil {
		t.Fatalf("changed fingerprints: %v", err)
	}

	assertServes(t, sc, "fakedef-row", true, "an unchanged definition's instance must keep serving its cached rows")
	assertServes(t, sc, "otherdef-row", false, "a removed definition's instance must stop serving its cached rows")
}

// TestEnsureDefsFingerprints_LegacyStringUpgradesWithOneFullExpire proves the
// upgrade path: a stored legacy corpus-wide fingerprint with no per-definition map
// yet cannot be diffed, so it triggers exactly one full expire (all instances) and
// then persists the map — and the NEXT boot with the same map expires nothing.
func TestEnsureDefsFingerprints_LegacyStringUpgradesWithOneFullExpire(t *testing.T) {
	t.Parallel()
	sc, fakedefInst, otherdefInst := twoInstanceCache(t)
	ctx := context.Background()
	if err := (database.AppSettings{}).Set(ctx, sc.db, keyCacheDefsFingerprint, "legacy-corpus-hash", sc.clock()); err != nil {
		t.Fatalf("seed legacy fingerprint: %v", err)
	}
	sc.storeBestEffort(ctx, cacheOp{instanceID: fakedefInst, q: search.Query{Keywords: "x"}, key: "fakedef-row"}, relSet("A"))
	sc.storeBestEffort(ctx, cacheOp{instanceID: otherdefInst, q: search.Query{Keywords: "y"}, key: "otherdef-row"}, relSet("B"))

	fps := map[string]string{"fakedef": fpA, "otherdef": fpA}
	if err := sc.EnsureDefsFingerprints(ctx, fps); err != nil {
		t.Fatalf("upgrade EnsureDefsFingerprints: %v", err)
	}

	assertServes(t, sc, "fakedef-row", false, "the legacy upgrade must expire every instance's rows once")
	assertServes(t, sc, "otherdef-row", false, "the legacy upgrade must expire every instance's rows once")
	if _, found, err := sc.store.FetchAny(ctx, sc.db, "fakedef-row"); err != nil || !found {
		t.Errorf("expired row should still be readable via FetchAny (expire, not delete): found=%v err=%v", found, err)
	}

	// Second boot, same defs: the map is now stored, so nothing is expired again.
	sc.storeBestEffort(ctx, cacheOp{instanceID: fakedefInst, q: search.Query{Keywords: "z"}, key: "after-upgrade"}, relSet("C"))
	if err := sc.EnsureDefsFingerprints(ctx, fps); err != nil {
		t.Fatalf("second EnsureDefsFingerprints: %v", err)
	}
	assertServes(t, sc, "after-upgrade", true, "the legacy upgrade must be one-time, not every boot")
}

// TestEnsureDefsFingerprints_UnreadableStoredMapRecovers proves a stored value
// that is not a fingerprint map — hand-edited garbage, or a JSON "null" that
// unmarshals cleanly into a NIL map — is logged and replaced rather than compared
// against: comparing would read every definition as newly added and expire every
// indexer's cache. Nothing is expired, and the next boot compares normally.
func TestEnsureDefsFingerprints_UnreadableStoredMapRecovers(t *testing.T) {
	t.Parallel()
	for _, stored := range []string{"not json at all", "null"} {
		t.Run(stored, func(t *testing.T) {
			t.Parallel()
			sc, fakedefInst, otherdefInst := twoInstanceCache(t)
			ctx := context.Background()
			if err := (database.AppSettings{}).Set(ctx, sc.db, keyCacheDefsFingerprints, stored, sc.clock()); err != nil {
				t.Fatalf("seed stored value: %v", err)
			}
			sc.storeBestEffort(ctx, cacheOp{instanceID: fakedefInst, q: search.Query{Keywords: "x"}, key: "fakedef-row"}, relSet("A"))
			sc.storeBestEffort(ctx, cacheOp{instanceID: otherdefInst, q: search.Query{Keywords: "y"}, key: "otherdef-row"}, relSet("B"))

			fps := map[string]string{"fakedef": fpA, "otherdef": fpA}
			if err := sc.EnsureDefsFingerprints(ctx, fps); err != nil {
				t.Fatalf("EnsureDefsFingerprints: %v", err)
			}

			assertServes(t, sc, "fakedef-row", true, "an unreadable stored map must not trigger an expiry storm")
			assertServes(t, sc, "otherdef-row", true, "an unreadable stored map must not trigger an expiry storm")

			// Next boot: the map is stored properly now, so a real change is detected.
			if err := sc.EnsureDefsFingerprints(ctx, map[string]string{"fakedef": fpB, "otherdef": fpA}); err != nil {
				t.Fatalf("second EnsureDefsFingerprints: %v", err)
			}
			assertServes(t, sc, "fakedef-row", false, "after recovery the next change must expire the changed def's rows")
			assertServes(t, sc, "otherdef-row", true, "after recovery an unchanged def's rows must survive")
		})
	}
}

// TestChangedDefs covers the add/remove/edit/unchanged diff in one table.
func TestChangedDefs(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		prev, next map[string]string
		want       []string
	}{
		{name: "no change", prev: map[string]string{"a": fpA}, next: map[string]string{"a": fpA}},
		{name: "edited", prev: map[string]string{"a": fpA, "b": fpA}, next: map[string]string{"a": fpB, "b": fpA}, want: []string{"a"}},
		{name: "added", prev: map[string]string{"a": fpA}, next: map[string]string{"a": fpA, "b": fpB}, want: []string{"b"}},
		{name: "removed", prev: map[string]string{"a": fpA, "b": fpB}, next: map[string]string{"a": fpA}, want: []string{"b"}},
		{name: "sorted by id", prev: map[string]string{}, next: map[string]string{"b": fpB, "a": fpA}, want: []string{"a", "b"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := changedDefs(tt.prev, tt.next)
			if len(got) != len(tt.want) {
				t.Fatalf("changedDefs = %v, want ids %v", got, tt.want)
			}
			for i, ch := range got {
				if ch.id != tt.want[i] {
					t.Errorf("changed[%d].id = %q, want %q", i, ch.id, tt.want[i])
				}
			}
		})
	}
}

// twoInstanceCache builds a cache with two instances on two different definitions:
// the testCache default ("fakedef") plus a second one on "otherdef".
func twoInstanceCache(t *testing.T) (sc *SearchCache, fakedefInst, otherdefInst int64) {
	t.Helper()
	sc, fakedefInst, _ = testCache(t, keywordTTL, 0)
	now := time.Now().UTC().Format("2006-01-02T15:04:05Z07:00")
	res, err := sc.db.ExecContext(context.Background(),
		`INSERT INTO indexer_instances (slug, definition_id, name, base_url, enabled, created_at, updated_at)
		 VALUES (?, ?, ?, ?, 1, ?, ?)`,
		"other", "otherdef", "Other", "", now, now)
	if err != nil {
		t.Fatalf("insert second instance: %v", err)
	}
	otherdefInst, err = res.LastInsertId()
	if err != nil {
		t.Fatalf("last insert id: %v", err)
	}
	return sc, fakedefInst, otherdefInst
}

// assertServes checks whether key is still live (Fetch, which filters on expires_at).
func assertServes(t *testing.T, sc *SearchCache, key string, want bool, msg string) {
	t.Helper()
	_, found, err := sc.store.Fetch(context.Background(), sc.db, key, sc.clock())
	if err != nil {
		t.Fatalf("Fetch %q: %v", key, err)
	}
	if found != want {
		t.Errorf("%s (Fetch %q found=%v, want %v)", msg, key, found, want)
	}
}

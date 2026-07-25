package registry

import (
	"context"
	"testing"

	"github.com/autobrr/harbrr/internal/database"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
)

// TestEnsureDefsFingerprint_FirstBootStoresWithoutExpiry proves an absent stored
// fingerprint (first boot with this feature) just persists the computed value —
// there is nothing to compare against yet, so nothing is expired.
func TestEnsureDefsFingerprint_FirstBootStoresWithoutExpiry(t *testing.T) {
	t.Parallel()
	sc, instID, _ := testCache(t, keywordTTL, 0)
	ctx := context.Background()
	sc.storeBestEffort(ctx, instID, map[string]string{}, 0, search.Query{Keywords: "x"}, "k", relSet("A"))

	if err := sc.EnsureDefsFingerprint(ctx, "fp-1"); err != nil {
		t.Fatalf("EnsureDefsFingerprint: %v", err)
	}

	if _, found, err := sc.store.Fetch(ctx, sc.db, "k", sc.clock()); err != nil {
		t.Fatalf("Fetch: %v", err)
	} else if !found {
		t.Error("live entry should still serve after a first-boot fingerprint store")
	}
	stored, found, err := database.AppSettings{}.Get(ctx, sc.db, keyCacheDefsFingerprint)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !found || stored != "fp-1" {
		t.Errorf("stored fingerprint = %q, found=%v, want fp-1", stored, found)
	}
}

// TestEnsureDefsFingerprint_SameFingerprintIsNoOp proves a re-check with the SAME
// fingerprint (an ordinary restart with no def-content change) never expires
// anything.
func TestEnsureDefsFingerprint_SameFingerprintIsNoOp(t *testing.T) {
	t.Parallel()
	sc, instID, _ := testCache(t, keywordTTL, 0)
	ctx := context.Background()
	sc.storeBestEffort(ctx, instID, map[string]string{}, 0, search.Query{Keywords: "x"}, "k", relSet("A"))

	if err := sc.EnsureDefsFingerprint(ctx, "fp-1"); err != nil {
		t.Fatalf("first EnsureDefsFingerprint: %v", err)
	}
	if err := sc.EnsureDefsFingerprint(ctx, "fp-1"); err != nil {
		t.Fatalf("second EnsureDefsFingerprint: %v", err)
	}

	if _, found, err := sc.store.Fetch(ctx, sc.db, "k", sc.clock()); err != nil {
		t.Fatalf("Fetch: %v", err)
	} else if !found {
		t.Error("live entry should still serve; the fingerprint did not change")
	}
}

// TestEnsureDefsFingerprint_ChangedFingerprintExpiresLiveEntries proves a
// mismatch (def content changed since the last boot) expires every live entry —
// never deletes it — and persists the new fingerprint.
func TestEnsureDefsFingerprint_ChangedFingerprintExpiresLiveEntries(t *testing.T) {
	t.Parallel()
	sc, instID, _ := testCache(t, keywordTTL, 0)
	ctx := context.Background()
	sc.storeBestEffort(ctx, instID, map[string]string{}, 0, search.Query{Keywords: "x"}, "k", relSet("A"))

	if err := sc.EnsureDefsFingerprint(ctx, "fp-1"); err != nil {
		t.Fatalf("seed fingerprint: %v", err)
	}
	if err := sc.EnsureDefsFingerprint(ctx, "fp-2"); err != nil {
		t.Fatalf("changed fingerprint: %v", err)
	}

	if _, found, err := sc.store.Fetch(ctx, sc.db, "k", sc.clock()); err != nil {
		t.Fatalf("Fetch: %v", err)
	} else if found {
		t.Error("live entry should be expired (Fetch) after a fingerprint change")
	}
	if _, found, err := sc.store.FetchAny(ctx, sc.db, "k"); err != nil || !found {
		t.Errorf("entry should still be readable via FetchAny after expiry (expire, not delete): found=%v err=%v", found, err)
	}
	stored, found, err := database.AppSettings{}.Get(ctx, sc.db, keyCacheDefsFingerprint)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !found || stored != "fp-2" {
		t.Errorf("stored fingerprint = %q, found=%v, want fp-2", stored, found)
	}
}

package appsync

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
)

// TestUpdateConnectionProfileTOCTOU pins the transactional read-modify-write in
// UpdateConnection against the TOCTOU it exists to close: a connection attaches a
// profile while, concurrently, that profile is narrowed to categories the
// connection's kind cannot consume. The paired guards (validateProfileRef on the
// attach, validateProfileInUse on the narrow) only hold if the ref check and the ref
// write share one transaction — otherwise both operations pass their check against
// stale state and commit, leaving a full-sync Sonarr connection pointing at a
// movies/books-only profile whose gate is empty (the next sync deletes every indexer
// it manages). The invariant asserted here is exactly that impossible state: if the
// connection ends up referencing the profile, the profile must still overlap the
// connection's kind. Runs many fresh interleavings under -race.
func TestUpdateConnectionProfileTOCTOU(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	for i := range 40 {
		f := newSyncFixture(t)

		// A TV profile the Sonarr connection can consume — but not yet attached, so the
		// narrow's in-use guard passes at the moment the goroutines start.
		tv, err := f.svc.CreateProfile(ctx, CreateProfileParams{Name: "tv", Categories: []int{5000}})
		if err != nil {
			t.Fatalf("iter %d: CreateProfile: %v", i, err)
		}

		var wg sync.WaitGroup
		wg.Add(2)
		// Attach the profile to the Sonarr connection.
		go func() {
			defer wg.Done()
			_ = f.svc.UpdateConnection(ctx, f.conn.ID, UpdateConnectionParams{
				SyncProfileID: RefUpdate{Present: true, Value: &tv.ID},
			})
		}()
		// Narrow the profile to books-only, which no Sonarr connection can consume.
		go func() {
			defer wg.Done()
			books := []int{7000}
			_ = f.svc.UpdateProfile(ctx, tv.ID, UpdateProfileParams{Categories: &books})
		}()
		wg.Wait()

		conn, err := f.svc.GetConnection(ctx, f.conn.ID)
		if err != nil {
			t.Fatalf("iter %d: GetConnection: %v", i, err)
		}
		if conn.SyncProfileID == nil || *conn.SyncProfileID != tv.ID {
			continue // the narrow won; the attach was correctly rejected
		}
		prof, err := f.svc.GetProfile(ctx, tv.ID)
		if err != nil {
			t.Fatalf("iter %d: GetProfile: %v", i, err)
		}
		if !profileOverlapsKind(conn.Kind, prof.Categories) {
			t.Fatalf("iter %d: empty-gate TOCTOU: %s connection references profile with categories %v (no overlap)",
				i, conn.Kind, prof.Categories)
		}
	}
}

// TestUpdateConnectionNoLostUpdate pins that two overlapping UpdateConnection patches
// — one rotating the app API key, one changing priority — cannot lose each other's
// write. Each UpdateConnection is a full-row read-modify-write; without a transaction
// the two reads both see the pre-write row and the second commit reverts the first
// field (a rotated key silently reverting → every later sync 401s). With the RMW under
// one transaction (serialized by the single DB connection) the second writer reads the
// first's commit, so both a fresh key and the new priority survive. Runs many
// interleavings under -race, asserting both fields landed each time.
func TestUpdateConnectionNoLostUpdate(t *testing.T) {
	t.Parallel()
	f := newSyncFixture(t)
	ctx := context.Background()

	for i := range 40 {
		wantKey := fmt.Sprintf("rotated-key-%d", i)
		wantPriority := i + 1

		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			if err := f.svc.UpdateConnection(ctx, f.conn.ID, UpdateConnectionParams{APIKey: &wantKey}); err != nil {
				t.Errorf("iter %d: rotate key: %v", i, err)
			}
		}()
		go func() {
			defer wg.Done()
			if err := f.svc.UpdateConnection(ctx, f.conn.ID, UpdateConnectionParams{Priority: &wantPriority}); err != nil {
				t.Errorf("iter %d: set priority: %v", i, err)
			}
		}()
		wg.Wait()

		conn, err := f.svc.GetConnection(ctx, f.conn.ID)
		if err != nil {
			t.Fatalf("iter %d: GetConnection: %v", i, err)
		}
		if conn.Priority != wantPriority {
			t.Fatalf("iter %d: priority = %d, want %d (priority write lost)", i, conn.Priority, wantPriority)
		}
		gotKey, err := f.svc.keyring.Decrypt(conn.ID, secretApp, conn.APIKeyEncrypted)
		if err != nil {
			t.Fatalf("iter %d: decrypt app key: %v", i, err)
		}
		if gotKey != wantKey {
			t.Fatalf("iter %d: app key = %q, want %q (key rotation lost)", i, gotKey, wantKey)
		}
	}
}

// TestUpdateConnectionConcurrentProfileDeleteIsClean confirms finding U10-F1(c): a
// profile deleted concurrently with an attach surfaces as a clean 400 (ErrInvalid via
// validateProfileRef's not-found mapping), never a raw FK-violation 500. Because the
// RMW holds the single DB connection for its whole span, a concurrent DeleteProfile
// either commits before the tx's profile read (so validateProfileRef sees it gone and
// returns ErrInvalid) or after the connection write (the attach already succeeded) —
// no interleaving yields an FK error at the write.
func TestUpdateConnectionConcurrentProfileDeleteIsClean(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	for i := range 40 {
		f := newSyncFixture(t)
		tv, err := f.svc.CreateProfile(ctx, CreateProfileParams{Name: "tv", Categories: []int{5000}})
		if err != nil {
			t.Fatalf("iter %d: CreateProfile: %v", i, err)
		}

		var wg sync.WaitGroup
		wg.Add(2)
		var attachErr error
		go func() {
			defer wg.Done()
			attachErr = f.svc.UpdateConnection(ctx, f.conn.ID, UpdateConnectionParams{
				SyncProfileID: RefUpdate{Present: true, Value: &tv.ID},
			})
		}()
		go func() {
			defer wg.Done()
			_ = f.svc.DeleteProfile(ctx, tv.ID)
		}()
		wg.Wait()

		// Either the attach won (nil) or it was rejected as invalid input — never a
		// wrapped FK/500 that the handler couldn't classify.
		if attachErr != nil && !errors.Is(attachErr, ErrInvalid) {
			t.Fatalf("iter %d: concurrent delete surfaced non-ErrInvalid: %v", i, attachErr)
		}
	}
}

package appsync

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/autobrr/harbrr/internal/domain"
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
		// Attach the profile to the Sonarr connection. The only legitimate outcomes are
		// success or an domain.ErrInvalid rejection (e.g. the narrow won and the ref no longer
		// overlaps) — anything else is an unexpected DB/FK fault masquerading as "the
		// narrow won" below.
		var attachErr error
		go func() {
			defer wg.Done()
			attachErr = f.svc.UpdateConnection(ctx, f.conn.ID, UpdateConnectionParams{
				SyncProfileID: RefUpdate{Present: true, Value: &tv.ID},
			})
		}()
		// Narrow the profile to books-only, which no Sonarr connection can consume.
		// Likewise, only success or domain.ErrInvalid (validateProfileInUse rejecting the narrow
		// because the attach already landed) are legitimate.
		var narrowErr error
		go func() {
			defer wg.Done()
			books := []int{7000}
			narrowErr = f.svc.UpdateProfile(ctx, tv.ID, UpdateProfileParams{Categories: &books})
		}()
		wg.Wait()

		if attachErr != nil && !errors.Is(attachErr, domain.ErrInvalid) {
			t.Fatalf("iter %d: attach: unexpected non-domain.ErrInvalid error: %v", i, attachErr)
		}
		if narrowErr != nil && !errors.Is(narrowErr, domain.ErrInvalid) {
			t.Fatalf("iter %d: narrow: unexpected non-domain.ErrInvalid error: %v", i, narrowErr)
		}

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

// TestCreateConnectionProfileTOCTOU pins the same TOCTOU close as
// TestUpdateConnectionProfileTOCTOU, but for CreateConnection: attaching a profile at
// creation time races a concurrent narrow of that profile's categories. Before the fix,
// CreateConnection validated the profile ref against a bare, out-of-transaction read
// (s.db) and insertConnection then opened its own separate transaction to write the row
// — leaving a window where a concurrent narrow could land between the validate and the
// write, producing a full-sync connection persisted against a profile with no
// overlapping categories. The invariant asserted here is exactly that impossible state:
// if a connection is created referencing the profile, the profile must still overlap
// the connection's kind. Runs many fresh interleavings under -race.
func TestCreateConnectionProfileTOCTOU(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	for i := range 40 {
		f := newSyncFixture(t)

		// A TV profile the new Sonarr connection can consume — but not yet attached, so
		// the narrow's in-use guard passes at the moment the goroutines start.
		tv, err := f.svc.CreateProfile(ctx, CreateProfileParams{Name: "tv", Categories: []int{5000}})
		if err != nil {
			t.Fatalf("iter %d: CreateProfile: %v", i, err)
		}

		var wg sync.WaitGroup
		wg.Add(2)
		// Create a second Sonarr connection attached to the profile. The only legitimate
		// outcomes are success or an domain.ErrInvalid rejection (e.g. the narrow won and the ref
		// no longer overlaps) — anything else is an unexpected DB/FK fault masquerading
		// as "the narrow won" below.
		var created domain.AppConnection
		var createErr error
		go func() {
			defer wg.Done()
			created, createErr = f.svc.CreateConnection(ctx, CreateConnectionParams{
				Name:          fmt.Sprintf("Sonarr-2-%d", i),
				Kind:          domain.AppKindSonarr,
				BaseURL:       fmt.Sprintf("http://sonarr-2-%d.example", i),
				APIKey:        "app-key-2",
				HarbrrURL:     "http://harbrr:8787",
				SyncProfileID: &tv.ID,
			})
		}()
		// Narrow the profile to books-only, which no Sonarr connection can consume.
		// Likewise, only success or domain.ErrInvalid (validateProfileInUse rejecting the narrow
		// because the create already landed) are legitimate.
		var narrowErr error
		go func() {
			defer wg.Done()
			books := []int{7000}
			narrowErr = f.svc.UpdateProfile(ctx, tv.ID, UpdateProfileParams{Categories: &books})
		}()
		wg.Wait()

		if createErr != nil && !errors.Is(createErr, domain.ErrInvalid) {
			t.Fatalf("iter %d: create: unexpected non-domain.ErrInvalid error: %v", i, createErr)
		}
		if narrowErr != nil && !errors.Is(narrowErr, domain.ErrInvalid) {
			t.Fatalf("iter %d: narrow: unexpected non-domain.ErrInvalid error: %v", i, narrowErr)
		}
		if createErr != nil {
			continue // the narrow won; the create was correctly rejected
		}

		conn, err := f.svc.GetConnection(ctx, created.ID)
		if err != nil {
			t.Fatalf("iter %d: GetConnection: %v", i, err)
		}
		if conn.SyncProfileID == nil || *conn.SyncProfileID != tv.ID {
			continue // the narrow won; the connection is not attached
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
// — one renaming, one changing priority — cannot lose each other's write. Each
// UpdateConnection is a full-row read-modify-write; without a transaction the two reads
// both see the pre-write row and the second commit reverts the first field. With the
// RMW under one transaction (serialized by the single DB connection) the second writer
// reads the first's commit, so both fields survive. Runs many interleavings under
// -race, asserting both fields landed each time. (Identity/credential — base URL, api
// key, harbrr URL — are App-level now and rotate via internal/apps, not here.)
func TestUpdateConnectionNoLostUpdate(t *testing.T) {
	t.Parallel()
	f := newSyncFixture(t)
	ctx := context.Background()

	for i := range 40 {
		wantName := fmt.Sprintf("renamed-%d", i)
		wantSyncLevel := domain.SyncLevelFull
		if i%2 == 0 {
			wantSyncLevel = domain.SyncLevelAddUpdate
		}

		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			if err := f.svc.UpdateConnection(ctx, f.conn.ID, UpdateConnectionParams{Name: &wantName}); err != nil {
				t.Errorf("iter %d: rename: %v", i, err)
			}
		}()
		go func() {
			defer wg.Done()
			if err := f.svc.UpdateConnection(ctx, f.conn.ID, UpdateConnectionParams{SyncLevel: &wantSyncLevel}); err != nil {
				t.Errorf("iter %d: set sync level: %v", i, err)
			}
		}()
		wg.Wait()

		conn, err := f.svc.GetConnection(ctx, f.conn.ID)
		if err != nil {
			t.Fatalf("iter %d: GetConnection: %v", i, err)
		}
		if conn.SyncLevel != wantSyncLevel {
			t.Fatalf("iter %d: sync level = %q, want %q (write lost)", i, conn.SyncLevel, wantSyncLevel)
		}
		if conn.Name != wantName {
			t.Fatalf("iter %d: name = %q, want %q (name write lost)", i, conn.Name, wantName)
		}
	}
}

// TestUpdateConnectionConcurrentProfileDeleteIsClean confirms finding U10-F1(c): a
// profile deleted concurrently with an attach surfaces as a clean 400 (domain.ErrInvalid via
// validateProfileRef's not-found mapping), never a raw FK-violation 500. Because the
// RMW holds the single DB connection for its whole span, a concurrent DeleteProfile
// either commits before the tx's profile read (so validateProfileRef sees it gone and
// returns domain.ErrInvalid) or after the connection write (the attach already succeeded) —
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
		var deleteErr error
		go func() {
			defer wg.Done()
			deleteErr = f.svc.DeleteProfile(ctx, tv.ID)
		}()
		wg.Wait()

		// Either the attach won (nil) or it was rejected as invalid input — never a
		// wrapped FK/500 that the handler couldn't classify.
		if attachErr != nil && !errors.Is(attachErr, domain.ErrInvalid) {
			t.Fatalf("iter %d: concurrent delete surfaced non-domain.ErrInvalid: %v", i, attachErr)
		}
		// The profile exists at the start of every iteration and nothing else deletes
		// it, so DeleteProfile has exactly one legitimate outcome here: success.
		if deleteErr != nil {
			t.Fatalf("iter %d: DeleteProfile: unexpected error: %v", i, deleteErr)
		}
	}
}

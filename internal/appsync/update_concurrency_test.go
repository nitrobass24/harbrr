package appsync

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/autobrr/harbrr/internal/domain"
)

// TestUpdateConnectionConcurrentProfileDeleteIsClean pins the transactional guard on
// both sides of the profile-delete race (#365 moved the guard from a category-overlap
// check onto DeleteProfile's in-use refusal): attaching a profile to a connection races
// DeleteProfile on that same profile. Because SQLite's single connection serializes the
// two transactions, exactly one legitimate pairing can result — attach wins (commits
// first) and the delete then sees the profile in use (domain.ErrConflict); or delete wins
// (commits first) and the attach's validateProfileRef sees the profile gone
// (domain.ErrInvalid). Both succeeding, or either producing an unclassified DB/FK fault,
// is the impossible state this guards against. Runs many fresh interleavings under -race.
func TestUpdateConnectionConcurrentProfileDeleteIsClean(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	for i := range 40 {
		f := newSyncFixture(t)
		tv, err := f.svc.CreateProfile(ctx, CreateProfileParams{Name: "tv"})
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

		switch {
		case attachErr == nil && deleteErr == nil:
			t.Fatalf("iter %d: both attach and delete succeeded — the profile should have been in use", i)
		case attachErr == nil:
			if !errors.Is(deleteErr, domain.ErrConflict) {
				t.Fatalf("iter %d: attach won; delete err = %v, want domain.ErrConflict", i, deleteErr)
			}
		case deleteErr == nil:
			if !errors.Is(attachErr, domain.ErrInvalid) {
				t.Fatalf("iter %d: delete won; attach err = %v, want domain.ErrInvalid", i, attachErr)
			}
		default:
			t.Fatalf("iter %d: both failed — attach %v, delete %v", i, attachErr, deleteErr)
		}
	}
}

// TestCreateConnectionConcurrentProfileDeleteIsClean is the create-path sibling of
// TestUpdateConnectionConcurrentProfileDeleteIsClean: a new connection's profile ref
// races a concurrent DeleteProfile on that same profile, with the same two legitimate
// outcomes and none other.
func TestCreateConnectionConcurrentProfileDeleteIsClean(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	for i := range 40 {
		f := newSyncFixture(t)
		tv, err := f.svc.CreateProfile(ctx, CreateProfileParams{Name: "tv"})
		if err != nil {
			t.Fatalf("iter %d: CreateProfile: %v", i, err)
		}

		var wg sync.WaitGroup
		wg.Add(2)
		var createErr error
		go func() {
			defer wg.Done()
			_, createErr = f.svc.CreateConnection(ctx, CreateConnectionParams{
				Name: fmt.Sprintf("Sonarr-2-%d", i), Kind: domain.AppKindSonarr,
				BaseURL: fmt.Sprintf("http://sonarr-2-%d.example", i), APIKey: "app-key-2",
				HarbrrURL: "http://harbrr:8787", SyncProfileID: &tv.ID,
			})
		}()
		var deleteErr error
		go func() {
			defer wg.Done()
			deleteErr = f.svc.DeleteProfile(ctx, tv.ID)
		}()
		wg.Wait()

		switch {
		case createErr == nil && deleteErr == nil:
			t.Fatalf("iter %d: both create and delete succeeded — the profile should have been in use", i)
		case createErr == nil:
			if !errors.Is(deleteErr, domain.ErrConflict) {
				t.Fatalf("iter %d: create won; delete err = %v, want domain.ErrConflict", i, deleteErr)
			}
		case deleteErr == nil:
			if !errors.Is(createErr, domain.ErrInvalid) {
				t.Fatalf("iter %d: delete won; create err = %v, want domain.ErrInvalid", i, createErr)
			}
		default:
			t.Fatalf("iter %d: both failed — create %v, delete %v", i, createErr, deleteErr)
		}
	}
}

// TestUpdateConnectionNoLostUpdate pins that two overlapping UpdateConnection patches
// — one renaming, one changing sync level — cannot lose each other's write. Each
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

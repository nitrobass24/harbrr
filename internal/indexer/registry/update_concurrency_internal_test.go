package registry

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/autobrr/harbrr/internal/database"
	"github.com/autobrr/harbrr/internal/domain"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
	"github.com/autobrr/harbrr/internal/secrets"
)

// mamDefYAML is a minimal definition carrying two rotatable credentials — mam_id (a
// session cookie a native driver rotates via persistSetting) and apikey (a secret a
// management Update rotates) — plus an unrelated plaintext toggle. The password type
// forces mam_id secret regardless of its name; apikey classifies secret by name.
const mamDefYAML = `---
id: mamtest
name: MAM Test
description: registry concurrency fixture
language: en-US
type: private
encoding: UTF-8
links:
  - https://mam.invalid/
caps:
  categorymappings:
    - {id: 1, cat: Movies}
  modes:
    search: [q]
settings:
  - name: mam_id
    type: password
    label: mam_id session
  - name: apikey
    type: text
    label: API Key
  - name: sort
    type: text
    label: Sort
search:
  path: /browse.php
  inputs:
    q: "{{ .Keywords }}"
  rows:
    selector: table.results > tbody > tr
  fields:
    title:
      selector: a.title
    download:
      selector: a.dl
      attribute: href
    category:
      selector: td.cat
      attribute: data-cat
    size:
      selector: td.size
    seeders:
      selector: td.seeders
    leechers:
      selector: td.leechers
`

// hookedKeyring wraps a real keyring and runs a one-shot hook on the next Encrypt.
// It lets a test observe the exact moment Update's mergeSettings encrypts an incoming
// secret — the point that, in the pre-fix code, sits AFTER the out-of-tx settings read
// but BEFORE the in-tx reinsert (the lost-update window).
type hookedKeyring struct {
	inner *secrets.Keyring
	mu    sync.Mutex
	hook  func() // fired once on the next Encrypt, then cleared
}

func (h *hookedKeyring) arm(fn func()) {
	h.mu.Lock()
	h.hook = fn
	h.mu.Unlock()
}

func (h *hookedKeyring) Encrypt(instanceID int64, setting, plaintext string) (string, error) {
	h.mu.Lock()
	fn := h.hook
	h.hook = nil
	h.mu.Unlock()
	if fn != nil {
		fn()
	}
	return h.inner.Encrypt(instanceID, setting, plaintext)
}

func (h *hookedKeyring) Decrypt(instanceID int64, setting, blob string) (string, error) {
	return h.inner.Decrypt(instanceID, setting, blob)
}

func (h *hookedKeyring) KeyID() string { return h.inner.KeyID() }

// storedMAMID reads and decrypts the persisted mam_id for the instance. It returns the
// plaintext to the test for an equality check only; the value is never logged.
func storedMAMID(t *testing.T, db *database.DB, kr *secrets.Keyring, instID int64) string {
	t.Helper()
	settings, err := (database.Instances{}).Settings(context.Background(), db, instID)
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	for _, s := range settings {
		if s.Name != "mam_id" {
			continue
		}
		pt, err := kr.Decrypt(instID, "mam_id", s.ValueEncrypted)
		if err != nil {
			t.Fatalf("decrypt mam_id: %v", err)
		}
		return pt
	}
	t.Fatalf("mam_id setting missing")
	return ""
}

// TestUpdateDoesNotClobberRotatedCredential pins the transactional read-modify-write in
// Registry.Update against the lost-update it exists to close (U8R-F1): a native driver
// rotating a live credential via persistSetting (MyAnonamouse's mam_id session cookie)
// concurrently with a management Update that rotates a DIFFERENT secret (apikey). Update
// is a full settings read → merge → delete → reinsert; if the read runs on r.db OUTSIDE
// the transaction it captures the pre-rotation mam_id, and the in-tx reinsert of that
// stale merged set reverts the rotation — silently dropping the live MAM session on the
// restart-fallback store.
//
// The interleaving is made deterministic with a keyring hook: Update's mergeSettings
// encrypts the incoming apikey exactly once, in the window between its settings read and
// its reinsert. The hook fires there, releases the rotation goroutine, and waits for it
// (bounded). Pre-fix, the read is out-of-tx so the connection is free: the rotation lands
// in the window and is then reverted by the reinsert → mam_id comes back "original".
// Post-fix, the read + merge are INSIDE the tx holding the only connection
// (SetMaxOpenConns(1)), so the rotation blocks until commit and survives → the wait times
// out, Update commits, the rotation applies last, and mam_id is the rotated value.
func TestUpdateDoesNotClobberRotatedCredential(t *testing.T) {
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
	realKR, err := secrets.OpenKeyring(secrets.KeyringOptions{EncryptionKey: resolveTestKey}, zerolog.Nop())
	if err != nil {
		t.Fatalf("keyring: %v", err)
	}
	kr := &hookedKeyring{inner: realKR}
	clock := func() time.Time { return time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC) }
	reg := New(db, loader.New(dropin), kr, nil, WithClock(clock))

	inst, err := reg.Add(ctx, AddParams{
		Slug: "mam", DefinitionID: "mamtest",
		Settings: map[string]string{"mam_id": "original-session", "apikey": "orig-apikey", "sort": "seeders"},
	})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	// The rotated session the native driver persists, pre-encrypted via the real keyring
	// so the rotation goroutine issues only a raw DB Upsert (no keyring call, so it can't
	// re-enter the hook).
	const rotated = "rotated-session"
	enc, err := realKR.Encrypt(inst.ID, "mam_id", rotated)
	if err != nil {
		t.Fatalf("encrypt rotated: %v", err)
	}
	rotatedSetting := domain.IndexerSetting{Name: "mam_id", ValueEncrypted: enc, KeyID: realKR.KeyID(), IsSecret: true}

	goRotate := make(chan struct{})
	rotateDone := make(chan struct{})
	go func() {
		<-goRotate
		if err := (database.Instances{}).UpsertSetting(ctx, db, inst.ID, rotatedSetting); err != nil {
			t.Errorf("rotate upsert: %v", err)
		}
		close(rotateDone)
	}()

	// Fire inside Update's read→reinsert window: release the rotation, then wait for it
	// (bounded so the fixed code — where the rotation is blocked on the single connection —
	// doesn't hang; it simply times out and commits, and the rotation applies afterward).
	kr.arm(func() {
		close(goRotate)
		select {
		case <-rotateDone:
		case <-time.After(2 * time.Second):
		}
	})

	if err := reg.Update(ctx, "mam", UpdateParams{Settings: map[string]string{"apikey": "new-apikey"}}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	<-rotateDone // the rotation has landed (either in the window, or after commit)

	if got := storedMAMID(t, db, realKR, inst.ID); got != rotated {
		t.Fatal("mam_id reverted after concurrent Update: the rotation was clobbered by an out-of-tx read-modify-write (U8R-F1)")
	}
}

package registry_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/autobrr/harbrr/internal/database"
	"github.com/autobrr/harbrr/internal/database/dbtest"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/indexer/core"
	"github.com/autobrr/harbrr/internal/indexer/registry"
	"github.com/autobrr/harbrr/internal/secrets"

	"github.com/rs/zerolog"
)

// probeRecorder is the synchronous-drain probe sink the trigger tests inject: it
// records which slugs the registry asked to probe, without any queue, goroutine or
// tracker request. This is the seam that keeps the registry's ~90 other Add call sites
// offline — they pass no sink at all and probing is simply off.
type probeRecorder struct {
	mu    sync.Mutex
	slugs []string
}

func (p *probeRecorder) enqueue(slug string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.slugs = append(p.slugs, slug)
}

func (p *probeRecorder) seen() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.slugs...)
}

// newProbedRegistry builds a registry over the shared testtracker fixture with a probe
// recorder installed, returning both. doer may be nil (no engine ever built).
func newProbedRegistry(t *testing.T, doer search.Doer) (*registry.Registry, *database.DB, *probeRecorder) {
	t.Helper()
	db := dbtest.OpenMigrated(t)
	dropin := t.TempDir()
	if err := os.WriteFile(filepath.Join(dropin, "testtracker.yml"), []byte(defYAML), 0o600); err != nil {
		t.Fatalf("write def: %v", err)
	}
	keyring, err := secrets.OpenKeyring(secrets.KeyringOptions{EncryptionKey: testHexKey}, zerolog.Nop())
	if err != nil {
		t.Fatalf("open keyring: %v", err)
	}
	rec := &probeRecorder{}
	opts := []registry.Option{registry.WithClock(fixedClock), registry.WithProbeSink(rec.enqueue)}
	if doer != nil {
		opts = append(opts, registry.WithDoerFactory(func(registry.ClientParams) (search.Doer, error) { return doer, nil }))
	}
	return registry.New(db, loader.New(dropin), keyring, nil, opts...), db, rec
}

// addProbed creates the shared fixture indexer and returns the recorder's state reset
// to empty, so a following Update assertion sees only its own enqueues.
func addProbed(t *testing.T, reg *registry.Registry, rec *probeRecorder, settings map[string]string) {
	t.Helper()
	if _, err := reg.Add(context.Background(), registry.AddParams{
		Slug: "tt", DefinitionID: "testtracker", Settings: settings,
	}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	rec.mu.Lock()
	rec.slugs = nil
	rec.mu.Unlock()
}

// TestAddEnqueuesProbe proves creating an indexer asks for a health probe server-side,
// so an indexer created through the API — or by a future importer — has real health
// evidence without a browser firing a follow-up test (autobrr/harbrr#484).
func TestAddEnqueuesProbe(t *testing.T) {
	t.Parallel()
	reg, _, rec := newProbedRegistry(t, nil)

	if _, err := reg.Add(context.Background(), registry.AddParams{
		Slug: "tt", DefinitionID: "testtracker", Settings: map[string]string{"apikey": "k1"},
	}); err != nil {
		t.Fatalf("Add: %v", err)
	}

	if got := rec.seen(); len(got) != 1 || got[0] != "tt" {
		t.Fatalf("probes enqueued = %v, want [tt]", got)
	}
}

// TestUpdateEnqueuesProbeOnlyOnCredentialChange is the trigger's whole point: a login
// is spent when the credentials actually change, and never for a rename, a priority
// nudge, or a form resubmit that left the secret as the redacted sentinel.
func TestUpdateEnqueuesProbeOnlyOnCredentialChange(t *testing.T) {
	t.Parallel()
	name := "Renamed"
	priority := 10

	tests := []struct {
		name  string
		patch registry.UpdateParams
		want  bool
	}{
		{name: "rename only", patch: registry.UpdateParams{Name: &name}},
		{name: "priority only", patch: registry.UpdateParams{Priority: &priority}},
		{
			name:  "secret resubmitted as the redacted sentinel",
			patch: registry.UpdateParams{Settings: map[string]string{"apikey": secrets.Redacted}},
		},
		{
			name:  "settings resubmitted unchanged",
			patch: registry.UpdateParams{Settings: map[string]string{"apikey": "k1", "sort": "seeders"}},
		},
		{
			name:  "credential changed",
			patch: registry.UpdateParams{Settings: map[string]string{"apikey": "k2"}},
			want:  true,
		},
		{
			name:  "plain setting changed",
			patch: registry.UpdateParams{Settings: map[string]string{"sort": "size"}},
			want:  true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			reg, _, rec := newProbedRegistry(t, nil)
			addProbed(t, reg, rec, map[string]string{"apikey": "k1", "sort": "seeders"})

			if err := reg.Update(context.Background(), "tt", tt.patch); err != nil {
				t.Fatalf("Update: %v", err)
			}

			got := rec.seen()
			if tt.want && (len(got) != 1 || got[0] != "tt") {
				t.Fatalf("probes enqueued = %v, want [tt]", got)
			}
			if !tt.want && len(got) != 0 {
				t.Fatalf("probes enqueued = %v, want none (this change spends no login)", got)
			}
		})
	}
}

// TestFailedProbeLeavesTheRowIntact drains the probe SYNCHRONOUSLY through the real
// Resolver.Test against a tracker that rejects the credentials, proving a failing probe records health
// without unwinding the write that triggered it.
func TestFailedProbeLeavesTheRowIntact(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := dbtest.OpenMigrated(t)
	dropin := t.TempDir()
	if err := os.WriteFile(filepath.Join(dropin, "testtracker.yml"), []byte(loginDefYAML), 0o600); err != nil {
		t.Fatalf("write def: %v", err)
	}
	keyring, err := secrets.OpenKeyring(secrets.KeyringOptions{EncryptionKey: testHexKey}, zerolog.Nop())
	if err != nil {
		t.Fatalf("open keyring: %v", err)
	}

	var (
		reg      *registry.Registry
		probeErr error
		probed   int
	)
	reg = registry.New(
		db, loader.New(dropin), keyring, nil,
		registry.WithClock(fixedClock),
		registry.WithDoerFactory(func(registry.ClientParams) (search.Doer, error) {
			return &replayDoer{body: `<html><body><div class="error">Invalid credentials</div></body></html>`}, nil
		}),
		registry.WithProbeSink(func(slug string) {
			probed++
			probeErr = reg.Test(ctx, slug)
		}),
	)

	inst, err := reg.Add(ctx, registry.AddParams{
		Slug: "tt", DefinitionID: "testtracker", Settings: map[string]string{"username": "u", "password": "p"},
	})
	if err != nil {
		t.Fatalf("Add returned an error even though only the probe failed: %v", err)
	}
	if probed != 1 {
		t.Fatalf("probes run = %d, want 1", probed)
	}
	if probeErr == nil {
		t.Fatal("the probe passed against a refusing tracker; the fixture is not exercising the failure path")
	}

	got, _, err := reg.Get(ctx, "tt")
	if err != nil {
		t.Fatalf("the created indexer is gone after a failed probe: %v", err)
	}
	if got.ID != inst.ID {
		t.Fatalf("Get returned id %d, want the created %d", got.ID, inst.ID)
	}
}

// TestTestReservesQueryBudget pins the budget accounting added to the probe path: a
// login is a real outbound request, and with the probe queue driving Test automatically
// it must count against the indexer's configured quota like any other query.
func TestTestReservesQueryBudget(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	reg, _, _ := newProbedRegistry(t, &replayDoer{body: "<html><body></body></html>"})
	if _, err := reg.Add(ctx, registry.AddParams{
		Slug: "tt", DefinitionID: "testtracker",
		Settings: map[string]string{"apikey": "k1", "query_limit": "2"},
	}); err != nil {
		t.Fatalf("Add: %v", err)
	}

	for i := range 2 {
		if err := reg.Test(ctx, "tt"); err != nil {
			t.Fatalf("Test %d refused while the budget still had room: %v", i, err)
		}
	}
	err := reg.Test(ctx, "tt")
	if !errors.Is(err, core.ErrBudgetExhausted) {
		t.Fatalf("Test past the budget = %v, want core.ErrBudgetExhausted", err)
	}
}

// TestTestIgnoresAnOpenCircuit is the deliberate asymmetry the probe queue depends on:
// Search refuses a breaker-disabled indexer outright, but Test goes ahead — re-checking
// whether a broken tracker recovered is exactly what a probe is for, and a passing Test
// is what descends the ladder again.
func TestTestIgnoresAnOpenCircuit(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	reg, db, _ := newProbedRegistry(t, &replayDoer{body: "<html><body></body></html>"})
	inst, err := reg.Add(ctx, registry.AddParams{
		Slug: "tt", DefinitionID: "testtracker", Settings: map[string]string{"apikey": "k1"},
	})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := (database.Circuit{}).Upsert(ctx, db, database.CircuitState{
		InstanceID:      inst.ID,
		EscalationLevel: 3,
		InitialFailure:  fixedClock().Add(-time.Hour),
		DisabledTill:    fixedClock().Add(time.Hour),
	}); err != nil {
		t.Fatalf("upsert circuit: %v", err)
	}

	idx, ok := reg.Indexer(ctx, "tt")
	if !ok {
		t.Fatal("Indexer(tt) not resolved")
	}
	if _, err := idx.Search(ctx, search.Query{Keywords: "x"}); !errors.Is(err, core.ErrCircuitOpen) {
		t.Fatalf("Search with an open circuit = %v, want core.ErrCircuitOpen", err)
	}
	if err := reg.Test(ctx, "tt"); err != nil {
		t.Fatalf("Test with an open circuit = %v, want it to proceed anyway", err)
	}
	// A passing probe clears the breaker, which is what makes the boot probe a
	// recovery mechanism rather than just a status refresh.
	state, err := (database.Circuit{}).Get(ctx, db, inst.ID)
	if err != nil {
		t.Fatalf("read circuit: %v", err)
	}
	if state.IsDisabled(fixedClock()) {
		t.Fatalf("circuit still open after a passing probe: %+v", state)
	}
}

// loginDefYAML is defYAML plus a form login, so Resolver.Test has a credential check to
// actually fail when the tracker refuses.
const loginDefYAML = `---
id: testtracker
name: Test Tracker
description: Registry probe fixture
language: en-US
type: private
encoding: UTF-8
links:
  - https://html.invalid/
caps:
  categorymappings:
    - {id: 1, cat: Movies}
  modes:
    search: [q]
settings:
  - name: username
    type: text
    label: Username
  - name: password
    type: password
    label: Password
login:
  path: takelogin.php
  method: post
  inputs:
    username: "{{ .Config.username }}"
    password: "{{ .Config.password }}"
  error:
    - selector: div.error
  test:
    path: browse.php
    selector: a[href="/logout.php"]
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

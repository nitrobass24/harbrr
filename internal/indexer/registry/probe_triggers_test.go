package registry_test

import (
	"context"
	"errors"
	stdhttp "net/http"
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

// fixtureBaseURL is the base URL addProbed creates the fixture indexer with, so a patch
// can resubmit it unchanged or move it.
const fixtureBaseURL = "https://html.invalid/"

// addProbed creates the shared fixture indexer and returns the recorder's state reset
// to empty, so a following Update assertion sees only its own enqueues.
func addProbed(t *testing.T, reg *registry.Registry, rec *probeRecorder, settings map[string]string) {
	t.Helper()
	if _, err := reg.Add(context.Background(), registry.AddParams{
		Slug: "tt", DefinitionID: "testtracker", BaseURL: fixtureBaseURL, Settings: settings,
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

// TestUpdateEnqueuesProbeOnlyOnLoginChange is the trigger's whole point: a login is
// spent when something the LOGIN depends on changes — a credential or the base URL —
// and never for a rename, a priority nudge, a sort-order flip, or a form resubmit that
// left the secret as the redacted sentinel.
func TestUpdateEnqueuesProbeOnlyOnLoginChange(t *testing.T) {
	t.Parallel()
	name := "Renamed"
	priority := 10
	sameURL := fixtureBaseURL
	movedURL := "https://mirror.invalid/"

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
			// A daemon knob the definition never declares: it changes how harbrr QUERIES
			// the indexer, not whether it can log in. (The fixture's own "sort" is
			// declared type: text, and every text input a definition collects counts as a
			// credential — that is where username lives.)
			name:  "non-credential setting changed",
			patch: registry.UpdateParams{Settings: map[string]string{"cache_ttl": "600"}},
		},
		{
			name:  "base URL resubmitted unchanged",
			patch: registry.UpdateParams{BaseURL: &sameURL},
		},
		{
			name:  "credential changed",
			patch: registry.UpdateParams{Settings: map[string]string{"apikey": "k2"}},
			want:  true,
		},
		{
			name:  "base URL changed",
			patch: registry.UpdateParams{BaseURL: &movedURL},
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

// TestProbeRecoversARule3OnlyFailure walks the trapdoor end to end. An indexer whose
// searches fail in a shape nothing classifies (a plain 500) derives failing on rule 3
// alone: no health event, no failing-since, no breaker window — nothing that expires.
// Feeds skip a failing indexer, so no traffic can ever produce the success that clears
// it; the probe is the only way out, and it only works if ProbeTargets actually selects
// it.
func TestProbeRecoversARule3OnlyFailure(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	doer := &swapDoer{body: bodyHTML}
	reg, _ := newRegistry(t, doer)
	if _, err := reg.Add(ctx, registry.AddParams{
		Slug: "tt", DefinitionID: "testtracker", Settings: map[string]string{"apikey": "x"},
	}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	idx, ok := reg.Indexer(ctx, "tt")
	if !ok {
		t.Fatal("Indexer(tt) not resolved")
	}

	doer.fail.Store(stdhttp.StatusInternalServerError)
	if _, err := idx.Search(ctx, search.Query{Keywords: "bunny"}); err == nil {
		t.Fatal("Search against a 500 succeeded; the fixture is not exercising the failure path")
	}
	st, err := reg.Status(ctx, "tt")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if st.Status != registry.StatusFailing || len(st.Events) != 0 || st.FailingSince != nil || st.DisabledTill != nil {
		t.Fatalf("status = %+v, want failing on rule 3 alone (no events, no failing-since, no disable window)", st)
	}

	targets, err := reg.ProbeTargets(ctx)
	if err != nil {
		t.Fatalf("ProbeTargets: %v", err)
	}
	if len(targets) != 1 || targets[0] != "tt" {
		t.Fatalf("probe targets = %v, want [tt] — a rule-3 failure is unreachable by any other traffic", targets)
	}

	// The tracker recovers, and the probe is what finds out.
	doer.fail.Store(0)
	if err := reg.Test(ctx, "tt"); err != nil {
		t.Fatalf("probe of a recovered tracker: %v", err)
	}
	if st, err := reg.Status(ctx, "tt"); err != nil || st.Status != registry.StatusHealthy {
		t.Fatalf("status after a passing probe = %q (err %v), want healthy", st.Status, err)
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

// TestTestIgnoresAnExhaustedBudget is the other half of Test's deliberate asymmetry: a
// diagnostic that can refuse you is worse than the one request it saves. Searching an
// indexer whose query budget is spent is refused (that is #251 working), but asking
// "does this passkey still work" — by hand or from the probe queue — always goes ahead.
func TestTestIgnoresAnExhaustedBudget(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	reg, _, _ := newProbedRegistry(t, &replayDoer{body: "<html><body></body></html>"})
	if _, err := reg.Add(ctx, registry.AddParams{
		Slug: "tt", DefinitionID: "testtracker",
		Settings: map[string]string{"apikey": "k1", "query_limit": "1"},
	}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	idx, ok := reg.Indexer(ctx, "tt")
	if !ok {
		t.Fatal("Indexer(tt) not resolved")
	}
	if _, err := idx.Search(ctx, search.Query{Keywords: "x"}); err != nil {
		t.Fatalf("first Search: %v", err)
	}
	if _, err := idx.Search(ctx, search.Query{Keywords: "y"}); !errors.Is(err, core.ErrBudgetExhausted) {
		t.Fatalf("Search past the budget = %v, want core.ErrBudgetExhausted (the fixture is not exhausting it)", err)
	}

	for i := range 2 {
		if err := reg.Test(ctx, "tt"); err != nil {
			t.Fatalf("Test %d refused on an exhausted budget: %v", i, err)
		}
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

package appsync

import (
	"context"
	"errors"
	"strconv"
	"testing"

	"github.com/autobrr/harbrr/internal/domain"
)

var errBoom = errors.New("boom")

// fakeTarget is a stateful in-memory app: Create/Update/Delete mutate its indexer
// list so List reflects reality, which lets the idempotency and recovery tests run
// the reconciler against a realistic moving target. Failures are injected per slug
// (create/update) or per remote id (delete).
type fakeTarget struct {
	remote     []RemoteIndexer
	nextID     int
	failCreate map[string]bool
	failUpdate map[string]bool
	failDelete map[string]bool
	creates    int
	updates    int
	deletes    int
}

func (f *fakeTarget) List(context.Context) ([]RemoteIndexer, error) {
	return append([]RemoteIndexer(nil), f.remote...), nil
}

func (f *fakeTarget) Create(_ context.Context, d DesiredIndexer) (string, error) {
	if f.failCreate[d.Slug] {
		return "", errBoom
	}
	f.nextID++
	id := strconv.Itoa(f.nextID)
	f.remote = append(f.remote, RemoteIndexer{RemoteID: id, Name: d.Name, FeedURL: d.FeedURL, ManagedBySlug: d.Slug})
	f.creates++
	return id, nil
}

func (f *fakeTarget) Update(_ context.Context, remoteID string, d DesiredIndexer) error {
	if f.failUpdate[d.Slug] {
		return errBoom
	}
	for i := range f.remote {
		if f.remote[i].RemoteID == remoteID {
			f.remote[i].Name = d.Name
		}
	}
	f.updates++
	return nil
}

func (f *fakeTarget) Delete(_ context.Context, remoteID string) error {
	if f.failDelete[remoteID] {
		return errBoom
	}
	kept := f.remote[:0]
	for _, r := range f.remote {
		if r.RemoteID != remoteID {
			kept = append(kept, r)
		}
	}
	f.remote = kept
	f.deletes++
	return nil
}

func (f *fakeTarget) Test(context.Context, DesiredIndexer) error { return nil }

func desired(slug string, enabled bool) DesiredIndexer {
	return DesiredIndexer{
		Slug: slug, Name: slug, Enabled: enabled, Priority: 25,
		FeedURL:    "http://harbrr/api/indexers/" + slug + "/results/torznab",
		APIKey:     "k",
		Categories: []Category{{ID: 5000, Name: "TV"}, {ID: 2000, Name: "Movies"}},
	}
}

func priorFrom(outs []IndexerOutcome) map[string]LedgerEntry {
	m := map[string]LedgerEntry{}
	for _, o := range outs {
		if o.Action == ActionCreated || o.Action == ActionUpdated || o.Action == ActionNoop {
			m[o.Slug] = LedgerEntry{RemoteID: o.RemoteID, PayloadHash: o.Hash}
		}
	}
	return m
}

func actionOf(outs []IndexerOutcome, slug string) string {
	for _, o := range outs {
		if o.Slug == slug {
			return o.Action
		}
	}
	return ""
}

func TestReconcileCreatesNew(t *testing.T) {
	t.Parallel()
	f := &fakeTarget{}
	outs, err := Reconcile(context.Background(), f, domain.SyncLevelFull, []DesiredIndexer{desired("a", true)}, nil)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if actionOf(outs, "a") != ActionCreated || f.creates != 1 {
		t.Fatalf("want one create, got outcomes=%+v creates=%d", outs, f.creates)
	}
	if outs[0].RemoteID == "" {
		t.Errorf("created outcome missing remote id")
	}
}

func TestReconcileNoopWhenUnchanged(t *testing.T) {
	t.Parallel()
	f := &fakeTarget{}
	in := []DesiredIndexer{desired("a", true), desired("b", false)}
	first, _ := Reconcile(context.Background(), f, domain.SyncLevelFull, in, nil)

	prior := priorFrom(first)
	f.creates, f.updates = 0, 0
	second, err := Reconcile(context.Background(), f, domain.SyncLevelFull, in, prior)
	if err != nil {
		t.Fatalf("second Reconcile: %v", err)
	}
	for _, o := range second {
		if o.Action != ActionNoop {
			t.Errorf("slug %q action = %q, want noop on unchanged re-sync", o.Slug, o.Action)
		}
	}
	if f.creates != 0 || f.updates != 0 {
		t.Errorf("re-sync touched the target: creates=%d updates=%d", f.creates, f.updates)
	}
}

func TestReconcileUpdatesOnChange(t *testing.T) {
	t.Parallel()
	f := &fakeTarget{}
	first, _ := Reconcile(context.Background(), f, domain.SyncLevelFull, []DesiredIndexer{desired("a", true)}, nil)
	prior := priorFrom(first)

	changed := desired("a", true)
	changed.Name = "Renamed"
	out, _ := Reconcile(context.Background(), f, domain.SyncLevelFull, []DesiredIndexer{changed}, prior)
	if actionOf(out, "a") != ActionUpdated || f.updates != 1 {
		t.Fatalf("want one update, got outcomes=%+v updates=%d", out, f.updates)
	}
}

func TestReconcileRecoversBySlugWhenLedgerMissing(t *testing.T) {
	t.Parallel()
	// Remote already has a harbrr-owned row, but harbrr's ledger is empty (lost
	// state / first sync after a restore). It must recover and update, not duplicate.
	f := &fakeTarget{remote: []RemoteIndexer{
		{RemoteID: "42", Name: "old", ManagedBySlug: "a", FeedURL: "http://harbrr/api/indexers/a/results/torznab"},
	}, nextID: 42}
	out, _ := Reconcile(context.Background(), f, domain.SyncLevelFull, []DesiredIndexer{desired("a", true)}, nil)
	if actionOf(out, "a") != ActionUpdated || f.creates != 0 {
		t.Fatalf("want recovery update, got outcomes=%+v creates=%d", out, f.creates)
	}
	if out[0].RemoteID != "42" {
		t.Errorf("recovered remote id = %q, want 42", out[0].RemoteID)
	}
}

func TestReconcileRecreatesWhenRemoteIDGone(t *testing.T) {
	t.Parallel()
	// Ledger points at id 99, but it was deleted in the app out of band and no
	// owned row matches the slug → reconcile must create a fresh one.
	f := &fakeTarget{}
	prior := map[string]LedgerEntry{"a": {RemoteID: "99", PayloadHash: "stale"}}
	out, _ := Reconcile(context.Background(), f, domain.SyncLevelFull, []DesiredIndexer{desired("a", true)}, prior)
	if actionOf(out, "a") != ActionCreated || f.creates != 1 {
		t.Fatalf("want recreate, got outcomes=%+v creates=%d", out, f.creates)
	}
}

func TestReconcilePrunesOrphansOnlyInFull(t *testing.T) {
	t.Parallel()
	seed := func() *fakeTarget {
		return &fakeTarget{remote: []RemoteIndexer{
			{RemoteID: "1", ManagedBySlug: "gone", FeedURL: "http://harbrr/api/indexers/gone/results/torznab"},
		}, nextID: 1}
	}

	full := seed()
	out, _ := Reconcile(context.Background(), full, domain.SyncLevelFull, nil, nil)
	if actionOf(out, "gone") != ActionDeleted || full.deletes != 1 {
		t.Errorf("full sync should prune orphan: outcomes=%+v deletes=%d", out, full.deletes)
	}

	add := seed()
	out2, _ := Reconcile(context.Background(), add, domain.SyncLevelAddUpdate, nil, nil)
	if len(out2) != 0 || add.deletes != 0 {
		t.Errorf("add_update must not prune: outcomes=%+v deletes=%d", out2, add.deletes)
	}
}

func TestReconcileNeverDeletesUnmanaged(t *testing.T) {
	t.Parallel()
	f := &fakeTarget{remote: []RemoteIndexer{
		{RemoteID: "1", Name: "hand-added", ManagedBySlug: ""}, // not harbrr's
	}, nextID: 1}
	out, _ := Reconcile(context.Background(), f, domain.SyncLevelFull, nil, nil)
	if len(out) != 0 || f.deletes != 0 {
		t.Errorf("unmanaged remote indexer must never be pruned: outcomes=%+v deletes=%d", out, f.deletes)
	}
}

func TestReconcilePartialFailureIsolated(t *testing.T) {
	t.Parallel()
	f := &fakeTarget{failCreate: map[string]bool{"bad": true}}
	in := []DesiredIndexer{desired("good", true), desired("bad", true)}
	out, err := Reconcile(context.Background(), f, domain.SyncLevelFull, in, nil)
	if err != nil {
		t.Fatalf("Reconcile returned fatal error on a per-indexer failure: %v", err)
	}
	if actionOf(out, "good") != ActionCreated {
		t.Errorf("healthy indexer should still apply: %+v", out)
	}
	if actionOf(out, "bad") != ActionFailed {
		t.Errorf("failed indexer should be recorded failed: %+v", out)
	}
	if Status(out) != domain.SyncStatusPartial {
		t.Errorf("Status = %q, want partial", Status(out))
	}
}

func TestReconcileListErrorIsFatal(t *testing.T) {
	t.Parallel()
	_, err := Reconcile(context.Background(), listErrTarget{}, domain.SyncLevelFull, []DesiredIndexer{desired("a", true)}, nil)
	if err == nil {
		t.Fatal("a failed List must be fatal (nothing can be reconciled)")
	}
}

func TestStatus(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   []IndexerOutcome
		want string
	}{
		{"empty", nil, domain.SyncStatusOK},
		{"all ok", []IndexerOutcome{{Action: ActionCreated}, {Action: ActionNoop}}, domain.SyncStatusOK},
		{"mixed", []IndexerOutcome{{Action: ActionCreated}, {Action: ActionFailed}}, domain.SyncStatusPartial},
		{"all failed", []IndexerOutcome{{Action: ActionFailed}}, domain.SyncStatusError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := Status(tc.in); got != tc.want {
				t.Errorf("Status(%s) = %q, want %q", tc.name, got, tc.want)
			}
		})
	}
}

// listErrTarget fails only at List, to prove a failed listing is fatal.
type listErrTarget struct{}

func (listErrTarget) List(context.Context) ([]RemoteIndexer, error)          { return nil, errBoom }
func (listErrTarget) Create(context.Context, DesiredIndexer) (string, error) { return "", nil }
func (listErrTarget) Update(context.Context, string, DesiredIndexer) error   { return nil }
func (listErrTarget) Delete(context.Context, string) error                   { return nil }
func (listErrTarget) Test(context.Context, DesiredIndexer) error             { return nil }

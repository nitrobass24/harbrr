package registry_test

import (
	"context"
	stdhttp "net/http"
	"strings"
	"testing"
	"time"

	"github.com/autobrr/harbrr/internal/database"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/indexer/registry"
)

// TestCircuitOpensAfterFailureAndGatesDispatch proves the #253 dispatch gate: a
// classified failure escalates the instance's circuit, and the NEXT Search is
// skipped entirely (the doer is never called again) rather than hitting the
// tracker, returning a clear "circuit open" error instead.
func TestCircuitOpensAfterFailureAndGatesDispatch(t *testing.T) {
	t.Parallel()
	doer := &countingDoer{status: stdhttp.StatusServiceUnavailable}
	reg, db := newRegistry(t, doer)
	ctx := context.Background()
	if _, err := reg.Add(ctx, registry.AddParams{
		Slug: "tt", DefinitionID: "testtracker", Settings: map[string]string{"apikey": "x"},
	}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	idx, ok := reg.Indexer(ctx, "tt")
	if !ok {
		t.Fatal("Indexer(tt) not resolved")
	}

	// First search reaches the tracker, fails (503 -> rate_limited), and escalates
	// the circuit to level 1 (a 60s window, per the ladder).
	if _, err := idx.Search(ctx, search.Query{Keywords: "bunny"}); err == nil {
		t.Fatal("first Search returned nil error, want a classified failure")
	}
	if got := doer.count(); got != 1 {
		t.Fatalf("doer called %d times after first search, want 1", got)
	}

	inst, err := database.Instances{}.GetBySlug(ctx, db, "tt")
	if err != nil {
		t.Fatalf("get instance: %v", err)
	}
	circuit, err := database.Circuit{}.Get(ctx, db, inst.ID)
	if err != nil {
		t.Fatalf("get circuit: %v", err)
	}
	if circuit.EscalationLevel != 1 {
		t.Fatalf("EscalationLevel = %d, want 1", circuit.EscalationLevel)
	}
	if !circuit.IsDisabled(fixedClock()) {
		t.Fatal("circuit must be open (disabled) right after the escalating failure")
	}

	// Second search must be gated: no new request reaches the doer.
	_, err = idx.Search(ctx, search.Query{Keywords: "bunny"})
	if err == nil {
		t.Fatal("second Search returned nil error, want circuit-open")
	}
	if !strings.Contains(err.Error(), "circuit open") {
		t.Errorf("second Search err = %v, want it to mention circuit open", err)
	}
	if got := doer.count(); got != 1 {
		t.Errorf("doer called %d times after second (gated) search, want still 1", got)
	}
}

// TestCircuitClosesAfterSuccess proves a success descends the ladder one rung and
// clears the disable window, matching Prowlarr's RecordSuccess (not a full reset).
func TestCircuitClosesAfterSuccess(t *testing.T) {
	t.Parallel()
	reg, db := newRegistry(t, &replayDoer{body: bodyHTML})
	ctx := context.Background()
	if _, err := reg.Add(ctx, registry.AddParams{
		Slug: "tt", DefinitionID: "testtracker", Settings: map[string]string{"apikey": "x"},
	}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	inst, err := database.Instances{}.GetBySlug(ctx, db, "tt")
	if err != nil {
		t.Fatalf("get instance: %v", err)
	}
	// Seed an already-escalated (but expired) circuit directly, as if two prior
	// failures had climbed to level 2.
	if err := (database.Circuit{}).Upsert(ctx, db, database.CircuitState{
		InstanceID: inst.ID, EscalationLevel: 2,
		InitialFailure: fixedClock().Add(-time.Hour), DisabledTill: fixedClock().Add(-time.Minute),
	}); err != nil {
		t.Fatalf("seed circuit: %v", err)
	}

	idx, ok := reg.Indexer(ctx, "tt")
	if !ok {
		t.Fatal("Indexer(tt) not resolved")
	}
	if _, err := idx.Search(ctx, search.Query{Keywords: "bunny"}); err != nil {
		t.Fatalf("Search: %v", err)
	}

	got, err := database.Circuit{}.Get(ctx, db, inst.ID)
	if err != nil {
		t.Fatalf("get circuit: %v", err)
	}
	if got.EscalationLevel != 1 {
		t.Errorf("EscalationLevel = %d, want 1 (descended one rung, not reset)", got.EscalationLevel)
	}
	if !got.DisabledTill.IsZero() {
		t.Error("DisabledTill must be cleared after a success")
	}
}

// TestCircuitGatesGrab proves the dispatch gate covers Grab too, not just Search:
// with the circuit already open, idx.Grab is skipped before the doer is touched
// (checkCircuit fires ahead of any link resolution), returning circuit-open.
func TestCircuitGatesGrab(t *testing.T) {
	t.Parallel()
	doer := &countingDoer{status: stdhttp.StatusOK}
	reg, db := newRegistry(t, doer)
	ctx := context.Background()
	if _, err := reg.Add(ctx, registry.AddParams{
		Slug: "tt", DefinitionID: "testtracker", Settings: map[string]string{"apikey": "x"},
	}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	inst, err := database.Instances{}.GetBySlug(ctx, db, "tt")
	if err != nil {
		t.Fatalf("get instance: %v", err)
	}
	// Seed an OPEN circuit (disabled well into the future).
	if err := (database.Circuit{}).Upsert(ctx, db, database.CircuitState{
		InstanceID: inst.ID, EscalationLevel: 3,
		InitialFailure: fixedClock().Add(-time.Hour), DisabledTill: fixedClock().Add(time.Hour),
	}); err != nil {
		t.Fatalf("seed circuit: %v", err)
	}
	idx, ok := reg.Indexer(ctx, "tt")
	if !ok {
		t.Fatal("Indexer(tt) not resolved")
	}
	_, err = idx.Grab(ctx, "https://tracker.example/dl?id=1")
	if err == nil {
		t.Fatal("Grab returned nil error, want circuit-open")
	}
	if !strings.Contains(err.Error(), "circuit open") {
		t.Errorf("Grab err = %v, want it to mention circuit open", err)
	}
	if got := doer.count(); got != 0 {
		t.Errorf("doer called %d times for a gated Grab, want 0", got)
	}
}

// countingDoer is statusDoer plus a request counter, so the dispatch-gate test can
// prove the second, gated Search never reaches it.
type countingDoer struct {
	status int
	n      int
}

func (d *countingDoer) Do(req *stdhttp.Request) (*stdhttp.Response, error) {
	d.n++
	return statusDoer{status: d.status}.Do(req)
}

func (d *countingDoer) count() int { return d.n }

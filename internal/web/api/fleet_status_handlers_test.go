package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/autobrr/harbrr/internal/database"
	"github.com/autobrr/harbrr/internal/domain"
	"github.com/autobrr/harbrr/internal/indexer/registry"
)

// fleetStatusBody mirrors the fleetStatusResponse JSON shape for assertions.
type fleetStatusBody struct {
	Healthy  int `json:"healthy"`
	Failing  int `json:"failing"`
	Unknown  int `json:"unknown"`
	Indexers []struct {
		Slug      string `json:"slug"`
		Status    string `json:"status"`
		LastEvent *struct {
			Kind       string    `json:"kind"`
			Detail     string    `json:"detail"`
			OccurredAt time.Time `json:"occurred_at"`
		} `json:"lastEvent"`
		FailingSince *time.Time `json:"failingSince"`
	} `json:"indexers"`
}

// TestAllIndexerStatusEmptyFleet: no configured indexers returns zeroed counts and an
// empty (not null) indexers array.
func TestAllIndexerStatusEmptyFleet(t *testing.T) {
	t.Parallel()
	base, c := serve(t, authDisabledEnv(t))

	resp, body := do(t, c, http.MethodGet, base+"/api/indexers/status", nil, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", resp.StatusCode, body)
	}
	var out fleetStatusBody
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("unmarshal: %v (body %s)", err, body)
	}
	if out.Healthy != 0 || out.Failing != 0 || out.Unknown != 0 {
		t.Errorf("counts = healthy=%d failing=%d unknown=%d, want 0/0/0", out.Healthy, out.Failing, out.Unknown)
	}
	if out.Indexers == nil || len(out.Indexers) != 0 {
		t.Errorf("indexers = %v, want empty array", out.Indexers)
	}
}

// fleetSlugs is the seeded fleet, in the sorted order the roll-up returns.
var fleetSlugs = []string{"failing-old", "failing-recent", "healthy-tested", "unknown-idle"}

// failingSinceSeed is the streak start seeded onto failing-recent's circuit. Fixed
// (not now-relative) so the endpoint's failingSince can be asserted by exact equality
// rather than a tolerance — it is only ever reported back, never compared to a clock.
var failingSinceSeed = time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)

// seedHealthFleet configures four indexers covering all three derived states (#389,
// remodelled by #484): one with a recent failure (failing), one whose failure is two
// hours old and which nothing has succeeded past since — still failing, because health is
// sticky and nothing expires — one that just passed a test (healthy), and one never heard
// from at all (unknown, meaning never tested).
func seedHealthFleet(t *testing.T, e *env) {
	t.Helper()
	ctx := context.Background()
	instanceIDs := map[string]int64{}
	for _, slug := range fleetSlugs {
		inst, err := e.registry.Add(ctx, registry.AddParams{
			Slug: slug, DefinitionID: "testtracker", Settings: map[string]string{"apikey": "x"},
		})
		if err != nil {
			t.Fatalf("Add %q: %v", slug, err)
		}
		instanceIDs[slug] = inst.ID
	}

	var health database.Health
	// failing-recent gets two events; lastEvent must pick the newer auth_failure,
	// not this older parse_error.
	if err := health.Record(ctx, e.db, domain.IndexerHealthEvent{
		InstanceID: instanceIDs["failing-recent"], Kind: "parse_error", Detail: "bad page",
		OccurredAt: time.Now().Add(-30 * time.Minute),
	}); err != nil {
		t.Fatalf("record older failure: %v", err)
	}
	if err := health.Record(ctx, e.db, domain.IndexerHealthEvent{
		InstanceID: instanceIDs["failing-recent"], Kind: "auth_failure", Detail: "login failed",
		OccurredAt: time.Now().Add(-1 * time.Minute),
	}); err != nil {
		t.Fatalf("record recent failure: %v", err)
	}
	// failing-recent also carries a circuit whose InitialFailure is the streak start the
	// endpoint reports as failingSince. DisabledTill is deliberately left zero: the row
	// already reads failing from its recent event, so this seeds the one new field
	// without opening the breaker and changing anything else in the response.
	if err := (database.Circuit{}).Upsert(ctx, e.db, database.CircuitState{
		InstanceID: instanceIDs["failing-recent"], EscalationLevel: 1,
		InitialFailure: failingSinceSeed,
	}); err != nil {
		t.Fatalf("upsert circuit: %v", err)
	}
	if err := health.Record(ctx, e.db, domain.IndexerHealthEvent{
		InstanceID: instanceIDs["failing-old"], Kind: "rate_limited", Detail: "429",
		OccurredAt: time.Now().Add(-2 * time.Hour),
	}); err != nil {
		t.Fatalf("record old failure: %v", err)
	}
	// A passing explicit test is the recovery watermark, and it is a success — so this
	// indexer reads healthy without any search traffic.
	if err := health.RecordRecovery(ctx, e.db, instanceIDs["healthy-tested"], time.Now()); err != nil {
		t.Fatalf("record recovery: %v", err)
	}
}

// TestAllIndexerStatus covers all three derived states (#389) in one roll-up, sorted
// by slug.
func TestAllIndexerStatus(t *testing.T) {
	t.Parallel()
	e := authDisabledEnv(t)
	slugs := fleetSlugs
	seedHealthFleet(t, e)

	base, c := serve(t, e)
	resp, body := do(t, c, http.MethodGet, base+"/api/indexers/status", nil, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", resp.StatusCode, body)
	}
	var out fleetStatusBody
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("unmarshal: %v (body %s)", err, body)
	}

	if out.Healthy != 1 || out.Failing != 2 || out.Unknown != 1 {
		t.Errorf("counts = healthy=%d failing=%d unknown=%d, want 1/2/1", out.Healthy, out.Failing, out.Unknown)
	}
	if len(out.Indexers) != len(slugs) {
		t.Fatalf("indexers rows = %d, want %d", len(out.Indexers), len(slugs))
	}
	for i, want := range slugs { // slugs is already in sorted order
		if out.Indexers[i].Slug != want {
			t.Errorf("indexers[%d].slug = %q, want %q", i, out.Indexers[i].Slug, want)
		}
	}

	byStatus := map[string]string{}
	lastEventKind := map[string]string{}
	for _, ind := range out.Indexers {
		byStatus[ind.Slug] = ind.Status
		if ind.LastEvent != nil {
			lastEventKind[ind.Slug] = ind.LastEvent.Kind
		}
	}
	wantStatus := map[string]string{
		"failing-recent": "failing",
		"failing-old":    "failing",
		"healthy-tested": "healthy",
		"unknown-idle":   "unknown",
	}
	for slug, want := range wantStatus {
		if byStatus[slug] != want {
			t.Errorf("%s status = %q, want %q", slug, byStatus[slug], want)
		}
	}
	if _, has := lastEventKind["unknown-idle"]; has {
		t.Errorf("unknown-idle lastEvent = %v, want omitted (no events)", lastEventKind["unknown-idle"])
	}
	if lastEventKind["failing-old"] != "rate_limited" {
		t.Errorf("failing-old lastEvent.kind = %q, want rate_limited", lastEventKind["failing-old"])
	}
	if lastEventKind["failing-recent"] != "auth_failure" {
		t.Errorf("failing-recent lastEvent.kind = %q, want auth_failure (the newest of its two events)", lastEventKind["failing-recent"])
	}

	// failingSince must survive the handler's mapping onto the wire, and must stay
	// absent on every row that is not failing — dropping the field from
	// toFleetIndexerStatus, or setting it unconditionally, has to fail here.
	failingSince := map[string]*time.Time{}
	for _, ind := range out.Indexers {
		failingSince[ind.Slug] = ind.FailingSince
	}
	if got := failingSince["failing-recent"]; got == nil || !got.Equal(failingSinceSeed) {
		t.Errorf("failing-recent failingSince = %v, want the seeded streak start %v", got, failingSinceSeed)
	}
	for _, slug := range []string{"healthy-tested", "unknown-idle"} {
		if got := failingSince[slug]; got != nil {
			t.Errorf("%s failingSince = %v, want omitted (only a failing row has a streak)", slug, got)
		}
	}
}

// TestAllIndexerStatusFilter: ?status= narrows the indexers array to exactly the
// derived set (#389 PR 3) while the counts stay fleet-wide, and an unrecognized value
// is a 400 rather than a silently empty list.
func TestAllIndexerStatusFilter(t *testing.T) {
	t.Parallel()
	e := authDisabledEnv(t)
	seedHealthFleet(t, e)
	base, c := serve(t, e)

	tests := []struct {
		name  string
		query string
		want  []string
	}{
		{name: "healthy", query: "?status=healthy", want: []string{"healthy-tested"}},
		{name: "failing", query: "?status=failing", want: []string{"failing-old", "failing-recent"}},
		{name: "unknown", query: "?status=unknown", want: []string{"unknown-idle"}},
		{name: "empty value is no filter", query: "?status=", want: fleetSlugs},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			resp, body := do(t, c, http.MethodGet, base+"/api/indexers/status"+tt.query, nil, nil)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status = %d, want 200 (body %s)", resp.StatusCode, body)
			}
			var out fleetStatusBody
			if err := json.Unmarshal(body, &out); err != nil {
				t.Fatalf("unmarshal: %v (body %s)", err, body)
			}
			if out.Healthy != 1 || out.Failing != 2 || out.Unknown != 1 {
				t.Errorf("counts = healthy=%d failing=%d unknown=%d, want the fleet-wide 1/2/1",
					out.Healthy, out.Failing, out.Unknown)
			}
			if len(out.Indexers) != len(tt.want) {
				t.Fatalf("indexers = %+v, want %v", out.Indexers, tt.want)
			}
			for i, want := range tt.want {
				if out.Indexers[i].Slug != want {
					t.Errorf("indexers[%d].slug = %q, want %q", i, out.Indexers[i].Slug, want)
				}
			}
		})
	}

	t.Run("unrecognized value is a 400", func(t *testing.T) {
		t.Parallel()
		resp, body := do(t, c, http.MethodGet, base+"/api/indexers/status?status=degraded", nil, nil)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400 (body %s)", resp.StatusCode, body)
		}
	})
}

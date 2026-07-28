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

// TestAllIndexerStatus covers all three derived states (#389) in one roll-up: an
// indexer that just passed a test (healthy), one with a recent failure (failing), one
// that has never been heard from (unknown), and one whose failure has aged out of the
// window (unknown, but lastEvent still reports the old failure). Sorted by slug.
func TestAllIndexerStatus(t *testing.T) {
	t.Parallel()
	e := authDisabledEnv(t)
	ctx := context.Background()

	slugs := []string{"failing-recent", "healthy-tested", "unknown-idle", "unknown-stale"}
	instanceIDs := map[string]int64{}
	for _, slug := range slugs {
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
	if err := health.Record(ctx, e.db, domain.IndexerHealthEvent{
		InstanceID: instanceIDs["unknown-stale"], Kind: "rate_limited", Detail: "429",
		OccurredAt: time.Now().Add(-2 * time.Hour),
	}); err != nil {
		t.Fatalf("record old failure: %v", err)
	}
	// A passing explicit test is the recovery watermark, and it is a success — so this
	// indexer reads healthy without any search traffic.
	if err := health.RecordRecovery(ctx, e.db, instanceIDs["healthy-tested"], time.Now()); err != nil {
		t.Fatalf("record recovery: %v", err)
	}

	base, c := serve(t, e)
	resp, body := do(t, c, http.MethodGet, base+"/api/indexers/status", nil, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", resp.StatusCode, body)
	}
	var out fleetStatusBody
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("unmarshal: %v (body %s)", err, body)
	}

	if out.Healthy != 1 || out.Failing != 1 || out.Unknown != 2 {
		t.Errorf("counts = healthy=%d failing=%d unknown=%d, want 1/1/2", out.Healthy, out.Failing, out.Unknown)
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
		"healthy-tested": "healthy",
		"unknown-idle":   "unknown",
		"unknown-stale":  "unknown",
	}
	for slug, want := range wantStatus {
		if byStatus[slug] != want {
			t.Errorf("%s status = %q, want %q", slug, byStatus[slug], want)
		}
	}
	if _, has := lastEventKind["unknown-idle"]; has {
		t.Errorf("unknown-idle lastEvent = %v, want omitted (no events)", lastEventKind["unknown-idle"])
	}
	if lastEventKind["unknown-stale"] != "rate_limited" {
		t.Errorf("unknown-stale lastEvent.kind = %q, want rate_limited (expired, still reported)", lastEventKind["unknown-stale"])
	}
	if lastEventKind["failing-recent"] != "auth_failure" {
		t.Errorf("failing-recent lastEvent.kind = %q, want auth_failure (the newest of its two events)", lastEventKind["failing-recent"])
	}
}

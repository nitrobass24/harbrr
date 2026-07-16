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
	Healthy   int `json:"healthy"`
	Unhealthy int `json:"unhealthy"`
	Indexers  []struct {
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
	if out.Healthy != 0 || out.Unhealthy != 0 {
		t.Errorf("counts = healthy=%d unhealthy=%d, want 0/0", out.Healthy, out.Unhealthy)
	}
	if out.Indexers == nil || len(out.Indexers) != 0 {
		t.Errorf("indexers = %v, want empty array", out.Indexers)
	}
}

// TestAllIndexerStatus: a healthy never-queried indexer, an unhealthy one with a recent
// failure, and one whose failure is outside the recency window (status healthy, but
// lastEvent still reports the old failure) roll up into the fleet counts and per-indexer
// entries, sorted by slug.
func TestAllIndexerStatus(t *testing.T) {
	t.Parallel()
	e := authDisabledEnv(t)
	ctx := context.Background()

	slugs := []string{"healthy-none", "healthy-old", "unhealthy-recent"}
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
	// unhealthy-recent gets two events; lastEvent must pick the newer auth_failure,
	// not this older parse_error.
	if err := health.Record(ctx, e.db, domain.IndexerHealthEvent{
		InstanceID: instanceIDs["unhealthy-recent"], Kind: "parse_error", Detail: "bad page",
		OccurredAt: time.Now().Add(-30 * time.Minute),
	}); err != nil {
		t.Fatalf("record older failure: %v", err)
	}
	if err := health.Record(ctx, e.db, domain.IndexerHealthEvent{
		InstanceID: instanceIDs["unhealthy-recent"], Kind: "auth_failure", Detail: "login failed",
		OccurredAt: time.Now().Add(-1 * time.Minute),
	}); err != nil {
		t.Fatalf("record recent failure: %v", err)
	}
	if err := health.Record(ctx, e.db, domain.IndexerHealthEvent{
		InstanceID: instanceIDs["healthy-old"], Kind: "rate_limited", Detail: "429",
		OccurredAt: time.Now().Add(-2 * time.Hour),
	}); err != nil {
		t.Fatalf("record old failure: %v", err)
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

	if out.Healthy != 2 || out.Unhealthy != 1 {
		t.Errorf("counts = healthy=%d unhealthy=%d, want 2/1", out.Healthy, out.Unhealthy)
	}
	if len(out.Indexers) != 3 {
		t.Fatalf("indexers rows = %d, want 3", len(out.Indexers))
	}
	// Sorted by slug: healthy-none, healthy-old, unhealthy-recent.
	wantOrder := []string{"healthy-none", "healthy-old", "unhealthy-recent"}
	for i, want := range wantOrder {
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
	if byStatus["healthy-none"] != "healthy" {
		t.Errorf("healthy-none status = %q, want healthy", byStatus["healthy-none"])
	}
	if _, has := lastEventKind["healthy-none"]; has {
		t.Errorf("healthy-none lastEvent = %v, want omitted (no events)", lastEventKind["healthy-none"])
	}
	if byStatus["healthy-old"] != "healthy" {
		t.Errorf("healthy-old status = %q, want healthy", byStatus["healthy-old"])
	}
	if lastEventKind["healthy-old"] != "rate_limited" {
		t.Errorf("healthy-old lastEvent.kind = %q, want rate_limited", lastEventKind["healthy-old"])
	}
	if byStatus["unhealthy-recent"] != "unhealthy" {
		t.Errorf("unhealthy-recent status = %q, want unhealthy", byStatus["unhealthy-recent"])
	}
	if lastEventKind["unhealthy-recent"] != "auth_failure" {
		t.Errorf("unhealthy-recent lastEvent.kind = %q, want auth_failure (the newest of its two events)", lastEventKind["unhealthy-recent"])
	}
}

package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/autobrr/harbrr/internal/database"
	"github.com/autobrr/harbrr/internal/indexer/registry"
	"github.com/autobrr/harbrr/internal/web/api"
)

// statsBody mirrors the indexerStatsResponse JSON shape for assertions.
type statsBody struct {
	Slug            string   `json:"slug"`
	Queries         int64    `json:"queries"`
	GrabAttempts    int64    `json:"grabAttempts"`
	Grabs           int64    `json:"grabs"`
	GrabSuccessRate *float64 `json:"grabSuccessRate"`
	AvgResponseMs   int64    `json:"avgResponseMs"`
	Categories      []struct {
		ID      int    `json:"id"`
		Name    string `json:"name"`
		Results int64  `json:"results"`
		Grabs   int64  `json:"grabs"`
	} `json:"categories"`
	Failures struct {
		AuthFailure int64 `json:"authFailure"`
		RateLimited int64 `json:"rateLimited"`
		ParseError  int64 `json:"parseError"`
		AntiBot     int64 `json:"antiBot"`
	} `json:"failures"`
	LastQueryAt   *string `json:"lastQueryAt"`
	LastFailureAt *string `json:"lastFailureAt"`
}

// authDisabledEnv builds an env whose auth is disabled for the loopback allowlist, so
// no session/API-key setup is needed.
func authDisabledEnv(t *testing.T) *env {
	return newEnv(t, api.Config{
		AuthDisabled: true,
		IPAllowlist:  []string{"127.0.0.0/8", "::1/128"},
	})
}

// TestIndexerStatsNeverQueried: a configured-but-never-queried indexer reports zeroed
// counters and a null lastQueryAt (absent), not the zero time.
func TestIndexerStatsNeverQueried(t *testing.T) {
	t.Parallel()
	e := authDisabledEnv(t)
	if _, err := e.registry.Add(context.Background(), registry.AddParams{
		Slug: "tt", DefinitionID: "testtracker", Settings: map[string]string{"apikey": "x"},
	}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	base, c := serve(t, e)

	resp, body := do(t, c, http.MethodGet, base+"/api/indexers/tt/stats", nil, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", resp.StatusCode, body)
	}
	var st statsBody
	if err := json.Unmarshal(body, &st); err != nil {
		t.Fatalf("unmarshal: %v (body %s)", err, body)
	}
	if st.Slug != "tt" || st.Queries != 0 || st.Grabs != 0 || st.AvgResponseMs != 0 {
		t.Errorf("stats = %+v, want tt with zeroed counters", st)
	}
	if st.LastQueryAt != nil {
		t.Errorf("lastQueryAt = %v, want null (never queried)", *st.LastQueryAt)
	}
	if st.LastFailureAt != nil {
		t.Errorf("lastFailureAt = %v, want null (no failures)", *st.LastFailureAt)
	}
	if st.GrabSuccessRate != nil {
		t.Errorf("grabSuccessRate = %v, want absent until something is grabbed", *st.GrabSuccessRate)
	}
	if len(st.Categories) != 0 {
		t.Errorf("categories = %+v, want empty", st.Categories)
	}
}

// TestIndexerStatsCategoriesAndGrabRate proves the #403 additions reach the wire: the
// per-category tallies flushed by the registry come back grouped and named, and the
// grab success rate is derived from attempts.
func TestIndexerStatsCategoriesAndGrabRate(t *testing.T) {
	t.Parallel()
	e := authDisabledEnv(t)
	ctx := context.Background()
	inst, err := e.registry.Add(ctx, registry.AddParams{
		Slug: "tt", DefinitionID: "testtracker", Settings: map[string]string{"apikey": "x"},
	})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	// Seed the durable rows the way a previous run's flush would have left them, then
	// let the registry rehydrate — the boot path, so no test-only hook is needed.
	now := time.Now()
	if err := (database.IndexerStatCountersStore{}).Upsert(ctx, e.db, database.IndexerStatCounter{
		InstanceID: inst.ID, Queries: 3, GrabAttempts: 5, Grabs: 4, ResponseMsTotal: 30, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("seed counters: %v", err)
	}
	if _, err := (database.IndexerCategoryStatsStore{}).AddDeltas(ctx, e.db, database.MonthBucket(now),
		[]database.IndexerCategoryCount{
			{InstanceID: inst.ID, CategoryID: 2000, QueryResults: 9, Grabs: 4},
			{InstanceID: inst.ID, CategoryID: 7000, QueryResults: 1},
		}, now); err != nil {
		t.Fatalf("seed categories: %v", err)
	}
	if err := e.registry.RehydrateStats(ctx); err != nil {
		t.Fatalf("RehydrateStats: %v", err)
	}
	base, c := serve(t, e)

	resp, body := do(t, c, http.MethodGet, base+"/api/indexers/tt/stats", nil, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", resp.StatusCode, body)
	}
	var st statsBody
	if err := json.Unmarshal(body, &st); err != nil {
		t.Fatalf("unmarshal: %v (body %s)", err, body)
	}
	if st.GrabAttempts != 5 || st.Grabs != 4 {
		t.Errorf("attempts/grabs = %d/%d, want 5/4", st.GrabAttempts, st.Grabs)
	}
	if st.GrabSuccessRate == nil || *st.GrabSuccessRate != 0.8 {
		t.Errorf("grabSuccessRate = %v, want 0.8", st.GrabSuccessRate)
	}
	if len(st.Categories) != 2 {
		t.Fatalf("categories = %+v, want two families", st.Categories)
	}
	if c0 := st.Categories[0]; c0.ID != 2000 || c0.Name != "Movies" || c0.Results != 9 || c0.Grabs != 4 {
		t.Errorf("categories[0] = %+v, want Movies 9 results / 4 grabs", c0)
	}
	if c1 := st.Categories[1]; c1.ID != 7000 || c1.Name != "Books" || c1.Results != 1 {
		t.Errorf("categories[1] = %+v, want Books 1 result", c1)
	}
}

// TestAllIndexerStats: the all-indexers endpoint returns a row per configured indexer.
func TestAllIndexerStats(t *testing.T) {
	t.Parallel()
	e := authDisabledEnv(t)
	for _, slug := range []string{"a", "b"} {
		if _, err := e.registry.Add(context.Background(), registry.AddParams{
			Slug: slug, DefinitionID: "testtracker", Settings: map[string]string{"apikey": "x"},
		}); err != nil {
			t.Fatalf("Add %q: %v", slug, err)
		}
	}
	base, c := serve(t, e)

	resp, body := do(t, c, http.MethodGet, base+"/api/indexers/stats", nil, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", resp.StatusCode, body)
	}
	var out []statsBody
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("unmarshal: %v (body %s)", err, body)
	}
	if len(out) != 2 {
		t.Fatalf("stats rows = %d, want 2", len(out))
	}
	// The static "stats" segment must not be swallowed by the {slug} route.
	slugs := map[string]bool{out[0].Slug: true, out[1].Slug: true}
	if !slugs["a"] || !slugs["b"] {
		t.Errorf("slugs = %v, want a and b", slugs)
	}
}

// TestIndexerStatsNotFound: stats for an unknown slug is a 404.
func TestIndexerStatsNotFound(t *testing.T) {
	t.Parallel()
	base, c := serve(t, authDisabledEnv(t))
	resp, _ := do(t, c, http.MethodGet, base+"/api/indexers/does-not-exist/stats", nil, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

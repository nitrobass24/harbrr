package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/autobrr/harbrr/internal/indexer/registry"
)

// testAllBody mirrors the batch-test response for assertions.
type testAllBody struct {
	Results []struct {
		Slug  string `json:"slug"`
		OK    bool   `json:"ok"`
		Error string `json:"error,omitempty"`
	} `json:"results"`
}

// seedTestAllFleet configures three indexers on the shared testtracker fixture.
func seedTestAllFleet(t *testing.T, e *env, slugs ...string) {
	t.Helper()
	for _, slug := range slugs {
		if _, err := e.registry.Add(context.Background(), registry.AddParams{
			Slug: slug, DefinitionID: "testtracker", Settings: map[string]string{"apikey": "x"},
		}); err != nil {
			t.Fatalf("Add %q: %v", slug, err)
		}
	}
}

// TestTestAllIndexersReportsEveryRequestedSlug drives the batch endpoint end-to-end
// through the router and the real probe queue: one request in, one verdict per indexer
// out, in the requested order (autobrr/harbrr#485).
func TestTestAllIndexersReportsEveryRequestedSlug(t *testing.T) {
	t.Parallel()
	e := authDisabledEnv(t)
	seedTestAllFleet(t, e, "alpha", "beta", "gamma")
	base, c := serve(t, e)

	want := []string{"gamma", "alpha", "beta"}
	resp, body := do(t, c, http.MethodPost, base+"/api/indexers/test", map[string][]string{"slugs": want}, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", resp.StatusCode, body)
	}
	var out testAllBody
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("unmarshal: %v (body %s)", err, body)
	}
	if len(out.Results) != len(want) {
		t.Fatalf("results = %d, want %d (body %s)", len(out.Results), len(want), body)
	}
	for i, slug := range want {
		if out.Results[i].Slug != slug {
			t.Errorf("results[%d].slug = %q, want %q", i, out.Results[i].Slug, slug)
		}
	}
}

// TestTestAllIndexersDefaultsToEveryIndexer proves an omitted slugs list means the
// whole fleet, so a bare POST is the "Test all" the UI button fires.
func TestTestAllIndexersDefaultsToEveryIndexer(t *testing.T) {
	t.Parallel()
	e := authDisabledEnv(t)
	seedTestAllFleet(t, e, "alpha", "beta")
	base, c := serve(t, e)

	resp, body := do(t, c, http.MethodPost, base+"/api/indexers/test", nil, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", resp.StatusCode, body)
	}
	var out testAllBody
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("unmarshal: %v (body %s)", err, body)
	}
	got := make(map[string]bool, len(out.Results))
	for _, r := range out.Results {
		got[r.Slug] = true
	}
	if len(got) != 2 || !got["alpha"] || !got["beta"] {
		t.Fatalf("results covered %v, want alpha+beta (body %s)", got, body)
	}
}

// TestTestAllIndexersEmptyFleet: nothing configured is an empty result set, not an error.
func TestTestAllIndexersEmptyFleet(t *testing.T) {
	t.Parallel()
	base, c := serve(t, authDisabledEnv(t))

	resp, body := do(t, c, http.MethodPost, base+"/api/indexers/test", nil, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", resp.StatusCode, body)
	}
	var out testAllBody
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("unmarshal: %v (body %s)", err, body)
	}
	if len(out.Results) != 0 {
		t.Fatalf("results = %v, want empty", out.Results)
	}
}

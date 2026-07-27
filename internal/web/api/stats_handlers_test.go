package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/autobrr/harbrr/internal/indexer/registry"
	"github.com/autobrr/harbrr/internal/web/api"
)

// statsBody mirrors the indexerStatsResponse JSON shape for assertions.
type statsBody struct {
	Slug          string `json:"slug"`
	Queries       int64  `json:"queries"`
	Grabs         int64  `json:"grabs"`
	AvgResponseMs int64  `json:"avgResponseMs"`
	Failures      struct {
		AuthFailure int64 `json:"authFailure"`
		RateLimited int64 `json:"rateLimited"`
		ParseError  int64 `json:"parseError"`
		AntiBot     int64 `json:"antiBot"`
	} `json:"failures"`
	LastQueryAt   *string     `json:"lastQueryAt"`
	LastFailureAt *string     `json:"lastFailureAt"`
	Budget        *budgetBody `json:"budget"`
}

// budgetBody mirrors the indexerBudget JSON shape for assertions.
type budgetBody struct {
	Unit      string `json:"unit"`
	PeriodEnd string `json:"periodEnd"`
	Query     struct {
		Used    int64 `json:"used"`
		Limit   int   `json:"limit"`
		Learned bool  `json:"learned"`
	} `json:"query"`
	Grab struct {
		Used    int64 `json:"used"`
		Limit   int   `json:"limit"`
		Learned bool  `json:"learned"`
	} `json:"grab"`
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

// TestIndexerStatsBudget: the per-indexer stats carry the request-budget block for the
// current period — the operator's configured cap, a zero count for an indexer nothing
// has queried yet, and a period boundary in the future (autobrr/harbrr#402). The
// all-indexers list omits the block entirely rather than emitting a zeroed one that
// would read as "no budget configured".
func TestIndexerStatsBudget(t *testing.T) {
	t.Parallel()
	e := authDisabledEnv(t)
	if _, err := e.registry.Add(context.Background(), registry.AddParams{
		Slug: "tt", DefinitionID: "testtracker",
		Settings: map[string]string{"apikey": "x", "query_limit": "2000", "limits_unit": "hour"},
	}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	base, c := serve(t, e)

	_, body := do(t, c, http.MethodGet, base+"/api/indexers/tt/stats", nil, nil)
	var st statsBody
	if err := json.Unmarshal(body, &st); err != nil {
		t.Fatalf("unmarshal: %v (body %s)", err, body)
	}
	if st.Budget == nil {
		t.Fatalf("budget block absent from per-indexer stats (body %s)", body)
	}
	if st.Budget.Unit != "hour" || st.Budget.Query.Limit != 2000 || st.Budget.Query.Used != 0 || st.Budget.Query.Learned {
		t.Errorf("budget = %+v, want the configured hourly cap of 2000 with nothing used or learned", st.Budget)
	}
	// The uncapped grab kind reports no cap rather than an invented one.
	if st.Budget.Grab.Limit != 0 {
		t.Errorf("grab limit = %d, want 0 (no cap configured)", st.Budget.Grab.Limit)
	}
	end, err := time.Parse(time.RFC3339, st.Budget.PeriodEnd)
	if err != nil || !end.After(time.Now()) {
		t.Errorf("periodEnd = %q (err %v), want a timestamp in the future", st.Budget.PeriodEnd, err)
	}

	_, body = do(t, c, http.MethodGet, base+"/api/indexers/stats", nil, nil)
	var all []statsBody
	if err := json.Unmarshal(body, &all); err != nil {
		t.Fatalf("unmarshal all: %v (body %s)", err, body)
	}
	if len(all) != 1 || all[0].Budget != nil {
		t.Errorf("all-indexers stats = %+v, want one row with no budget block", all)
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

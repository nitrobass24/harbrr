package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/autobrr/harbrr/internal/database"
	"github.com/autobrr/harbrr/internal/domain"
	"github.com/autobrr/harbrr/internal/web/api"
)

// TestGetIndexerFallsBackToConfiguredHostWhenFailoverUnresolvable pins the REQUIRED
// effectiveBaseUrl against going empty on a readable indexer (#375).
//
// The instance below references a definition that no longer loads — what a vendored-def
// refresh leaves behind when it drops a tracker an instance still points at. Get does not
// resolve the definition, so the indexer still reads fine; only FailoverState fails, and
// it yields a ZERO struct. Reporting that struct's empty EffectiveBaseURL would tell the
// operator this indexer talks to nothing, so the handler falls back to the configured
// host. The failover fields stay empty: with no definition, no promotion is knowable.
func TestGetIndexerFallsBackToConfiguredHostWhenFailoverUnresolvable(t *testing.T) {
	t.Parallel()
	e := authDisabledEnv(t)
	const configuredHost = "https://configured.invalid/"

	now := time.Now().UTC()
	if _, err := (database.Instances{}).Insert(context.Background(), e.db, domain.IndexerInstance{
		Slug: "orphaned", DefinitionID: "definition-that-no-longer-exists", Name: "Orphaned",
		BaseURL: configuredHost, Enabled: true, Protocol: "torrent",
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("insert instance: %v", err)
	}

	base, c := serve(t, e)
	resp, body := do(t, c, http.MethodGet, base+"/api/indexers/orphaned", nil, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 — an unloadable definition must not turn a readable indexer into an error (body %s)",
			resp.StatusCode, body)
	}
	var out struct {
		EffectiveBaseURL string `json:"effectiveBaseUrl"`
		FailoverBaseURL  string `json:"failoverBaseUrl"`
		FailoverDisabled bool   `json:"failoverDisabled"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("unmarshal: %v (body %s)", err, body)
	}
	if out.EffectiveBaseURL != configuredHost {
		t.Errorf("effectiveBaseUrl = %q, want the configured host %q (never empty on a readable indexer)",
			out.EffectiveBaseURL, configuredHost)
	}
	if out.FailoverBaseURL != "" || out.FailoverDisabled {
		t.Errorf("failover fields = %q/%v, want zero — no promotion is knowable without the definition",
			out.FailoverBaseURL, out.FailoverDisabled)
	}
}

// TestListIndexersCarriesFailoverStanding pins the list payload's failover fields
// (autobrr/harbrr#684): the indexer table reads the pill's state from the list rows
// instead of fanning a detail request out per slug. A promoted instance reports the
// host it moved to; an unpromoted one omits failoverBaseUrl entirely, the same way the
// detail view represents absence.
func TestListIndexersCarriesFailoverStanding(t *testing.T) {
	t.Parallel()
	e := newEnv(t, api.Config{})
	base, c := serve(t, e)
	setupAndLogin(t, base, c)

	// The promoted host has to be one the definition still lists, or the promotion is
	// ignored — a stale failover_base_url would offer a revert from nothing.
	const promotedHost = "https://html.invalid/"
	resp, body := do(t, c, http.MethodPost, base+"/api/indexers", map[string]any{
		"slug": "promoted", "definitionId": "testtracker", "baseUrl": "https://configured.invalid/",
		"settings": map[string]string{"failover_base_url": promotedHost, "failover_disabled": "true"},
	}, nil)
	mustStatus(t, resp, body, http.StatusCreated)

	resp, body = do(t, c, http.MethodPost, base+"/api/indexers",
		map[string]any{"slug": "plain", "definitionId": "testtracker"}, nil)
	mustStatus(t, resp, body, http.StatusCreated)

	resp, body = do(t, c, http.MethodGet, base+"/api/indexers", nil, nil)
	mustStatus(t, resp, body, http.StatusOK)
	var list []struct {
		Slug             string `json:"slug"`
		FailoverBaseURL  string `json:"failoverBaseUrl"`
		FailoverDisabled bool   `json:"failoverDisabled"`
	}
	if err := json.Unmarshal(body, &list); err != nil {
		t.Fatalf("unmarshal: %v (body %s)", err, body)
	}
	if len(list) != 2 {
		t.Fatalf("list returned %d rows, want 2 (body %s)", len(list), body)
	}
	for _, row := range list {
		wantURL, wantDisabled := "", false
		if row.Slug == "promoted" {
			wantURL, wantDisabled = promotedHost, true
		}
		if row.FailoverBaseURL != wantURL || row.FailoverDisabled != wantDisabled {
			t.Errorf("%s row = %q/%v, want %q/%v", row.Slug, row.FailoverBaseURL, row.FailoverDisabled, wantURL, wantDisabled)
		}
	}
	// Absence is an omitted key, not an empty string — only the promoted row carries it.
	if n := strings.Count(string(body), "failoverBaseUrl"); n != 1 {
		t.Errorf("failoverBaseUrl appears %d times, want 1 (omitted on the unpromoted row); body %s", n, body)
	}
}

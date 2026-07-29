package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/autobrr/harbrr/internal/database"
	"github.com/autobrr/harbrr/internal/domain"
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

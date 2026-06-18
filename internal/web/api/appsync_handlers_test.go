package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/autobrr/harbrr/internal/appsync"
	"github.com/autobrr/harbrr/internal/database"
	"github.com/autobrr/harbrr/internal/domain"
	"github.com/autobrr/harbrr/internal/web/api"
)

// fakeAppSource is the IndexerSource the test env injects into the app-sync service.
type fakeAppSource struct {
	instances []domain.IndexerInstance
	cats      map[string][]appsync.Category
}

func (f *fakeAppSource) List(context.Context) ([]domain.IndexerInstance, error) {
	return f.instances, nil
}

func (f *fakeAppSource) Categories(_ context.Context, slug string) ([]appsync.Category, error) {
	return f.cats[slug], nil
}

func createConnBody(name, baseURL string) map[string]any {
	return map[string]any{
		"name": name, "kind": "sonarr", "baseUrl": baseURL,
		"apiKey": "secret-app-key", "harbrrUrl": "http://harbrr:8787",
	}
}

func TestAppConnectionCRUD(t *testing.T) {
	t.Parallel()
	base, c := serve(t, newEnv(t, api.Config{}))
	setupAndLogin(t, base, c)
	url := base + "/api/app-connections"

	// Create — the app key must never appear in the response; defaults applied.
	resp, body := do(t, c, http.MethodPost, url, createConnBody("Sonarr", "http://sonarr:8989"), nil)
	mustStatus(t, resp, body, http.StatusCreated)
	if strings.Contains(string(body), "secret-app-key") || !strings.Contains(string(body), "redacted") {
		t.Fatalf("create response leaked or did not redact the app key: %s", body)
	}
	var created struct {
		ID         int64  `json:"id"`
		SyncLevel  string `json:"syncLevel"`
		IndexScope string `json:"indexScope"`
		Priority   int    `json:"priority"`
	}
	if err := json.Unmarshal(body, &created); err != nil {
		t.Fatalf("decode create: %v", err)
	}
	if created.SyncLevel != "full" || created.IndexScope != "all" || created.Priority != 25 {
		t.Errorf("defaults not applied: %+v", created)
	}
	id := itoa(created.ID)

	// List.
	resp, body = do(t, c, http.MethodGet, url, nil, nil)
	mustStatus(t, resp, body, http.StatusOK)
	if !strings.Contains(string(body), `"name":"Sonarr"`) {
		t.Errorf("list missing the connection: %s", body)
	}

	// Patch the sync level.
	resp, body = do(t, c, http.MethodPatch, url+"/"+id, map[string]any{"syncLevel": "add_update"}, nil)
	mustStatus(t, resp, body, http.StatusNoContent)
	resp, body = do(t, c, http.MethodGet, url+"/"+id, nil, nil)
	mustStatus(t, resp, body, http.StatusOK)
	if !strings.Contains(string(body), `"syncLevel":"add_update"`) {
		t.Errorf("patch not applied: %s", body)
	}

	// Disable then enable.
	resp, body = do(t, c, http.MethodPost, url+"/"+id+"/disable", nil, nil)
	mustStatus(t, resp, body, http.StatusNoContent)
	_, body = do(t, c, http.MethodGet, url+"/"+id, nil, nil)
	if !strings.Contains(string(body), `"enabled":false`) {
		t.Errorf("disable not applied: %s", body)
	}
	resp, body = do(t, c, http.MethodPost, url+"/"+id+"/enable", nil, nil)
	mustStatus(t, resp, body, http.StatusNoContent)

	// Status exposes the (empty) ledger.
	resp, body = do(t, c, http.MethodGet, url+"/"+id+"/status", nil, nil)
	mustStatus(t, resp, body, http.StatusOK)
	if !strings.Contains(string(body), `"indexers":`) {
		t.Errorf("status missing ledger: %s", body)
	}

	// Delete, then it is gone.
	resp, body = do(t, c, http.MethodDelete, url+"/"+id, nil, nil)
	mustStatus(t, resp, body, http.StatusNoContent)
	resp, body = do(t, c, http.MethodGet, url+"/"+id, nil, nil)
	mustStatus(t, resp, body, http.StatusNotFound)
}

func TestAppConnectionErrors(t *testing.T) {
	t.Parallel()
	base, c := serve(t, newEnv(t, api.Config{}))
	setupAndLogin(t, base, c)
	url := base + "/api/app-connections"

	// Create one, then a duplicate (same kind+baseUrl) is a conflict.
	resp, body := do(t, c, http.MethodPost, url, createConnBody("A", "http://dup:8989"), nil)
	mustStatus(t, resp, body, http.StatusCreated)
	resp, body = do(t, c, http.MethodPost, url, createConnBody("B", "http://dup:8989"), nil)
	mustStatus(t, resp, body, http.StatusConflict)

	// Bad kind → 400.
	resp, body = do(t, c, http.MethodPost, url, map[string]any{
		"name": "x", "kind": "plex", "baseUrl": "http://x", "apiKey": "k", "harbrrUrl": "http://h",
	}, nil)
	mustStatus(t, resp, body, http.StatusBadRequest)

	// Unknown id → 404; non-numeric id → 400.
	resp, body = do(t, c, http.MethodGet, url+"/999", nil, nil)
	mustStatus(t, resp, body, http.StatusNotFound)
	resp, body = do(t, c, http.MethodGet, url+"/abc", nil, nil)
	mustStatus(t, resp, body, http.StatusBadRequest)
}

func TestAppConnectionSyncOverHTTP(t *testing.T) {
	t.Parallel()
	e := newEnv(t, api.Config{})
	base, c := serve(t, e)
	setupAndLogin(t, base, c)

	// A Sonarr stub that accepts the pushed indexer.
	stub := httptest.NewServer(sonarrStubHandler())
	t.Cleanup(stub.Close)

	// One real instance (FK for the ledger) advertised by the source.
	instID := seedInst(t, e.db, "tracker-a")
	e.source.instances = []domain.IndexerInstance{{ID: instID, Slug: "tracker-a", Name: "Tracker A", Enabled: true}}
	e.source.cats = map[string][]appsync.Category{"tracker-a": {{ID: 5000, Name: "TV"}}}

	url := base + "/api/app-connections"
	resp, body := do(t, c, http.MethodPost, url, createConnBody("Sonarr", stub.URL), nil)
	mustStatus(t, resp, body, http.StatusCreated)
	var created struct {
		ID int64 `json:"id"`
	}
	_ = json.Unmarshal(body, &created)
	id := itoa(created.ID)

	// Test the connection (stub lists fine) and sync (pushes the one indexer).
	resp, body = do(t, c, http.MethodPost, url+"/"+id+"/test", nil, nil)
	mustStatus(t, resp, body, http.StatusOK)
	if !strings.Contains(string(body), `"ok":true`) {
		t.Errorf("test = %s, want ok:true", body)
	}
	resp, body = do(t, c, http.MethodPost, url+"/"+id+"/sync", nil, nil)
	mustStatus(t, resp, body, http.StatusOK)
	if !strings.Contains(string(body), `"status":"ok"`) || !strings.Contains(string(body), `"action":"created"`) {
		t.Errorf("sync = %s, want ok + a created result", body)
	}
}

// sonarrStubHandler is a minimal Sonarr v3 indexer API for the HTTP sync test.
func sonarrStubHandler() http.Handler {
	mux := http.NewServeMux()
	next := 0
	mux.HandleFunc("GET /api/v3/indexer", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("[]"))
	})
	mux.HandleFunc("POST /api/v3/indexer", func(w http.ResponseWriter, _ *http.Request) {
		next++
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":` + itoa(int64(next)) + `}`))
	})
	return mux
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func seedInst(t *testing.T, db *database.DB, slug string) int64 {
	t.Helper()
	now := time.Now().UTC()
	id, err := (database.Instances{}).Insert(context.Background(), db, domain.IndexerInstance{
		Slug: slug, DefinitionID: "def", Name: slug, Enabled: true, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("seed instance: %v", err)
	}
	return id
}

package api_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/autobrr/harbrr/internal/web/api"
)

func TestSyncProfileCRUD(t *testing.T) {
	t.Parallel()
	base, c := serve(t, newEnv(t, api.Config{}))
	setupAndLogin(t, base, c)
	url := base + "/api/sync-profiles"

	// Create — omitted indexerIds means every compatible indexer, echoed as [].
	resp, body := do(t, c, http.MethodPost, url, map[string]any{"name": "movies"}, nil)
	mustStatus(t, resp, body, http.StatusCreated)
	var created struct {
		ID         int64   `json:"id"`
		IndexerIDs []int64 `json:"indexerIds"`
	}
	if err := json.Unmarshal(body, &created); err != nil {
		t.Fatalf("decode create: %v", err)
	}
	if created.IndexerIDs == nil || len(created.IndexerIDs) != 0 {
		t.Errorf("indexerIds should default to an empty array: %+v", created)
	}
	id := itoa(created.ID)

	// List includes it.
	resp, body = do(t, c, http.MethodGet, url, nil, nil)
	mustStatus(t, resp, body, http.StatusOK)
	if !strings.Contains(string(body), `"name":"movies"`) {
		t.Errorf("list missing the profile: %s", body)
	}

	// Duplicate name → 409.
	resp, body = do(t, c, http.MethodPost, url, map[string]any{"name": "movies"}, nil)
	mustStatus(t, resp, body, http.StatusConflict)

	// Unknown instance id in indexerIds → 400.
	resp, body = do(t, c, http.MethodPost, url, map[string]any{"name": "bad", "indexerIds": []int64{99999}}, nil)
	mustStatus(t, resp, body, http.StatusBadRequest)

	// Patch with a present-but-empty indexerIds array clears the selection (a no-op here
	// since it was already empty, but proves the present-empty semantics wire through).
	resp, body = do(t, c, http.MethodPatch, url+"/"+id, map[string]any{"indexerIds": []int64{}}, nil)
	mustStatus(t, resp, body, http.StatusNoContent)
	resp, body = do(t, c, http.MethodGet, url+"/"+id, nil, nil)
	mustStatus(t, resp, body, http.StatusOK)
	if !strings.Contains(string(body), `"indexerIds":[]`) {
		t.Errorf("indexerIds not cleared to []: %s", body)
	}

	// Delete, then it is gone; a non-numeric id is a 400.
	resp, body = do(t, c, http.MethodDelete, url+"/"+id, nil, nil)
	mustStatus(t, resp, body, http.StatusNoContent)
	resp, body = do(t, c, http.MethodGet, url+"/"+id, nil, nil)
	mustStatus(t, resp, body, http.StatusNotFound)
	resp, body = do(t, c, http.MethodGet, url+"/abc", nil, nil)
	mustStatus(t, resp, body, http.StatusBadRequest)
}

// TestSyncProfileDeleteRefusedWhileInUse proves deleting a profile a connection
// references is a 409, and succeeds once the connection is detached.
func TestSyncProfileDeleteRefusedWhileInUse(t *testing.T) {
	t.Parallel()
	base, c := serve(t, newEnv(t, api.Config{}))
	setupAndLogin(t, base, c)

	resp, body := do(t, c, http.MethodPost, base+"/api/sync-profiles", map[string]any{"name": "p"}, nil)
	mustStatus(t, resp, body, http.StatusCreated)
	var profile struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(body, &profile); err != nil {
		t.Fatalf("decode profile: %v", err)
	}

	connURL := base + "/api/app-connections"
	create := createConnBody("Sonarr", "http://sonarr:8989")
	create["syncProfileId"] = profile.ID
	resp, body = do(t, c, http.MethodPost, connURL, create, nil)
	mustStatus(t, resp, body, http.StatusCreated)

	profileURL := base + "/api/sync-profiles/" + itoa(profile.ID)
	resp, body = do(t, c, http.MethodDelete, profileURL, nil, nil)
	mustStatus(t, resp, body, http.StatusConflict)

	// Detach, then the delete succeeds.
	respConn, bodyConn := do(t, c, http.MethodGet, connURL, nil, nil)
	mustStatus(t, respConn, bodyConn, http.StatusOK)
	var conns []struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(bodyConn, &conns); err != nil || len(conns) != 1 {
		t.Fatalf("decode connections: %v (%d rows)", err, len(conns))
	}
	resp, body = do(t, c, http.MethodPatch, connURL+"/"+itoa(conns[0].ID), map[string]any{"syncProfileId": nil}, nil)
	mustStatus(t, resp, body, http.StatusNoContent)

	resp, body = do(t, c, http.MethodDelete, profileURL, nil, nil)
	mustStatus(t, resp, body, http.StatusNoContent)
}

// TestConnectionSyncProfileRef checks the connection<->profile wiring over HTTP: a
// created connection echoes syncProfileId; an omitted PATCH keeps it; an explicit null
// clears it; an unknown id 400s; and a qui connection may reference a profile (#365
// dropped the old qui rejection).
func TestConnectionSyncProfileRef(t *testing.T) {
	t.Parallel()
	base, c := serve(t, newEnv(t, api.Config{}))
	setupAndLogin(t, base, c)

	resp, body := do(t, c, http.MethodPost, base+"/api/sync-profiles", map[string]any{"name": "tv"}, nil)
	mustStatus(t, resp, body, http.StatusCreated)
	var profile struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(body, &profile); err != nil {
		t.Fatalf("decode profile: %v", err)
	}

	// Create a Sonarr connection referencing the profile — the response echoes it.
	connURL := base + "/api/app-connections"
	create := createConnBody("Sonarr", "http://sonarr:8989")
	create["syncProfileId"] = profile.ID
	resp, body = do(t, c, http.MethodPost, connURL, create, nil)
	mustStatus(t, resp, body, http.StatusCreated)
	var conn struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(body, &conn); err != nil {
		t.Fatalf("decode connection: %v", err)
	}
	id := itoa(conn.ID)
	if !strings.Contains(string(body), `"syncProfileId":`+itoa(profile.ID)) {
		t.Errorf("create response missing syncProfileId: %s", body)
	}

	// A PATCH that omits syncProfileId keeps it.
	resp, body = do(t, c, http.MethodPatch, connURL+"/"+id, map[string]any{"name": "Renamed"}, nil)
	mustStatus(t, resp, body, http.StatusNoContent)
	resp, body = do(t, c, http.MethodGet, connURL+"/"+id, nil, nil)
	mustStatus(t, resp, body, http.StatusOK)
	if !strings.Contains(string(body), `"syncProfileId":`+itoa(profile.ID)) {
		t.Errorf("omitted PATCH cleared syncProfileId: %s", body)
	}

	// An unknown profile id is a 400.
	resp, body = do(t, c, http.MethodPatch, connURL+"/"+id, map[string]any{"syncProfileId": 99999}, nil)
	mustStatus(t, resp, body, http.StatusBadRequest)

	// An explicit null clears it (omitempty → absent on read-back).
	resp, body = do(t, c, http.MethodPatch, connURL+"/"+id, map[string]any{"syncProfileId": nil}, nil)
	mustStatus(t, resp, body, http.StatusNoContent)
	resp, body = do(t, c, http.MethodGet, connURL+"/"+id, nil, nil)
	mustStatus(t, resp, body, http.StatusOK)
	if strings.Contains(string(body), "syncProfileId") {
		t.Errorf("explicit null did not clear syncProfileId: %s", body)
	}

	// A qui connection may now carry a profile ref (#365).
	qui := map[string]any{
		"name": "qui", "kind": "qui", "baseUrl": "http://qui:7000",
		"apiKey": "k", "harbrrUrl": "http://harbrr:8787", "syncProfileId": profile.ID,
	}
	resp, body = do(t, c, http.MethodPost, connURL, qui, nil)
	mustStatus(t, resp, body, http.StatusCreated)
	if !strings.Contains(string(body), `"syncProfileId":`+itoa(profile.ID)) {
		t.Errorf("qui create response missing syncProfileId: %s", body)
	}
}

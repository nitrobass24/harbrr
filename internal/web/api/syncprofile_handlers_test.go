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

	// Create — omitted toggles default to true; categories echo back as an array.
	resp, body := do(t, c, http.MethodPost, url, map[string]any{
		"name": "movies", "categories": []int{5000, 2000}, "minSeeders": 3,
	}, nil)
	mustStatus(t, resp, body, http.StatusCreated)
	var created struct {
		ID                      int64 `json:"id"`
		Categories              []int `json:"categories"`
		MinSeeders              int   `json:"minSeeders"`
		EnableRss               bool  `json:"enableRss"`
		EnableAutomaticSearch   bool  `json:"enableAutomaticSearch"`
		EnableInteractiveSearch bool  `json:"enableInteractiveSearch"`
	}
	if err := json.Unmarshal(body, &created); err != nil {
		t.Fatalf("decode create: %v", err)
	}
	if !created.EnableRss || !created.EnableAutomaticSearch || !created.EnableInteractiveSearch {
		t.Errorf("toggles should default to true: %+v", created)
	}
	if created.MinSeeders != 3 || len(created.Categories) != 2 {
		t.Errorf("create echoed wrong body: %+v", created)
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

	// Invalid category id → 400.
	resp, body = do(t, c, http.MethodPost, url, map[string]any{"name": "bad", "categories": []int{0}}, nil)
	mustStatus(t, resp, body, http.StatusBadRequest)

	// Patch with a present-but-empty categories array clears the set.
	resp, body = do(t, c, http.MethodPatch, url+"/"+id, map[string]any{"categories": []int{}}, nil)
	mustStatus(t, resp, body, http.StatusNoContent)
	resp, body = do(t, c, http.MethodGet, url+"/"+id, nil, nil)
	mustStatus(t, resp, body, http.StatusOK)
	if !strings.Contains(string(body), `"categories":[]`) {
		t.Errorf("categories not cleared to []: %s", body)
	}

	// Delete, then it is gone; a non-numeric id is a 400.
	resp, body = do(t, c, http.MethodDelete, url+"/"+id, nil, nil)
	mustStatus(t, resp, body, http.StatusNoContent)
	resp, body = do(t, c, http.MethodGet, url+"/"+id, nil, nil)
	mustStatus(t, resp, body, http.StatusNotFound)
	resp, body = do(t, c, http.MethodGet, url+"/abc", nil, nil)
	mustStatus(t, resp, body, http.StatusBadRequest)
}

// TestConnectionSyncProfileRef checks the connection<->profile wiring over HTTP: a
// created connection echoes syncProfileId; an omitted PATCH keeps it; an explicit null
// clears it; an unknown id 400s; and qui rejects a profile ref.
func TestConnectionSyncProfileRef(t *testing.T) {
	t.Parallel()
	base, c := serve(t, newEnv(t, api.Config{}))
	setupAndLogin(t, base, c)

	// A TV profile overlaps Sonarr's content range.
	resp, body := do(t, c, http.MethodPost, base+"/api/sync-profiles", map[string]any{
		"name": "tv", "categories": []int{5000},
	}, nil)
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

	// A qui connection may not carry a profile ref.
	qui := map[string]any{
		"name": "qui", "kind": "qui", "baseUrl": "http://qui:7000",
		"apiKey": "k", "harbrrUrl": "http://harbrr:8787", "syncProfileId": profile.ID,
	}
	resp, body = do(t, c, http.MethodPost, connURL, qui, nil)
	mustStatus(t, resp, body, http.StatusBadRequest)
}

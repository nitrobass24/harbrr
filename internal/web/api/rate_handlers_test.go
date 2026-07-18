package api_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/autobrr/harbrr/internal/web/api"
)

// rateLimitReq is the typed request/response body for the rate-limit-default
// endpoint (autobrr/harbrr#104).
type rateLimitReq struct {
	DefaultInterval string `json:"defaultInterval"`
}

// TestRateLimitEndpoint exercises the global rate-limit-default API end to end: get
// the seed, put a new value (live + persisted), get reflects it, and an invalid
// duration answers 400 and changes nothing.
func TestRateLimitEndpoint(t *testing.T) {
	e := newEnv(t, api.Config{})
	base, c := serve(t, e)
	setupAndLogin(t, base, c)

	resp, body := do(t, c, http.MethodGet, base+"/api/config/rate-limit", nil, nil)
	mustStatus(t, resp, body, http.StatusOK)
	var got rateLimitReq
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode get: %v", err)
	}
	if got.DefaultInterval != "1s" {
		t.Errorf("seed defaultInterval = %q, want 1s", got.DefaultInterval)
	}

	resp, body = do(t, c, http.MethodPut, base+"/api/config/rate-limit", rateLimitReq{DefaultInterval: "5s"}, nil)
	mustStatus(t, resp, body, http.StatusOK)
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode put: %v", err)
	}
	if got.DefaultInterval != "5s" {
		t.Errorf("PUT defaultInterval = %q, want 5s", got.DefaultInterval)
	}

	resp, body = do(t, c, http.MethodGet, base+"/api/config/rate-limit", nil, nil)
	mustStatus(t, resp, body, http.StatusOK)
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode get after put: %v", err)
	}
	if got.DefaultInterval != "5s" {
		t.Errorf("GET after PUT = %q, want 5s", got.DefaultInterval)
	}

	resp, body = do(t, c, http.MethodPut, base+"/api/config/rate-limit", rateLimitReq{DefaultInterval: "not-a-duration"}, nil)
	mustStatus(t, resp, body, http.StatusBadRequest)

	resp, body = do(t, c, http.MethodPut, base+"/api/config/rate-limit", rateLimitReq{DefaultInterval: "0s"}, nil)
	mustStatus(t, resp, body, http.StatusBadRequest)

	// The rejected PUTs must not have changed anything.
	resp, body = do(t, c, http.MethodGet, base+"/api/config/rate-limit", nil, nil)
	mustStatus(t, resp, body, http.StatusOK)
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode get after rejected puts: %v", err)
	}
	if got.DefaultInterval != "5s" {
		t.Errorf("after rejected PUTs defaultInterval = %q, want unchanged 5s", got.DefaultInterval)
	}
}

// TestRateLimitEndpointRequiresAuth proves the routes sit behind the authenticated group.
func TestRateLimitEndpointRequiresAuth(t *testing.T) {
	e := newEnv(t, api.Config{})
	base, c := serve(t, e)

	resp, body := do(t, c, http.MethodGet, base+"/api/config/rate-limit", nil, nil)
	mustStatus(t, resp, body, http.StatusUnauthorized)
}

package api_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/rs/zerolog"

	"github.com/autobrr/harbrr/internal/web/api"
)

// TestLogLevelEndpoint exercises the runtime log-level API end to end (auth, get, set,
// persistence-through-get, and the invalid-level 400). It mutates the process-global
// level, so it is not parallel and restores a permissive default.
func TestLogLevelEndpoint(t *testing.T) {
	defer zerolog.SetGlobalLevel(zerolog.TraceLevel)

	e := newEnv(t, api.Config{})
	base, c := serve(t, e)
	setupAndLogin(t, base, c)

	// PUT a valid level -> 200, echoes the applied level.
	resp, body := do(t, c, http.MethodPut, base+"/api/config/log-level", logLevelReq{Level: "debug"}, nil)
	mustStatus(t, resp, body, http.StatusOK)
	var got struct {
		Level string `json:"level"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode put: %v", err)
	}
	if got.Level != "debug" {
		t.Errorf("PUT level = %q, want debug", got.Level)
	}

	// GET reflects the applied level.
	resp, body = do(t, c, http.MethodGet, base+"/api/config/log-level", nil, nil)
	mustStatus(t, resp, body, http.StatusOK)
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode get: %v", err)
	}
	if got.Level != "debug" {
		t.Errorf("GET after PUT = %q, want debug", got.Level)
	}

	// An unknown level is a 400 and changes nothing.
	resp, body = do(t, c, http.MethodPut, base+"/api/config/log-level", logLevelReq{Level: "loud"}, nil)
	mustStatus(t, resp, body, http.StatusBadRequest)
}

// logLevelReq is the typed request body for the log-level endpoint (compile-time shape).
type logLevelReq struct {
	Level string `json:"level"`
}

// TestLogLevelEndpointRequiresAuth proves the routes sit behind the authenticated group.
func TestLogLevelEndpointRequiresAuth(t *testing.T) {
	e := newEnv(t, api.Config{})
	base, c := serve(t, e)

	resp, body := do(t, c, http.MethodGet, base+"/api/config/log-level", nil, nil)
	mustStatus(t, resp, body, http.StatusUnauthorized)
}

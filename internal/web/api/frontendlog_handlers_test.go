package api_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/rs/zerolog"

	"github.com/autobrr/harbrr/internal/web/api"
)

// frontendLogReq is the typed request body for POST /api/logs/frontend (compile-time
// shape, mirrors internal/web/api's unexported frontendLogBody).
type frontendLogReq struct {
	Level   string `json:"level"`
	Message string `json:"message"`
	Context string `json:"context,omitempty"`
}

// TestPostFrontendLogRequiresAuth proves the route sits behind the authenticated group.
func TestPostFrontendLogRequiresAuth(t *testing.T) {
	t.Parallel()

	e := newEnv(t, api.Config{})
	base, c := serve(t, e)

	resp, body := do(t, c, http.MethodPost, base+"/api/logs/frontend",
		frontendLogReq{Level: "error", Message: "boom"}, nil)
	mustStatus(t, resp, body, http.StatusUnauthorized)
}

// TestPostFrontendLogValidation exercises the level enum, required message, and both
// length caps.
func TestPostFrontendLogValidation(t *testing.T) {
	t.Parallel()

	e := newEnv(t, api.Config{})
	base, c := serve(t, e)
	setupAndLogin(t, base, c)

	tests := []struct {
		name string
		req  frontendLogReq
		want int
	}{
		{"error level", frontendLogReq{Level: "error", Message: "a fetch failed"}, http.StatusNoContent},
		{"warn level", frontendLogReq{Level: "warn", Message: "a fetch was slow"}, http.StatusNoContent},
		{"info level", frontendLogReq{Level: "info", Message: "user did a thing"}, http.StatusNoContent},
		{"with context", frontendLogReq{Level: "error", Message: "save failed", Context: "500: internal error"}, http.StatusNoContent},
		{"unknown level", frontendLogReq{Level: "loud", Message: "boom"}, http.StatusBadRequest},
		{"empty level", frontendLogReq{Level: "", Message: "boom"}, http.StatusBadRequest},
		{"empty message", frontendLogReq{Level: "error", Message: ""}, http.StatusBadRequest},
		{"oversize message", frontendLogReq{Level: "error", Message: strings.Repeat("x", 1025)}, http.StatusBadRequest},
		{"message at cap", frontendLogReq{Level: "error", Message: strings.Repeat("x", 1024)}, http.StatusNoContent},
		{"oversize context", frontendLogReq{Level: "error", Message: "boom", Context: strings.Repeat("x", 4097)}, http.StatusBadRequest},
		{"context at cap", frontendLogReq{Level: "error", Message: "boom", Context: strings.Repeat("x", 4096)}, http.StatusNoContent},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, body := do(t, c, http.MethodPost, base+"/api/logs/frontend", tt.req, nil)
			mustStatus(t, resp, body, tt.want)
		})
	}
}

// TestPostFrontendLogWritesLogEntry proves the handler actually relays the toast into
// the daemon's zerolog stream at the requested level, tagged component=webui, and that
// an absent context omits the field rather than writing it empty. It pins the global
// level permissive (like TestLogLevelEndpoint) since zerolog's threshold is process-
// global, so it must not run in parallel with a test that changes it.
func TestPostFrontendLogWritesLogEntry(t *testing.T) {
	prev := zerolog.GlobalLevel()
	zerolog.SetGlobalLevel(zerolog.TraceLevel)
	defer zerolog.SetGlobalLevel(prev)

	var buf bytes.Buffer
	e := newEnvWithLogger(t, api.Config{}, zerolog.New(&buf))
	base, c := serve(t, e)
	setupAndLogin(t, base, c)
	buf.Reset() // drop the setup/login request's own log lines, if any.

	resp, body := do(t, c, http.MethodPost, base+"/api/logs/frontend",
		frontendLogReq{Level: "error", Message: "add indexer failed", Context: "network error"}, nil)
	mustStatus(t, resp, body, http.StatusNoContent)

	var entry struct {
		Level     string `json:"level"`
		Component string `json:"component"`
		Message   string `json:"message"`
		Context   string `json:"context"`
	}
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("expected exactly one log line, got %d: %q", len(lines), buf.String())
	}
	if err := json.Unmarshal([]byte(lines[0]), &entry); err != nil {
		t.Fatalf("decode log line: %v (line: %s)", err, lines[0])
	}
	if entry.Level != "error" {
		t.Errorf("level = %q, want error", entry.Level)
	}
	if entry.Component != "webui" {
		t.Errorf("component = %q, want webui", entry.Component)
	}
	if entry.Message != "add indexer failed" {
		t.Errorf("message = %q, want %q", entry.Message, "add indexer failed")
	}
	if entry.Context != "network error" {
		t.Errorf("context = %q, want %q", entry.Context, "network error")
	}
}

// TestPostFrontendLogOmitsEmptyContext proves a request with no context writes no
// context field at all, rather than an empty one.
func TestPostFrontendLogOmitsEmptyContext(t *testing.T) {
	prev := zerolog.GlobalLevel()
	zerolog.SetGlobalLevel(zerolog.TraceLevel)
	defer zerolog.SetGlobalLevel(prev)

	var buf bytes.Buffer
	e := newEnvWithLogger(t, api.Config{}, zerolog.New(&buf))
	base, c := serve(t, e)
	setupAndLogin(t, base, c)
	buf.Reset()

	resp, body := do(t, c, http.MethodPost, base+"/api/logs/frontend",
		frontendLogReq{Level: "warn", Message: "slow response"}, nil)
	mustStatus(t, resp, body, http.StatusNoContent)

	var entry map[string]any
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("expected exactly one log line, got %d: %q", len(lines), buf.String())
	}
	if err := json.Unmarshal([]byte(lines[0]), &entry); err != nil {
		t.Fatalf("decode log line: %v (line: %s)", err, lines[0])
	}
	if _, ok := entry["context"]; ok {
		t.Errorf("context field present with no context sent: %v", entry)
	}
}

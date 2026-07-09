package api_test

import (
	"bytes"
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/autobrr/harbrr/internal/web/api"
)

// maxTestBodyBytes mirrors the unexported maxRequestBodyBytes cap in encode.go
// (1 MiB). Kept in sync manually — the point of these tests is to prove the
// boundary, so a drift here would make them assert against the wrong size.
const maxTestBodyBytes = 1 << 20

// postRaw sends an exact byte body (bypassing the JSON marshaling in do) so the
// tests can control the body size precisely.
func postRaw(t *testing.T, c *http.Client, url string, body []byte) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	defer resp.Body.Close()
	data := make([]byte, 0, 512)
	buf := make([]byte, 512)
	for {
		n, rerr := resp.Body.Read(buf)
		data = append(data, buf[:n]...)
		if rerr != nil {
			break
		}
	}
	return resp, data
}

// TestDecodeJSONBodySizeCap proves the shared JSON decoder bounds the request body:
// a valid body just over the cap is rejected with 413 (fails before the fix, which
// buffered the whole body and returned 201), a normal small body still succeeds, and
// a malformed body still returns 400 (the size branch did not break that path). It
// uses the public /api/auth/setup route (decodeJSON, no auth/CSRF) so no credential
// is needed — mirroring the unauthenticated attack surface the cap protects. Each
// case gets its own fresh env: setup is a one-shot action, so an earlier case's
// outcome must never leave a later case racing an already-completed setup.
func TestDecodeJSONBodySizeCap(t *testing.T) {
	t.Parallel()

	// A well-formed credentials object (known fields only) whose password padding
	// pushes the total past maxRequestBodyBytes; before the fix this decoded and
	// returned 201.
	pad := strings.Repeat("x", maxTestBodyBytes) // guarantees the full body exceeds the cap
	oversizeBody := []byte(`{"username":"admin","password":"` + pad + `"}`)
	if len(oversizeBody) <= maxTestBodyBytes {
		t.Fatalf("oversize body is %d bytes, want > cap %d", len(oversizeBody), maxTestBodyBytes)
	}

	tests := []struct {
		name         string
		body         []byte
		wantStatus   int
		wantContains string // empty = skip the body-content assertion
	}{
		{
			name:         "oversize body -> 413 (pre-fix: buffered the whole body and returned 201)",
			body:         oversizeBody,
			wantStatus:   http.StatusRequestEntityTooLarge,
			wantContains: "request body too large",
		},
		{
			name:       "normal small body decodes and completes setup",
			body:       []byte(`{"username":"admin","password":"correct-horse-staple"}`),
			wantStatus: http.StatusCreated,
		},
		{
			name:         "malformed small body still 400s — the size branch didn't swallow it",
			body:         []byte(`{"username":`),
			wantStatus:   http.StatusBadRequest,
			wantContains: "invalid JSON body",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			base, c := serve(t, newEnv(t, api.Config{}))
			resp, body := postRaw(t, c, base+"/api/auth/setup", tt.body)
			if resp.StatusCode != tt.wantStatus {
				t.Fatalf("status = %d, want %d (body: %s)", resp.StatusCode, tt.wantStatus, body)
			}
			if tt.wantContains != "" && !strings.Contains(string(body), tt.wantContains) {
				t.Errorf("body = %s, want it to contain %q", body, tt.wantContains)
			}
		})
	}
}

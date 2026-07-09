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
// is needed — mirroring the unauthenticated attack surface the cap protects.
func TestDecodeJSONBodySizeCap(t *testing.T) {
	t.Parallel()

	base, c := serve(t, newEnv(t, api.Config{}))
	setupURL := base + "/api/auth/setup"

	// (a) A valid JSON object just OVER the cap -> 413. The body is a well-formed
	// credentials object (known fields only) whose password padding pushes the total
	// past maxRequestBodyBytes; before the fix this decoded and returned 201.
	pad := strings.Repeat("x", maxTestBodyBytes) // guarantees the full body exceeds the cap
	oversize := []byte(`{"username":"admin","password":"` + pad + `"}`)
	if len(oversize) <= maxTestBodyBytes {
		t.Fatalf("oversize body is %d bytes, want > cap %d", len(oversize), maxTestBodyBytes)
	}
	resp, body := postRaw(t, c, setupURL, oversize)
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversize body: status = %d, want 413 (body: %s)", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "request body too large") {
		t.Errorf("oversize body message = %s, want to mention 'request body too large'", body)
	}

	// (b) A normal small body decodes and completes setup (201). Uses a fresh env so
	// (a)'s rejection left setup incomplete here and this is the first real setup.
	base2, c2 := serve(t, newEnv(t, api.Config{}))
	resp, body = postRaw(t, c2, base2+"/api/auth/setup",
		[]byte(`{"username":"admin","password":"correct-horse-staple"}`))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("small valid body: status = %d, want 201 (body: %s)", resp.StatusCode, body)
	}

	// (c) A malformed (but small) body still returns 400 invalid JSON — the size branch
	// did not swallow the existing malformed path.
	base3, c3 := serve(t, newEnv(t, api.Config{}))
	resp, body = postRaw(t, c3, base3+"/api/auth/setup", []byte(`{"username":`))
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("malformed body: status = %d, want 400 (body: %s)", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "invalid JSON body") {
		t.Errorf("malformed body message = %s, want 'invalid JSON body'", body)
	}
}

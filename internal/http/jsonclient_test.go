package http

import (
	"context"
	"encoding/json"
	"io"
	stdhttp "net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// jsonBody is the marshalled request body one table case sends.
type jsonBody struct {
	Name string `json:"name"`
}

// recordedRequest is what a test server saw, guarded because the handler runs on the
// server's goroutine.
type recordedRequest struct {
	mu          sync.Mutex
	paths       []string
	auth        string
	contentType string
	body        string
}

func (r *recordedRequest) record(req *stdhttp.Request) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.paths = append(r.paths, req.URL.Path)
	r.auth = req.Header.Get("X-Api-Key")
	r.contentType = req.Header.Get("Content-Type")
	body, _ := io.ReadAll(req.Body)
	r.body = string(body)
}

func (r *recordedRequest) snapshot() recordedRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	return recordedRequest{paths: r.paths, auth: r.auth, contentType: r.contentType, body: r.body}
}

// newTestClient starts a server running handler and returns a JSONClient pointed at
// it, with baseSuffix appended to the base URL (to exercise normalisation).
func newTestClient(t *testing.T, baseSuffix, secret string, handler stdhttp.HandlerFunc) *JSONClient {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return NewJSONClient(JSONClient{
		Prefix: "test: app",
		Base:   srv.URL + baseSuffix,
		Auth:   stdhttp.Header{"X-API-Key": {secret}},
		Client: srv.Client(),
		Secret: secret,
	})
}

// TestJSONClientRequestShape covers the request side: the base URL is normalised once
// (a trailing slash can never produce "//api/..."), the auth header is attached, and
// the body is JSON-encoded — or sent verbatim for a RawBody.
func TestJSONClientRequestShape(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name            string
		baseSuffix      string
		in              any
		wantPath        string
		wantContentType string
		wantBody        string
	}{
		{name: "no body", baseSuffix: "", in: nil, wantPath: "/api/instances"},
		{
			name: "trailing slash on the base is stripped", baseSuffix: "/",
			in: nil, wantPath: "/api/instances",
		},
		{
			name: "several trailing slashes are stripped", baseSuffix: "///",
			in: nil, wantPath: "/api/instances",
		},
		{
			name: "struct body is marshalled as json", baseSuffix: "",
			in:              jsonBody{Name: "x"},
			wantPath:        "/api/instances",
			wantContentType: "application/json",
			wantBody:        `{"name":"x"}`,
		},
		{
			name: "raw body is sent verbatim", baseSuffix: "",
			in:              RawBody{ContentType: "multipart/form-data; boundary=b", Data: []byte("--b--")},
			wantPath:        "/api/instances",
			wantContentType: "multipart/form-data; boundary=b",
			wantBody:        "--b--",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var rec recordedRequest
			c := newTestClient(t, tt.baseSuffix, "an-api-key-value", func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
				rec.record(r)
				_, _ = w.Write([]byte(`{}`))
			})
			if _, err := c.Do(context.Background(), stdhttp.MethodPost, "/api/instances", tt.in, nil); err != nil {
				t.Fatalf("Do: %v", err)
			}
			got := rec.snapshot()
			if len(got.paths) != 1 || got.paths[0] != tt.wantPath {
				t.Errorf("server saw paths %q, want exactly [%q]", got.paths, tt.wantPath)
			}
			if got.auth != "an-api-key-value" {
				t.Errorf("auth header = %q, want the api key", got.auth)
			}
			if got.contentType != tt.wantContentType {
				t.Errorf("Content-Type = %q, want %q", got.contentType, tt.wantContentType)
			}
			if got.body != tt.wantBody {
				t.Errorf("body = %q, want %q", got.body, tt.wantBody)
			}
		})
	}
}

// TestJSONClientNonSuccessBodyPolicy is the security decision this client exists to
// hold in one place: a non-2xx surfaces the status, and the remote's body reaches the
// error ONLY through a Reason parser that allowlists safe fields.
func TestJSONClientNonSuccessBodyPolicy(t *testing.T) {
	t.Parallel()
	const body = `{"message":"instance is offline","apiKey":"leaked-in-the-body"}`

	tests := []struct {
		name       string
		reason     func([]byte) string
		wantSubstr []string
		wantAbsent []string
	}{
		{
			name:       "status only is the default",
			wantSubstr: []string{"test: app", "POST /api/x", "status 409"},
			wantAbsent: []string{"instance is offline", "leaked-in-the-body"},
		},
		{
			name: "a reason parser may surface its allowlisted field",
			reason: func(raw []byte) string {
				var obj struct {
					Message string `json:"message"`
				}
				_ = json.Unmarshal(raw, &obj)
				return obj.Message
			},
			wantSubstr: []string{"status 409", "instance is offline"},
			wantAbsent: []string{"leaked-in-the-body"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c := newTestClient(t, "", "an-api-key-value", func(w stdhttp.ResponseWriter, _ *stdhttp.Request) {
				w.WriteHeader(stdhttp.StatusConflict)
				_, _ = w.Write([]byte(body))
			})
			c.Reason = tt.reason
			status, err := c.Do(context.Background(), stdhttp.MethodPost, "/api/x", nil, nil)
			if err == nil {
				t.Fatal("Do on a 409 returned no error")
			}
			if status != stdhttp.StatusConflict {
				t.Errorf("status = %d, want 409 (the caller must be able to branch on it)", status)
			}
			for _, want := range tt.wantSubstr {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q is missing %q", err, want)
				}
			}
			for _, absent := range tt.wantAbsent {
				if strings.Contains(err.Error(), absent) {
					t.Errorf("error %q leaks %q", err, absent)
				}
			}
		})
	}
}

// TestJSONClientErrorNeverCarriesTheSecret is the property that makes one shared
// client safer than the copies it replaced: the client HOLDS the credential, so it
// value-scrubs it (ScrubValues) out of every error it emits. The pattern scrub alone
// cannot do this — the remote here echoes the key as bare prose, with no
// credential-shaped "key=value" for the denylist to match — so deleting the
// ScrubValues call in scrub() must fail this test.
func TestJSONClientErrorNeverCarriesTheSecret(t *testing.T) {
	t.Parallel()
	const secret = "Zx9QhandledKeyNotAPattern"

	// The worst case a Reason parser can produce: the remote's message quotes the
	// submitted credential verbatim, and the parser passes that text through.
	c := newTestClient(t, "", secret, func(w stdhttp.ResponseWriter, _ *stdhttp.Request) {
		w.WriteHeader(stdhttp.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"message":"the key ` + secret + ` was rejected"}`))
	})
	c.Reason = func(raw []byte) string {
		var obj struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(raw, &obj)
		return obj.Message
	}

	_, err := c.Do(context.Background(), stdhttp.MethodGet, "/api/x", nil, nil)
	if err == nil {
		t.Fatal("Do on a 401 returned no error")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("the error carries the configured secret verbatim: %q", err)
	}
	if !strings.Contains(err.Error(), "was rejected") {
		t.Errorf("the scrub ate the whole message: %q", err)
	}
}

// TestJSONClientShortSecretIsNotScrubbed guards the over-scrub hazard: ScrubValues is
// a literal ReplaceAll, so a degenerate one- or two-character "secret" would shred
// every message it appears in. Below the threshold only the pattern scrub applies.
func TestJSONClientShortSecretIsNotScrubbed(t *testing.T) {
	t.Parallel()
	c := newTestClient(t, "", "ap", func(w stdhttp.ResponseWriter, _ *stdhttp.Request) {
		w.WriteHeader(stdhttp.StatusNotFound)
	})
	_, err := c.Do(context.Background(), stdhttp.MethodGet, "/api/x", nil, nil)
	if err == nil {
		t.Fatal("Do on a 404 returned no error")
	}
	if got := err.Error(); got != "test: app: GET /api/x: status 404" {
		t.Errorf("a 2-char secret shredded the message: %q", got)
	}
}

// TestJSONClientDecodeAndTransportErrors covers the two remaining error paths: a 2xx
// whose body is not the expected shape, and an unreachable host (whose *url.Error —
// which quotes the full request URL — must be scrubbed away).
func TestJSONClientDecodeAndTransportErrors(t *testing.T) {
	t.Parallel()

	t.Run("decode failure names the path, not the payload", func(t *testing.T) {
		t.Parallel()
		c := newTestClient(t, "", "an-api-key-value", func(w stdhttp.ResponseWriter, _ *stdhttp.Request) {
			_, _ = w.Write([]byte(`<html>login</html>`))
		})
		var out []struct{}
		status, err := c.Do(context.Background(), stdhttp.MethodGet, "/api/x", nil, &out)
		if err == nil {
			t.Fatal("decoding HTML into a slice returned no error")
		}
		if status != stdhttp.StatusOK {
			t.Errorf("status = %d, want 200 (the response was fine, the body was not)", status)
		}
		if !strings.Contains(err.Error(), "test: app: decode /api/x") {
			t.Errorf("error %q does not name the caller and path", err)
		}
	})

	t.Run("transport failure drops the request url", func(t *testing.T) {
		t.Parallel()
		c := NewJSONClient(JSONClient{
			Prefix: "test: app",
			Base:   "http://127.0.0.1:1",
			Client: &stdhttp.Client{},
			Secret: "an-api-key-value",
		})
		_, err := c.Do(context.Background(), stdhttp.MethodGet, "/api/x?apikey=an-api-key-value", nil, nil)
		if err == nil {
			t.Fatal("a request to a closed port returned no error")
		}
		if strings.Contains(err.Error(), "an-api-key-value") {
			t.Errorf("the transport error leaks the credential: %q", err)
		}
	})
}

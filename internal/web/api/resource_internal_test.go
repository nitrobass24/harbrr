package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog"

	"github.com/autobrr/harbrr/internal/database"
)

// reqWithID builds a request carrying a chi route context with {id} set to raw, so
// pathID (which reads it via chi.URLParam) can be exercised without a full router.
func reqWithID(raw string) *http.Request {
	r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/x", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", raw)
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}

// TestPathID covers the shared {id} parse: a valid id passes through untouched with
// nothing written, a malformed one writes the per-resource 400 message.
func TestPathID(t *testing.T) {
	t.Parallel()
	t.Run("valid id", func(t *testing.T) {
		t.Parallel()
		rec := httptest.NewRecorder()
		id, ok := pathID(rec, reqWithID("42"), "widget")
		if !ok || id != 42 {
			t.Fatalf("pathID = (%d, %v), want (42, true)", id, ok)
		}
		if rec.Body.Len() != 0 {
			t.Errorf("expected nothing written on success, got %q", rec.Body.String())
		}
	})
	t.Run("malformed id", func(t *testing.T) {
		t.Parallel()
		rec := httptest.NewRecorder()
		id, ok := pathID(rec, reqWithID("abc"), "widget")
		if ok || id != 0 {
			t.Fatalf("pathID = (%d, %v), want (0, false)", id, ok)
		}
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
		}
		var body errorResponse
		if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if body.Error != "invalid widget id" || body.Code != "bad_request" {
			t.Errorf("body = %+v, want {invalid widget id, bad_request}", body)
		}
	})
}

// TestTestEndpoint covers the three-arm test envelope: a nil probe passes, a
// database.ErrNotFound probe maps through writeServiceError, and any other probe
// error is reported ok:false with its message scrubbed of secret-shaped tokens.
func TestTestEndpoint(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		probeErr   error
		wantStatus int
		checkBody  func(t *testing.T, body []byte)
	}{
		{
			name:       "pass",
			probeErr:   nil,
			wantStatus: http.StatusOK,
			checkBody: func(t *testing.T, body []byte) {
				t.Helper()
				var res testResult
				if err := json.Unmarshal(body, &res); err != nil {
					t.Fatalf("decode: %v", err)
				}
				if !res.OK || res.Error != "" {
					t.Errorf("testResult = %+v, want {OK:true, Error:\"\"}", res)
				}
			},
		},
		{
			name:       "unknown resource maps through writeServiceError",
			probeErr:   database.ErrNotFound,
			wantStatus: http.StatusNotFound,
			checkBody: func(t *testing.T, body []byte) {
				t.Helper()
				var res errorResponse
				if err := json.Unmarshal(body, &res); err != nil {
					t.Fatalf("decode: %v", err)
				}
				if res.Code != "not_found" {
					t.Errorf("code = %q, want not_found", res.Code)
				}
			},
		},
		{
			name:       "other failure is scrubbed",
			probeErr:   errors.New("dial failed: apikey=sekret123"),
			wantStatus: http.StatusOK,
			checkBody: func(t *testing.T, body []byte) {
				t.Helper()
				var res testResult
				if err := json.Unmarshal(body, &res); err != nil {
					t.Fatalf("decode: %v", err)
				}
				if res.OK {
					t.Error("expected ok:false")
				}
				if res.Error == "" {
					t.Error("expected a non-empty error message")
				}
				if strings.Contains(string(body), "sekret123") {
					t.Errorf("raw secret leaked into response body: %s", body)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			rt := &router{log: zerolog.Nop()}
			rec := httptest.NewRecorder()
			r := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/x", nil)
			rt.testEndpoint(rec, r, "test widget", func(context.Context) error { return tt.probeErr })
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
			tt.checkBody(t, rec.Body.Bytes())
		})
	}
}

// TestSetResourceEnabled covers the shared enable/disable handler: success returns
// 204 and invokes set with the parsed id/enabled, a service error maps through
// writeServiceError, and a malformed id short-circuits before set is ever called.
func TestSetResourceEnabled(t *testing.T) {
	t.Parallel()
	t.Run("success", func(t *testing.T) {
		t.Parallel()
		var gotID int64
		var gotEnabled bool
		var calls int
		set := func(_ context.Context, id int64, enabled bool) error {
			calls++
			gotID, gotEnabled = id, enabled
			return nil
		}
		rt := &router{log: zerolog.Nop()}
		rec := httptest.NewRecorder()
		rt.setResourceEnabled(rec, reqWithID("42"), "widget", "set widget enabled", set, true)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusNoContent)
		}
		if calls != 1 || gotID != 42 || !gotEnabled {
			t.Errorf("set called with (%d, %v) x%d, want (42, true) x1", gotID, gotEnabled, calls)
		}
	})
	t.Run("service error maps through writeServiceError", func(t *testing.T) {
		t.Parallel()
		set := func(context.Context, int64, bool) error { return database.ErrNotFound }
		rt := &router{log: zerolog.Nop()}
		rec := httptest.NewRecorder()
		rt.setResourceEnabled(rec, reqWithID("42"), "widget", "set widget enabled", set, true)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
		}
	})
	t.Run("malformed id never calls set", func(t *testing.T) {
		t.Parallel()
		var calls int
		set := func(context.Context, int64, bool) error {
			calls++
			return nil
		}
		rt := &router{log: zerolog.Nop()}
		rec := httptest.NewRecorder()
		rt.setResourceEnabled(rec, reqWithID("abc"), "widget", "set widget enabled", set, true)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
		}
		if calls != 0 {
			t.Errorf("set called %d times, want 0", calls)
		}
	})
}

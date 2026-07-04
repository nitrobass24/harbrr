package registry

import (
	"context"
	stdhttp "net/http"
	"net/http/httptest"
	"testing"
	"time"

	apphttp "github.com/autobrr/harbrr/internal/http"
)

// TestNewDoerRedirectPolicy proves the production client honors the per-request
// redirect signal: an unstamped request keeps the stdlib follow behavior (login,
// download, native drivers), while a request stamped with WithNoRedirectFollow
// (every search-path request) gets the raw 3xx back so the engine can apply
// Jackett's followredirect / logged-out semantics itself.
func TestNewDoerRedirectPolicy(t *testing.T) {
	t.Parallel()
	mux := stdhttp.NewServeMux()
	mux.HandleFunc("/redirect", func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		stdhttp.Redirect(w, r, "/landed", stdhttp.StatusFound)
	})
	mux.HandleFunc("/landed", func(w stdhttp.ResponseWriter, _ *stdhttp.Request) {
		w.WriteHeader(stdhttp.StatusOK)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	d, err := newDoer(ClientParams{RateInterval: time.Millisecond})
	if err != nil {
		t.Fatalf("newDoer: %v", err)
	}

	get := func(ctx context.Context) *stdhttp.Response {
		req, rerr := stdhttp.NewRequestWithContext(ctx, stdhttp.MethodGet, srv.URL+"/redirect", nil)
		if rerr != nil {
			t.Fatalf("new request: %v", rerr)
		}
		resp, derr := d.Do(req)
		if derr != nil {
			t.Fatalf("Do: %v", derr)
		}
		t.Cleanup(func() { _ = resp.Body.Close() })
		return resp
	}

	if resp := get(context.Background()); resp.StatusCode != stdhttp.StatusOK {
		t.Errorf("unstamped request status = %d, want 200 (redirect followed)", resp.StatusCode)
	}
	if resp := get(apphttp.WithNoRedirectFollow(context.Background())); resp.StatusCode != stdhttp.StatusFound {
		t.Errorf("stamped request status = %d, want 302 surfaced raw", resp.StatusCode)
	}
}

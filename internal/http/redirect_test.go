package http

import (
	"context"
	stdhttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRedirectPolicy(t *testing.T) {
	t.Parallel()

	newReq := func(ctx context.Context) *stdhttp.Request {
		req, err := stdhttp.NewRequestWithContext(ctx, stdhttp.MethodGet, "https://tracker.example/", nil)
		if err != nil {
			t.Fatalf("building request: %v", err)
		}
		return req
	}

	tests := []struct {
		name    string
		stamped bool
		via     int
		wantErr error
		wantNil bool
	}{
		{name: "stamped surfaces the redirect", stamped: true, via: 1, wantErr: stdhttp.ErrUseLastResponse},
		{name: "stamped wins even deep in a chain", stamped: true, via: 15, wantErr: stdhttp.ErrUseLastResponse},
		{name: "unstamped follows", via: 1, wantNil: true},
		{name: "unstamped follows at nine hops", via: 9, wantNil: true},
		{name: "unstamped stops after ten hops", via: 10},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			if tt.stamped {
				ctx = WithNoRedirectFollow(ctx)
			}
			via := make([]*stdhttp.Request, tt.via)
			for i := range via {
				via[i] = newReq(context.Background())
			}
			err := RedirectPolicy(newReq(ctx), via)
			switch {
			case tt.wantErr != nil:
				if err != tt.wantErr { //nolint:errorlint // ErrUseLastResponse is matched by identity in net/http itself.
					t.Fatalf("RedirectPolicy() = %v, want %v", err, tt.wantErr)
				}
			case tt.wantNil:
				if err != nil {
					t.Fatalf("RedirectPolicy() = %v, want nil", err)
				}
			default:
				if err == nil {
					t.Fatal("RedirectPolicy() = nil, want hop-cap error")
				}
			}
		})
	}
}

// TestRefuseCrossHostRedirect drives the app-facing policy through a real http.Client
// against httptest servers: an authenticated (any-header) hop to another hostname is
// refused and the header never lands there; a header-less hop and a same-hostname hop
// (different port) are followed; the stdlib 10-hop cap survives the custom policy.
func TestRefuseCrossHostRedirect(t *testing.T) {
	t.Parallel()
	const keyHeader = "X-API-Key"

	tests := []struct {
		name        string
		crossHost   bool // redirect to the second server (different port, same hostname)
		spoofHost   bool // rewrite the origin's URL to another hostname for the same server
		withHeader  bool
		loop        bool // the origin redirects to itself forever (trips the hop cap)
		wantErr     bool
		wantLanded  bool // the redirect target was reached
		wantKeySent bool // ...and the api key arrived with it
	}{
		{name: "authenticated cross-host refused", crossHost: true, spoofHost: true, withHeader: true, wantErr: true},
		{name: "header-less cross-host followed", crossHost: true, spoofHost: true, wantLanded: true},
		{name: "same hostname different port followed", crossHost: true, withHeader: true, wantLanded: true, wantKeySent: true},
		{name: "same host followed", withHeader: true, wantLanded: true, wantKeySent: true},
		{name: "ten-hop cap trips", withHeader: true, loop: true, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var landed bool
			var landedKey string
			landing := func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
				landed, landedKey = true, r.Header.Get(keyHeader)
				w.WriteHeader(stdhttp.StatusOK)
			}
			other := httptest.NewServer(stdhttp.HandlerFunc(landing))
			defer other.Close()

			var origin *httptest.Server
			origin = httptest.NewServer(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
				switch {
				case r.URL.Path == "/landing":
					landing(w, r)
				case tt.loop:
					stdhttp.Redirect(w, r, origin.URL+"/loop", stdhttp.StatusFound)
				case tt.crossHost:
					stdhttp.Redirect(w, r, other.URL+"/landing", stdhttp.StatusFound)
				default:
					stdhttp.Redirect(w, r, origin.URL+"/landing", stdhttp.StatusFound)
				}
			}))
			defer origin.Close()

			// httptest binds 127.0.0.1, so both servers share a hostname. To make a real
			// cross-hostname hop, address the origin as "localhost" (resolves to the same
			// listener) so via[0].URL.Hostname() != req.URL.Hostname().
			target := origin.URL
			if tt.spoofHost {
				target = strings.Replace(target, "127.0.0.1", "localhost", 1)
			}
			req, err := stdhttp.NewRequestWithContext(context.Background(), stdhttp.MethodGet, target+"/", nil)
			if err != nil {
				t.Fatalf("building request: %v", err)
			}
			if tt.withHeader {
				req.Header.Set(keyHeader, "k_secret")
			}
			client := &stdhttp.Client{CheckRedirect: RefuseCrossHostRedirect}
			resp, err := client.Do(req)
			if resp != nil {
				resp.Body.Close()
			}
			if (err != nil) != tt.wantErr {
				t.Fatalf("Do err = %v, wantErr %v", err, tt.wantErr)
			}
			if landed != tt.wantLanded {
				t.Errorf("redirect target reached = %v, want %v", landed, tt.wantLanded)
			}
			if (landedKey != "") != tt.wantKeySent {
				t.Errorf("api key delivered to redirect target = %v, want %v", landedKey != "", tt.wantKeySent)
			}
			if err != nil && (strings.Contains(err.Error(), "k_secret") || strings.Contains(err.Error(), "?")) {
				t.Errorf("error carries more than hosts: %v", err)
			}
		})
	}
}

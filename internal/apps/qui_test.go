package apps_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/autobrr/harbrr/internal/apps"
	"github.com/autobrr/harbrr/internal/domain"
)

func TestQuiInstancesHappyPath(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	const apiKey = "qui-secret-key"
	var gotKey string
	mux := http.NewServeMux()
	mux.HandleFunc("/api/instances", func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get("X-API-Key")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]apps.QuiInstance{{ID: 1, Name: "a"}})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	svc, _ := newService(t)
	app, err := svc.Resolve(ctx, apps.Ref{Kind: domain.AppKindQui, BaseURL: srv.URL, APIKey: apiKey})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	instances, err := svc.QuiInstances(ctx, app.ID)
	if err != nil {
		t.Fatalf("QuiInstances: %v", err)
	}
	if len(instances) != 1 || instances[0].ID != 1 || instances[0].Name != "a" {
		t.Errorf("instances = %+v, want [{1 a}]", instances)
	}
	if gotKey != apiKey {
		t.Errorf("X-API-Key sent = %q, want %q", gotKey, apiKey)
	}
}

func TestQuiInstancesNonQuiAppInvalid(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc, _ := newService(t)
	app, err := svc.Resolve(ctx, apps.Ref{Kind: domain.AppKindSonarr, BaseURL: "http://sonarr:8989", APIKey: "k"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if _, err := svc.QuiInstances(ctx, app.ID); !errors.Is(err, domain.ErrInvalid) {
		t.Errorf("err = %v, want domain.ErrInvalid", err)
	}
}

// TestQuiInstancesServerErrorNoSecretLeak proves a failed proxy call's error never
// carries the app's decrypted credential — only the redacted URL may appear.
func TestQuiInstancesServerErrorNoSecretLeak(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	const apiKey = "qui-secret-key-do-not-leak"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	svc, _ := newService(t)
	app, err := svc.Resolve(ctx, apps.Ref{Kind: domain.AppKindQui, BaseURL: srv.URL, APIKey: apiKey})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	_, err = svc.QuiInstances(ctx, app.ID)
	if err == nil {
		t.Fatal("QuiInstances (500) err = nil, want an error")
	}
	if strings.Contains(err.Error(), apiKey) {
		t.Errorf("error leaks the credential: %v", err)
	}
}

// TestQuiInstancesTrailingSlashBaseURL is the regression for the bug this proxy had
// alone among the app-facing callers: it concatenated the stored base URL with the
// path without trimming, so an app saved as "http://qui:7476/" asked qui for
// "//api/instances". The shared JSON client normalises the base once.
func TestQuiInstancesTrailingSlashBaseURL(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		suffix string
	}{
		{"no trailing slash", ""},
		{"one trailing slash", "/"},
		{"repeated trailing slashes", "///"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()

			var mu sync.Mutex
			var paths []string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				paths = append(paths, r.URL.Path)
				mu.Unlock()
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode([]apps.QuiInstance{{ID: 1, Name: "a"}})
			}))
			t.Cleanup(srv.Close)

			svc, _ := newService(t)
			app, err := svc.Resolve(ctx, apps.Ref{Kind: domain.AppKindQui, BaseURL: srv.URL + tc.suffix, APIKey: "qui-secret-key"})
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}
			if _, err := svc.QuiInstances(ctx, app.ID); err != nil {
				t.Fatalf("QuiInstances: %v", err)
			}

			// Exactly one request, at the single-slash path: a double slash would
			// either 404 or cost a redirect hop, and the redirect would be the only
			// reason a second request appeared.
			mu.Lock()
			defer mu.Unlock()
			if len(paths) != 1 || paths[0] != "/api/instances" {
				t.Errorf(`qui saw %q, want exactly ["/api/instances"] (no double slash, no redirect hop)`, paths)
			}
		})
	}
}

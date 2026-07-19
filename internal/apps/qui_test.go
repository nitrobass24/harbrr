package apps_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
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

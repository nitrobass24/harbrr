package api_test

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/getkin/kin-openapi/openapi3"
	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog"

	"github.com/autobrr/harbrr/internal/announce"
	"github.com/autobrr/harbrr/internal/appsync"
	"github.com/autobrr/harbrr/internal/auth"
	"github.com/autobrr/harbrr/internal/database"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
	"github.com/autobrr/harbrr/internal/indexer/registry"
	"github.com/autobrr/harbrr/internal/notify"
	"github.com/autobrr/harbrr/internal/secrets"
	"github.com/autobrr/harbrr/internal/web/api"
	"github.com/autobrr/harbrr/internal/web/swagger"
	"github.com/autobrr/harbrr/internal/web/torznabhttp"
)

// testKey is a synthetic 32-byte AES key (tests only).
const testKey = "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20"

// defYAML is a minimal definition written to a drop-in dir so indexer-CRUD tests
// have a definition to configure.
const defYAML = `---
id: testtracker
name: Test Tracker
description: API test fixture
language: en-US
type: private
encoding: UTF-8
links:
  - https://html.invalid/
caps:
  categorymappings:
    - {id: 1, cat: Movies}
  modes:
    search: [q]
settings:
  - name: apikey
    type: text
    label: API Key
search:
  path: /browse.php
  inputs:
    q: "{{ .Keywords }}"
  rows:
    selector: table.results > tbody > tr
  fields:
    title:
      selector: a.title
    download:
      selector: a.dl
      attribute: href
    category:
      selector: td.cat
      attribute: data-cat
    size:
      selector: td.size
    seeders:
      selector: td.seeders
    leechers:
      selector: td.leechers
`

// env bundles a built router with the collaborators tests drive directly.
type env struct {
	handler  http.Handler
	auth     *auth.Service
	registry *registry.Registry
	sessions *scs.SessionManager
	db       *database.DB
	source   *fakeAppSource
}

// newEnv builds the management API over an in-memory database with a fixed clock,
// the vendored loader, and a session manager. cfg sets the auth posture.
func newEnv(t *testing.T, cfg api.Config) *env {
	return newEnvWithCache(t, cfg, nil)
}

// newEnvWithCache is newEnv with an optional search-results cache wired into Deps.
// buildCache (when non-nil) is handed the env's database so the cache is backed by
// the same store the handlers read; a nil builder means caching is off (the
// /api/cache routes then report a disabled state).
func newEnvWithCache(t *testing.T, cfg api.Config, buildCache func(db *database.DB) *registry.SearchCache) *env {
	t.Helper()

	db, err := database.Open(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	keyring, err := secrets.OpenKeyring(secrets.KeyringOptions{EncryptionKey: testKey}, zerolog.Nop())
	if err != nil {
		t.Fatalf("keyring: %v", err)
	}

	dropin := t.TempDir()
	if err := os.WriteFile(filepath.Join(dropin, "testtracker.yml"), []byte(defYAML), 0o600); err != nil {
		t.Fatalf("write def: %v", err)
	}
	ldr := loader.New(dropin)

	// Cookie hardening mirrors production (Box 5): the CSRF posture is SameSite=Lax
	// + HttpOnly, no token (qui model).
	sm := scs.New()
	sm.Store = database.NewSessionStore(db)
	sm.Cookie.Name = "harbrr_session"
	sm.Cookie.HttpOnly = true
	sm.Cookie.SameSite = http.SameSiteLaxMode
	sm.Cookie.Persist = false
	sm.Lifetime = time.Hour

	authSvc := auth.NewService(db)
	reg := registry.New(db, ldr, keyring)
	source := &fakeAppSource{}
	appSync := appsync.NewService(db, source, authSvc, keyring, http.DefaultClient, zerolog.Nop())
	announceSvc := announce.NewService(db, authSvc, keyring,
		announce.DefaultTargetFactory(http.DefaultClient, nil, nil), zerolog.Nop())
	notifySvc := notify.NewService(db, keyring, http.DefaultClient, zerolog.Nop())

	var cache *registry.SearchCache
	if buildCache != nil {
		cache = buildCache(db)
	}

	handler, err := api.NewRouter(api.Deps{
		Auth: authSvc, Registry: reg, Loader: ldr, AppSync: appSync, Announce: announceSvc,
		Notify: notifySvc, Sessions: sm,
		Cache: cache, Logger: zerolog.Nop(), LogLevel: api.NewLogLevelStore(db, nil),
	}, cfg)
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}
	return &env{handler: handler, auth: authSvc, registry: reg, sessions: sm, db: db, source: source}
}

// TestOpenAPIDriftRoutesMatchSpec asserts the mounted routes and the embedded
// OpenAPI spec describe exactly the same set of (method, path) operations — so the
// spec cannot drift from the handlers. It covers both the management chi router and
// the *arr-facing Torznab feed, which is mounted on a separate ServeMux at the
// server level and so is not reachable via chi.Walk.
func TestOpenAPIDriftRoutesMatchSpec(t *testing.T) {
	t.Parallel()

	e := newEnv(t, api.Config{})
	routes := walkRoutes(t, e.handler)
	// The Torznab feed handler mounts its routes on a separate ServeMux, so fold them
	// in from the package's own route table (its single source of truth) — otherwise
	// the spec's documented feed endpoints would look like undocumented drift.
	for _, rt := range torznabhttp.Routes() {
		routes[rt.Method+" "+rt.Path] = struct{}{}
	}
	spec := specOperations(t)

	for op := range routes {
		if _, ok := spec[op]; !ok {
			t.Errorf("route %q is not documented in openapi.yaml", op)
		}
	}
	for op := range spec {
		if _, ok := routes[op]; !ok {
			t.Errorf("openapi.yaml documents %q but no route is mounted", op)
		}
	}
}

// walkRoutes collects "METHOD /path" for every mounted route.
func walkRoutes(t *testing.T, h http.Handler) map[string]struct{} {
	t.Helper()
	routes, ok := h.(chi.Routes)
	if !ok {
		t.Fatal("handler is not a chi.Routes")
	}
	out := map[string]struct{}{}
	err := chi.Walk(routes, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		out[method+" "+route] = struct{}{}
		return nil
	})
	if err != nil {
		t.Fatalf("walk routes: %v", err)
	}
	return out
}

// specOperations collects "METHOD /path" for every operation in the spec.
func specOperations(t *testing.T) map[string]struct{} {
	t.Helper()
	doc, err := openapi3.NewLoader().LoadFromData(swagger.Spec())
	if err != nil {
		t.Fatalf("load spec: %v", err)
	}
	out := map[string]struct{}{}
	for path, item := range doc.Paths.Map() {
		for method, op := range item.Operations() {
			if op == nil {
				continue
			}
			out[strings.ToUpper(method)+" "+path] = struct{}{}
		}
	}
	return out
}

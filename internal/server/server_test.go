package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	stdhttp "net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/alexedwards/scs/v2"
	"github.com/rs/zerolog"

	"github.com/autobrr/harbrr/internal/auth"
	"github.com/autobrr/harbrr/internal/database"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/indexer/registry"
	"github.com/autobrr/harbrr/internal/secrets"
	"github.com/autobrr/harbrr/internal/server"
	"github.com/autobrr/harbrr/internal/web/api"
	"github.com/autobrr/harbrr/internal/web/swagger"
	"github.com/autobrr/harbrr/internal/web/torznab"
)

const testKey = "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20"

const defYAML = `---
id: testtracker
name: Test Tracker
description: server e2e fixture
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
    filters:
      - name: andmatch
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

const bodyHTML = `<!DOCTYPE html><html><body>
<table class="results"><tbody>
<tr><td class="cat" data-cat="1"></td>
<td><a class="title" href="/d?id=1">Big Buck Bunny 1080p</a></td>
<td><a class="dl" href="/dl?id=1&amp;passkey=NOTREALSECRET00">dl</a></td>
<td class="size">2.5 GB</td><td class="seeders">42</td><td class="leechers">7</td></tr>
</tbody></table></body></html>`

// replayDoer serves a fixed body; no network.
type replayDoer struct {
	body string
	mu   sync.Mutex
	hits int
}

func (d *replayDoer) Do(req *stdhttp.Request) (*stdhttp.Response, error) {
	d.mu.Lock()
	d.hits++
	d.mu.Unlock()
	return &stdhttp.Response{
		StatusCode: stdhttp.StatusOK,
		Header:     stdhttp.Header{},
		Body:       io.NopCloser(strings.NewReader(d.body)),
		Request:    req,
	}, nil
}

// stack bundles the running server and the bits a test inspects.
type stack struct {
	url string
	db  *database.DB
}

// buildStack assembles the full server (DB, keyring, sessions, auth, registry with
// a replay Doer, management API, Torznab) under basePath and starts it.
func buildStack(t *testing.T, basePath string) (*stack, *stdhttp.Client) {
	t.Helper()

	db, err := database.Open(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	dropin := t.TempDir()
	if err := os.WriteFile(filepath.Join(dropin, "testtracker.yml"), []byte(defYAML), 0o600); err != nil {
		t.Fatalf("write def: %v", err)
	}

	keyring, err := secrets.OpenKeyring(secrets.KeyringOptions{EncryptionKey: testKey}, zerolog.Nop())
	if err != nil {
		t.Fatalf("keyring: %v", err)
	}

	sm := scs.New()
	sm.Store = database.NewSessionStore(db)
	sm.Cookie.Name = "harbrr_session"
	sm.Cookie.HttpOnly = true
	sm.Cookie.SameSite = stdhttp.SameSiteLaxMode

	authSvc := auth.NewService(db)
	doer := &replayDoer{body: bodyHTML}
	reg := registry.New(db, loader.New(dropin), keyring,
		registry.WithDoerFactory(func(registry.ClientParams) (search.Doer, error) { return doer, nil }))

	mgmt, err := api.NewRouter(api.Deps{
		Auth: authSvc, Registry: reg, Loader: loader.New(dropin), Sessions: sm, Logger: zerolog.Nop(),
	}, api.Config{})
	if err != nil {
		t.Fatalf("api router: %v", err)
	}
	tz := torznab.NewHandler(
		reg,
		torznab.WithAPIKeyValidator(func(k string) bool {
			_, err := authSvc.ValidateAPIKey(context.Background(), k)
			return err == nil
		}),
		torznab.WithBasePath(basePath),
	)

	srv := server.New(server.Deps{Management: mgmt, Torznab: tz, Spec: swagger.Spec(), DocsUI: swagger.UI(), Logger: zerolog.Nop()},
		server.Config{BasePath: basePath})

	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookie jar: %v", err)
	}
	return &stack{url: ts.URL + basePath, db: db}, &stdhttp.Client{Jar: jar}
}

// TestServerEndToEnd proves the full offline path: boot -> setup -> login -> mint
// apikey -> add indexer (creds encrypted) -> Torznab caps + search over the replay
// Doer. The registry IS the production Provider.
func TestServerEndToEnd(t *testing.T) {
	t.Parallel()
	runEndToEnd(t, "")
}

// TestServerEndToEndBasePath runs the same flow under a base path and asserts the
// served feed self URL includes the base path.
func TestServerEndToEndBasePath(t *testing.T) {
	t.Parallel()
	runEndToEnd(t, "/harbrr")
}

func runEndToEnd(t *testing.T, basePath string) {
	s, c := buildStack(t, basePath)

	// healthz + spec + docs UI are public.
	mustGet(t, c, s.url+"/healthz", stdhttp.StatusOK)
	mustGet(t, c, s.url+"/api/openapi.yaml", stdhttp.StatusOK)
	if docs := mustGet(t, c, s.url+"/api/docs", stdhttp.StatusOK); !strings.Contains(docs, "swagger-ui") {
		t.Errorf("/api/docs did not serve the Swagger UI page")
	}

	// First-run setup + login.
	mustJSON(t, c, stdhttp.MethodPost, s.url+"/api/auth/setup",
		map[string]string{"username": "admin", "password": "correct-horse-staple"}, stdhttp.StatusCreated)
	mustJSON(t, c, stdhttp.MethodPost, s.url+"/api/auth/login",
		map[string]string{"username": "admin", "password": "correct-horse-staple"}, stdhttp.StatusNoContent)

	// Mint a Torznab API key.
	_, body := mustJSON(t, c, stdhttp.MethodPost, s.url+"/api/apikeys",
		map[string]string{"name": "sonarr"}, stdhttp.StatusCreated)
	var minted struct {
		Key string `json:"key"`
	}
	if err := json.Unmarshal(body, &minted); err != nil || minted.Key == "" {
		t.Fatalf("mint key: %v (%s)", err, body)
	}

	// Add an indexer with a secret setting.
	mustJSON(t, c, stdhttp.MethodPost, s.url+"/api/indexers", map[string]any{
		"slug": "tt", "definitionId": "testtracker",
		"settings": map[string]string{"apikey": "TRACKER-SECRET-XYZ"},
	}, stdhttp.StatusCreated)

	// The credential is encrypted at rest (not the plaintext).
	assertEncryptedAtRest(t, s.db, "TRACKER-SECRET-XYZ")

	// Torznab caps via the DB-resolved registry (apikey-authenticated).
	feed := s.url + "/api/v2.0/indexers/tt/results/torznab"
	caps := mustGet(t, c, feed+"?t=caps&apikey="+minted.Key, stdhttp.StatusOK)
	if !strings.Contains(caps, "<caps") {
		t.Errorf("caps response not a caps document: %s", caps)
	}

	// A wrong apikey is rejected (error document, still HTTP 200 per Torznab).
	bad := mustGet(t, c, feed+"?t=caps&apikey=wrong", stdhttp.StatusOK)
	if !strings.Contains(bad, "error") {
		t.Errorf("wrong apikey did not yield an error document: %s", bad)
	}

	// Torznab search over the replay Doer returns the release.
	results := mustGet(t, c, feed+"/api?t=search&q=bunny&apikey="+minted.Key, stdhttp.StatusOK)
	if !strings.Contains(results, "Big Buck Bunny 1080p") {
		t.Errorf("search did not return the release: %s", results)
	}
	// The feed self URL reflects the external base path.
	wantSelf := "/api/v2.0/indexers/tt/results/torznab"
	if basePath != "" {
		wantSelf = basePath + wantSelf
	}
	if !strings.Contains(results, wantSelf) {
		t.Errorf("search self URL missing %q: %s", wantSelf, results)
	}
}

// assertEncryptedAtRest fails if the plaintext appears in the stored setting.
func assertEncryptedAtRest(t *testing.T, db *database.DB, plaintext string) {
	t.Helper()
	var enc, val *string
	var isSecret int
	err := db.QueryRowContext(context.Background(),
		`SELECT value, value_encrypted, is_secret FROM indexer_settings WHERE name='apikey'`).
		Scan(&val, &enc, &isSecret)
	if err != nil {
		t.Fatalf("read setting: %v", err)
	}
	if isSecret != 1 {
		t.Error("apikey not marked secret")
	}
	if enc == nil || *enc == "" || strings.Contains(*enc, plaintext) {
		t.Errorf("apikey not encrypted at rest: enc=%v", enc)
	}
	if val != nil && strings.Contains(*val, plaintext) {
		t.Error("apikey plaintext stored in the value column")
	}
}

func mustGet(t *testing.T, c *stdhttp.Client, url string, want int) string {
	t.Helper()
	req, err := stdhttp.NewRequestWithContext(context.Background(), stdhttp.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("new request %s: %v", url, err)
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body %s: %v", url, err)
	}
	if resp.StatusCode != want {
		t.Fatalf("GET %s: status %d, want %d (%s)", url, resp.StatusCode, want, body)
	}
	return string(body)
}

func mustJSON(t *testing.T, c *stdhttp.Client, method, url string, payload any, want int) (*stdhttp.Response, []byte) {
	t.Helper()
	b, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal %s: %v", url, err)
	}
	req, err := stdhttp.NewRequestWithContext(context.Background(), method, url, bytes.NewReader(b))
	if err != nil {
		t.Fatalf("new request %s: %v", url, err)
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body %s: %v", url, err)
	}
	if resp.StatusCode != want {
		t.Fatalf("%s %s: status %d, want %d (%s)", method, url, resp.StatusCode, want, body)
	}
	return resp, body
}

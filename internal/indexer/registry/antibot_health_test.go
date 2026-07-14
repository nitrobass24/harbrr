package registry_test

import (
	"context"
	"errors"
	"io"
	stdhttp "net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rs/zerolog"

	"github.com/autobrr/harbrr/internal/database"
	"github.com/autobrr/harbrr/internal/domain"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/login"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/indexer/registry"
	"github.com/autobrr/harbrr/internal/secrets"
)

// cfLoginDef is a form-login definition whose landing page (served by cfDoer as a
// Cloudflare interstitial) trips anti-bot detection during login.
const cfLoginDef = `---
id: cftracker
name: CF Tracker
description: anti-bot integration fixture
language: en-US
type: private
encoding: UTF-8
links:
  - https://cf.invalid/
caps:
  categorymappings:
    - {id: 1, cat: Movies}
  modes:
    search: [q]
settings:
  - name: username
    type: text
    label: Username
login:
  path: /login.php
  method: form
  form: form#login
  inputs:
    username: "{{ .Config.username }}"
search:
  paths:
    - path: /browse.php
  inputs:
    q: "{{ .Keywords }}"
  rows:
    selector: tr.torrent
  fields:
    title:
      selector: a.name
    download:
      selector: a.dl
      attribute: href
    size:
      selector: td.size
    seeders:
      selector: td.seeders
    leechers:
      selector: td.leechers
    category:
      text: "1"
`

// cfDoer serves a Cloudflare "Just a moment..." interstitial for every request.
type cfDoer struct{}

func (cfDoer) Do(req *stdhttp.Request) (*stdhttp.Response, error) {
	return &stdhttp.Response{
		StatusCode: stdhttp.StatusOK,
		Header:     stdhttp.Header{},
		Body:       io.NopCloser(strings.NewReader("Just a moment...")),
		Request:    req,
	}, nil
}

// TestSearchRecordsAntiBotHealthEvent pins the end-to-end anti_bot path: a login
// landing page that is a Cloudflare challenge, with no solver configured, makes the
// engine fail with ErrSolverRequired, which the adapter classifies + records as an
// anti_bot health event surfaced by Status. (A configured FlareSolverr solver that
// FAILS takes the identical ErrSolverRequired path.)
func TestSearchRecordsAntiBotHealthEvent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	db, err := database.Open(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	dropin := t.TempDir()
	if err := os.WriteFile(filepath.Join(dropin, "cftracker.yml"), []byte(cfLoginDef), 0o600); err != nil {
		t.Fatalf("write def: %v", err)
	}
	kr, err := secrets.OpenKeyring(secrets.KeyringOptions{EncryptionKey: testHexKey}, zerolog.Nop())
	if err != nil {
		t.Fatalf("keyring: %v", err)
	}
	reg := registry.New(db, loader.New(dropin), kr, nil,
		registry.WithDoerFactory(func(registry.ClientParams) (search.Doer, error) { return cfDoer{}, nil }))

	if _, err := reg.Add(ctx, registry.AddParams{
		Slug: "cf", DefinitionID: "cftracker", Settings: map[string]string{"username": "u"},
	}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	idx, ok := reg.Indexer(ctx, "cf")
	if !ok {
		t.Fatal("Indexer(cf) not resolved")
	}
	if _, err := idx.Search(ctx, search.Query{Keywords: "x"}); !errors.Is(err, login.ErrSolverRequired) {
		t.Fatalf("Search err = %v, want ErrSolverRequired (anti-bot, no solver)", err)
	}

	st, err := reg.Status(ctx, "cf")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if len(st.Events) != 1 || st.Events[0].Kind != domain.HealthAntiBot {
		t.Fatalf("events = %+v, want exactly one anti_bot", st.Events)
	}
}

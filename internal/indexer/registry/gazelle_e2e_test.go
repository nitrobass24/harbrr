package registry_test

import (
	"context"
	"io"
	stdhttp "net/http"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/indexer/registry"
)

// gazelleAPIKey is a synthetic API key for this e2e test only. It rides exclusively
// in the Authorization header (RED bare, OPS "token "-prefixed) and must never appear
// in any request URL — the header-auth model is the whole point of the driver.
const gazelleAPIKey = "GAZELLE-E2E-SYNTHETIC-APIKEY"

// gazelleDoer fronts a Gazelle native driver as the registry wires it: a GET to
// ajax.php?action=browse returns the saved browse golden. Every request is recorded
// (URL + Authorization header) so the test can assert the per-site auth header is
// correct and the apikey never leaks into a URL.
type gazelleDoer struct {
	browseBody string

	mu   sync.Mutex
	reqs []recordedReq
}

type recordedReq struct {
	url  string
	auth string
}

func (d *gazelleDoer) Do(req *stdhttp.Request) (*stdhttp.Response, error) {
	d.mu.Lock()
	d.reqs = append(d.reqs, recordedReq{url: req.URL.String(), auth: req.Header.Get("Authorization")})
	d.mu.Unlock()

	return &stdhttp.Response{
		StatusCode: stdhttp.StatusOK,
		Header:     stdhttp.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(d.browseBody)),
	}, nil
}

func (d *gazelleDoer) records() []recordedReq {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]recordedReq, len(d.reqs))
	copy(out, d.reqs)
	return out
}

// TestGazelleEndToEnd builds BOTH the redacted and orpheus native drivers through the
// registry (Add -> resolve), runs a music search against the saved browse golden, and
// asserts: releases come back, the per-site Authorization header is correct (RED bare,
// OPS "token "-prefixed), and the apikey never appears in any recorded request URL (it
// rides only in the header, which is never logged).
func TestGazelleEndToEnd(t *testing.T) {
	golden, err := os.ReadFile("../native/gazelle/testdata/browse_music.json")
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}

	cases := []struct {
		slug     string
		defID    string
		wantAuth string
	}{
		{slug: "red", defID: "redacted", wantAuth: gazelleAPIKey},
		{slug: "ops", defID: "orpheus", wantAuth: "token " + gazelleAPIKey},
	}

	for _, tc := range cases {
		t.Run(tc.defID, func(t *testing.T) {
			doer := &gazelleDoer{browseBody: string(golden)}
			reg, _ := newRegistry(t, doer)
			ctx := context.Background()

			if _, err := reg.Add(ctx, registry.AddParams{
				Slug:         tc.slug,
				DefinitionID: tc.defID,
				Settings:     map[string]string{"apikey": gazelleAPIKey},
			}); err != nil {
				t.Fatalf("Add(%s): %v", tc.defID, err)
			}

			idx, ok := reg.Indexer(ctx, tc.slug)
			if !ok {
				t.Fatalf("%s indexer should resolve", tc.defID)
			}
			if idx.NeedsResolver() {
				t.Error("NeedsResolver = true, want false (no passkey in URL; header auth at grab time)")
			}
			if idx.Capabilities().Modes["music-search"] == nil {
				t.Error("native caps missing music-search mode")
			}

			releases, err := idx.Search(ctx, search.Query{Keywords: "fear not"})
			if err != nil {
				t.Fatalf("Search: %v", err)
			}
			// Three torrents in the music golden flatten to three releases,
			// ordered by PublishDate descending (mirroring Prowlarr): the 2021
			// remaster sorts ahead of the two 2012 torrents.
			if len(releases) != 3 {
				t.Fatalf("releases = %d, want 3", len(releases))
			}
			wantTitles := []string{
				"Logistics - Fear Not (2012) [Album] [Remaster 2021] [FLAC 24bit Lossless / Vinyl]",
				"Logistics - Fear Not (2012) [Album] [MP3 320 / WEB]",
				"Logistics - Fear Not (2012) [Album] [FLAC Lossless / CD / Log (100%) / Cue]",
			}
			for i, want := range wantTitles {
				if releases[i].Title != want {
					t.Errorf("releases[%d].Title = %q, want %q", i, releases[i].Title, want)
				}
			}

			recs := doer.records()
			if len(recs) == 0 {
				t.Fatal("no requests recorded")
			}
			for _, r := range recs {
				if r.auth != tc.wantAuth {
					t.Errorf("Authorization = %q, want %q", r.auth, tc.wantAuth)
				}
				if strings.Contains(r.url, gazelleAPIKey) {
					t.Errorf("apikey leaked into request URL: %q", r.url)
				}
			}
		})
	}
}

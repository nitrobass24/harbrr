package api_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/indexer/registry"
	"github.com/autobrr/harbrr/internal/web/api"
	"github.com/autobrr/harbrr/internal/web/torznabhttp"
)

// adultDefYAML is a drop-in definition advertising one ordinary and one XXX
// category, both with a desc so the mapper also synthesises the custom (1:1)
// categories at 100001/100006 — the "custom category carrying the tracker's own
// adult label" case the hide-adult-categories setting has to cover.
const adultDefYAML = `---
id: adulttracker
name: Adult Test Tracker
description: hide-adult-categories fixture
language: en-US
type: private
encoding: UTF-8
links:
  - https://adult.invalid/
caps:
  categorymappings:
    - {id: 1, cat: Movies, desc: "Films"}
    - {id: 6, cat: XXX, desc: "Adult Films"}
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

// adultBodyHTML is the replayed results page: one Movies row (tracker cat 1) and
// one XXX row (tracker cat 6).
const adultBodyHTML = `<!DOCTYPE html><html><body>
<table class="results"><tbody>
<tr><td class="cat" data-cat="1"></td>
<td><a class="title" href="/d?id=1">Ordinary Release 1080p</a></td>
<td><a class="dl" href="/dl?id=1">dl</a></td>
<td class="size">2.5 GB</td><td class="seeders">42</td><td class="leechers">7</td></tr>
<tr><td class="cat" data-cat="6"></td>
<td><a class="title" href="/d?id=2">Adult Release 1080p</a></td>
<td><a class="dl" href="/dl?id=2">dl</a></td>
<td class="size">1.5 GB</td><td class="seeders">9</td><td class="leechers">1</td></tr>
</tbody></table></body></html>`

// staticDoer replays a fixed HTML body for every request; no network.
type staticDoer struct{ body string }

func (d staticDoer) Do(req *http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader(d.body)),
		Request:    req,
	}, nil
}

type adultCategoriesBody struct {
	Hidden bool `json:"hidden"`
}

// adultEnv builds an auth-disabled env whose registry replays adultBodyHTML, with
// the adulttracker indexer configured.
func adultEnv(t *testing.T) (*env, string, *http.Client) {
	t.Helper()
	e := newEnvWithCache(t, api.Config{
		AuthDisabled: true,
		IPAllowlist:  []string{"127.0.0.0/8", "::1/128"},
	}, nil, registry.WithDoerFactory(func(registry.ClientParams) (search.Doer, error) {
		return staticDoer{body: adultBodyHTML}, nil
	}))
	if _, err := e.registry.Add(context.Background(), registry.AddParams{
		Slug: "adult", DefinitionID: "adulttracker", Settings: map[string]string{"apikey": "k"},
	}); err != nil {
		t.Fatalf("Add(adulttracker): %v", err)
	}
	base, c := serve(t, e)
	return e, base, c
}

// setHidden PUTs the global setting and asserts the echoed value.
func setHidden(t *testing.T, base string, c *http.Client, hidden bool) {
	t.Helper()
	resp, body := do(t, c, http.MethodPut, base+"/api/config/adult-categories", adultCategoriesBody{Hidden: hidden}, nil)
	mustStatus(t, resp, body, http.StatusOK)
	var got adultCategoriesBody
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode put: %v (%s)", err, body)
	}
	if got.Hidden != hidden {
		t.Fatalf("PUT hidden = %v, want %v", got.Hidden, hidden)
	}
}

// catIDs returns the category ids in a capabilities-shaped JSON body.
func catIDs(t *testing.T, body []byte, path string) []int {
	t.Helper()
	var full struct {
		Categories []struct {
			ID int `json:"id"`
		} `json:"categories"`
		Caps struct {
			Categories []struct {
				ID int `json:"id"`
			} `json:"categories"`
		} `json:"caps"`
	}
	if err := json.Unmarshal(body, &full); err != nil {
		t.Fatalf("decode %s: %v (%s)", path, err, body)
	}
	src := full.Categories
	if len(src) == 0 {
		src = full.Caps.Categories
	}
	out := make([]int, 0, len(src))
	for _, c := range src {
		out = append(out, c.ID)
	}
	return out
}

func hasCat(ids []int, want int) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}

// TestAdultCategoriesSettingRoundTrip covers the knob itself: absent = off, PUT
// persists and is echoed by GET, and toggling back restores the default.
func TestAdultCategoriesSettingRoundTrip(t *testing.T) {
	t.Parallel()
	_, base, c := adultEnv(t)

	resp, body := do(t, c, http.MethodGet, base+"/api/config/adult-categories", nil, nil)
	mustStatus(t, resp, body, http.StatusOK)
	var got adultCategoriesBody
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode get: %v (%s)", err, body)
	}
	if got.Hidden {
		t.Fatal("with no stored row the setting must be off")
	}

	setHidden(t, base, c, true)
	resp, body = do(t, c, http.MethodGet, base+"/api/config/adult-categories", nil, nil)
	mustStatus(t, resp, body, http.StatusOK)
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode get: %v (%s)", err, body)
	}
	if !got.Hidden {
		t.Error("GET after PUT true = false, want true")
	}

	setHidden(t, base, c, false)
}

// TestAdultCategoriesHiddenFromCategorySurfaces walks both category-listing
// surfaces (a configured indexer's capabilities and a definition's caps block)
// and proves: off = unchanged, on = the XXX family AND the custom category
// carrying the tracker's adult label are gone while ordinary categories stay,
// and turning it back off restores the original response byte-for-byte.
func TestAdultCategoriesHiddenFromCategorySurfaces(t *testing.T) {
	t.Parallel()
	_, base, c := adultEnv(t)

	for _, path := range []string{"/api/indexers/adult/capabilities", "/api/definitions/adulttracker"} {
		t.Run(path, func(t *testing.T) {
			setHidden(t, base, c, false)
			resp, before := do(t, c, http.MethodGet, base+path, nil, nil)
			mustStatus(t, resp, before, http.StatusOK)
			ids := catIDs(t, before, path)
			for _, want := range []int{2000, 6000, 100001, 100006} {
				if !hasCat(ids, want) {
					t.Fatalf("setting off: category %d missing from %v", want, ids)
				}
			}

			setHidden(t, base, c, true)
			resp, body := do(t, c, http.MethodGet, base+path, nil, nil)
			mustStatus(t, resp, body, http.StatusOK)
			ids = catIDs(t, body, path)
			for _, gone := range []int{6000, 100006} {
				if hasCat(ids, gone) {
					t.Errorf("setting on: adult category %d still advertised in %v", gone, ids)
				}
			}
			for _, kept := range []int{2000, 100001} {
				if !hasCat(ids, kept) {
					t.Errorf("setting on: non-adult category %d was dropped from %v", kept, ids)
				}
			}

			setHidden(t, base, c, false)
			resp, after := do(t, c, http.MethodGet, base+path, nil, nil)
			mustStatus(t, resp, after, http.StatusOK)
			if string(after) != string(before) {
				t.Errorf("setting off again is not byte-identical:\n before = %s\n after  = %s", before, after)
			}
		})
	}
}

// searchTitles runs the JSON search and returns the result titles plus the
// reported total.
func searchTitles(t *testing.T, base string, c *http.Client, query string) ([]string, int) {
	t.Helper()
	resp, body := do(t, c, http.MethodGet, base+"/api/indexers/adult/search?"+query, nil, nil)
	mustStatus(t, resp, body, http.StatusOK)
	var got struct {
		Results []struct {
			Title string `json:"title"`
		} `json:"results"`
		Total int `json:"total"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode search: %v (%s)", err, body)
	}
	titles := make([]string, 0, len(got.Results))
	for _, r := range got.Results {
		titles = append(titles, r.Title)
	}
	return titles, got.Total
}

func containsTitle(titles []string, want string) bool {
	for _, ti := range titles {
		if strings.Contains(ti, want) {
			return true
		}
	}
	return false
}

// TestAdultCategoriesSearchFiltering pins the search half of the acceptance
// criteria: with the setting on, a search that names no categories drops
// XXX-category results (and reports a total that matches), while a search that
// explicitly asks for a category — including the XXX one — is honored unchanged.
func TestAdultCategoriesSearchFiltering(t *testing.T) {
	t.Parallel()
	_, base, c := adultEnv(t)

	tests := []struct {
		name       string
		hidden     bool
		query      string
		wantAdult  bool
		wantNormal bool
	}{
		{name: "setting off, no cat", hidden: false, query: "q=release", wantAdult: true, wantNormal: true},
		{name: "setting on, no cat", hidden: true, query: "q=release", wantAdult: false, wantNormal: true},
		{name: "setting on, explicit XXX cat", hidden: true, query: "q=release&cat=6000", wantAdult: true, wantNormal: false},
		{name: "setting on, explicit movie cat", hidden: true, query: "q=release&cat=2000", wantAdult: false, wantNormal: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setHidden(t, base, c, tt.hidden)
			titles, total := searchTitles(t, base, c, tt.query)
			if got := containsTitle(titles, "Adult Release"); got != tt.wantAdult {
				t.Errorf("adult result present = %v, want %v (titles %v)", got, tt.wantAdult, titles)
			}
			if got := containsTitle(titles, "Ordinary Release"); got != tt.wantNormal {
				t.Errorf("ordinary result present = %v, want %v (titles %v)", got, tt.wantNormal, titles)
			}
			if total != len(titles) {
				t.Errorf("total = %d, want %d (the served, post-filter count)", total, len(titles))
			}
		})
	}
}

// TestAdultCategoriesFeedUntouched pins the deliberate non-goal: the *arr-facing
// Torznab feed is NOT filtered by this setting. Feed consumers are automation
// carrying their own category selection; the setting is an operator-UI feature.
// Changing this is a product decision, not a refactor — hence the test.
func TestAdultCategoriesFeedUntouched(t *testing.T) {
	t.Parallel()
	e, base, c := adultEnv(t)
	setHidden(t, base, c, true)

	// Sanity: the management search really is filtered in this env.
	if titles, _ := searchTitles(t, base, c, "q=release"); containsTitle(titles, "Adult Release") {
		t.Fatalf("management search should be filtered here, got %v", titles)
	}

	feed := httptest.NewServer(torznabhttp.NewHandler(e.registry,
		torznabhttp.WithAPIKeyValidator(func(string) bool { return true })))
	t.Cleanup(feed.Close)

	resp, body := do(t, c, http.MethodGet,
		feed.URL+"/api/indexers/adult/results/torznab/api?t=search&q=release&apikey=k", nil, nil)
	mustStatus(t, resp, body, http.StatusOK)
	xml := string(body)
	if !strings.Contains(xml, "Adult Release") {
		t.Errorf("the feed must still serve XXX results with the setting on:\n%s", xml)
	}
	if !strings.Contains(xml, "Ordinary Release") {
		t.Errorf("the feed lost its ordinary results:\n%s", xml)
	}
	// The feed's caps document keeps the XXX taxonomy too.
	resp, body = do(t, c, http.MethodGet,
		feed.URL+"/api/indexers/adult/results/torznab/api?t=caps&apikey=k", nil, nil)
	mustStatus(t, resp, body, http.StatusOK)
	if !strings.Contains(string(body), `id="6000"`) {
		t.Errorf("the feed caps must still advertise the XXX family:\n%s", body)
	}
}

// TestAdultHidingKeepsAggregateLedgerConsistent pins the invariant the aggregate search
// surface exists for: when adult hiding drops releases from the merged window, each
// member's ledger Count must still equal the releases actually served for it. A ledger
// row claiming more than the page shows is the page disagreeing with itself — the exact
// failure the server-merged design removes (review finding on autobrr/harbrr#372).
func TestAdultHidingKeepsAggregateLedgerConsistent(t *testing.T) {
	t.Parallel()
	_, base, c := adultEnv(t)
	setHidden(t, base, c, true)

	resp, body := do(t, c, http.MethodGet, base+"/api/search?indexers=adult&q=release", nil, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", resp.StatusCode, body)
	}
	var got struct {
		Results []struct {
			Indexer string `json:"indexer"`
		} `json:"results"`
		Members []struct {
			Slug  string `json:"slug"`
			Count int    `json:"count"`
		} `json:"members"`
		Total int `json:"total"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	served := map[string]int{}
	for _, r := range got.Results {
		served[r.Indexer]++
	}
	for _, m := range got.Members {
		if m.Count != served[m.Slug] {
			t.Errorf("member %q ledger Count = %d, but %d releases served for it", m.Slug, m.Count, served[m.Slug])
		}
	}
	if got.Total != len(got.Results) {
		t.Errorf("total = %d, want %d (the served window)", got.Total, len(got.Results))
	}
}

package api_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/indexer/registry"
	"github.com/autobrr/harbrr/internal/web/api"
)

// perHostDoer replays a results page chosen by the request host, so two instances of
// the same definition return DISTINGUISHABLE releases and the merge can be checked.
type perHostDoer struct{ bodies map[string]string }

func (d perHostDoer) Do(req *http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader(d.bodies[req.URL.Host])),
		Request:    req,
	}, nil
}

// rowsHTML builds a results page carrying one row per title.
func rowsHTML(titles ...string) string {
	var b strings.Builder
	b.WriteString(`<!DOCTYPE html><html><body><table class="results"><tbody>`)
	for i, ti := range titles {
		b.WriteString(`<tr><td class="cat" data-cat="1"></td>` +
			`<td><a class="title" href="/d">` + ti + `</a></td>` +
			`<td><a class="dl" href="/dl?id=` + strconv.Itoa(i) + `">dl</a></td>` +
			`<td class="size">1 GB</td><td class="seeders">3</td><td class="leechers">1</td></tr>`)
	}
	b.WriteString(`</tbody></table></body></html>`)
	return b.String()
}

// aggregateSearchBody is the served /api/search envelope, decoded.
type aggregateSearchBody struct {
	Results []struct {
		Indexer string `json:"indexer"`
		Release struct {
			Title string `json:"title"`
			Link  string `json:"link"`
		} `json:"release"`
	} `json:"results"`
	Members []struct {
		Slug   string `json:"slug"`
		Name   string `json:"name"`
		Status string `json:"status"`
		Reason string `json:"reason"`
		Count  int    `json:"count"`
	} `json:"members"`
	Total  int `json:"total"`
	Limit  int `json:"limit"`
	Offset int `json:"offset"`
}

// aggregateEnv configures three indexers on the shared test definition — two live
// (each replaying its own host's results) and one disabled — and serves the router.
func aggregateEnv(t *testing.T) (string, *http.Client) {
	t.Helper()
	e := newEnvWithCache(t, api.Config{
		AuthDisabled: true,
		IPAllowlist:  []string{"127.0.0.0/8", "::1/128"},
	}, nil, registry.WithDoerFactory(func(registry.ClientParams) (search.Doer, error) {
		return perHostDoer{bodies: map[string]string{
			"alpha.invalid": rowsHTML("Alpha One 1080p", "Alpha One 2160p"),
			"beta.invalid":  rowsHTML("Beta One 1080p"),
			"gamma.invalid": rowsHTML("Gamma One 1080p"),
		}}, nil
	}))
	for _, ix := range []struct{ slug, base string }{
		{"alpha", "https://alpha.invalid/"},
		{"beta", "https://beta.invalid/"},
		{"gamma", "https://gamma.invalid/"},
	} {
		if _, err := e.registry.Add(context.Background(), registry.AddParams{
			Slug: ix.slug, DefinitionID: "testtracker", BaseURL: ix.base,
			Settings: map[string]string{"apikey": "k"},
		}); err != nil {
			t.Fatalf("Add(%s): %v", ix.slug, err)
		}
	}
	base, c := serve(t, e)
	resp, body := do(t, c, http.MethodPost, base+"/api/indexers/gamma/disable", nil, nil)
	mustStatus(t, resp, body, http.StatusNoContent)
	return base, c
}

func getAggregate(t *testing.T, base string, c *http.Client, query string) aggregateSearchBody {
	t.Helper()
	resp, body := do(t, c, http.MethodGet, base+"/api/search?"+query, nil, nil)
	mustStatus(t, resp, body, http.StatusOK)
	var got aggregateSearchBody
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode /api/search: %v (%s)", err, body)
	}
	return got
}

// TestSearchAggregateServesMergedWindowAndLedger is the endpoint's contract: an
// explicit subset comes back as ONE merged window whose releases each name their
// origin, a total that is the merged fetched count, and one ledger row per SELECTED
// member — including the disabled one, which explains itself rather than vanishing.
func TestSearchAggregateServesMergedWindowAndLedger(t *testing.T) {
	t.Parallel()
	base, c := aggregateEnv(t)

	got := getAggregate(t, base, c, "indexers=alpha,beta,gamma&q=one")

	if got.Total != 3 || len(got.Results) != 3 {
		t.Fatalf("total = %d with %d results, want 3 merged releases (2 alpha + 1 beta)", got.Total, len(got.Results))
	}
	origins := map[string]string{}
	for _, r := range got.Results {
		origins[r.Release.Title] = r.Indexer
	}
	for title, want := range map[string]string{
		"Alpha One 1080p": "alpha",
		"Alpha One 2160p": "alpha",
		"Beta One 1080p":  "beta",
	} {
		if origins[title] != want {
			t.Errorf("release %q origin = %q, want %q", title, origins[title], want)
		}
	}
	if origins["Gamma One 1080p"] != "" {
		t.Error("a disabled member contributed releases")
	}

	want := map[string]struct {
		status string
		reason string
		count  int
	}{
		"alpha": {status: "ok", count: 2},
		"beta":  {status: "ok", count: 1},
		"gamma": {status: "skipped", reason: "disabled"},
	}
	if len(got.Members) != len(want) {
		t.Fatalf("ledger has %d rows, want one per selected member (%d): %+v", len(got.Members), len(want), got.Members)
	}
	for _, m := range got.Members {
		w, ok := want[m.Slug]
		if !ok {
			t.Errorf("unexpected ledger row %q", m.Slug)
			continue
		}
		if m.Status != w.status || m.Reason != w.reason || m.Count != w.count {
			t.Errorf("ledger[%s] = {status:%q reason:%q count:%d}, want {%q %q %d}",
				m.Slug, m.Status, m.Reason, m.Count, w.status, w.reason, w.count)
		}
		if m.Name == "" {
			t.Errorf("ledger[%s] has no name to render", m.Slug)
		}
	}
}

// TestSearchAggregateRejectsBadSubset: an explicit list is the caller asserting these
// indexers exist, so a slug that names none is a 400 (a client bug), as is an empty or
// missing list — never a silently narrower search.
func TestSearchAggregateRejectsBadSubset(t *testing.T) {
	t.Parallel()
	base, c := aggregateEnv(t)

	for _, tt := range []struct{ name, query string }{
		{"unknown slug", "indexers=alpha,nosuch&q=one"},
		{"only unknown", "indexers=nosuch&q=one"},
		{"empty list", "indexers=&q=one"},
		{"blank entries", "indexers=,%20,&q=one"},
		{"missing param", "q=one"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			resp, body := do(t, c, http.MethodGet, base+"/api/search?"+tt.query, nil, nil)
			mustStatus(t, resp, body, http.StatusBadRequest)
		})
	}
}

// TestSearchAggregateDisabledOnlySubsetStillAnswers: a subset made entirely of skipped
// members is a successful, empty, fully-explained answer — the ledger is the whole point.
func TestSearchAggregateDisabledOnlySubsetStillAnswers(t *testing.T) {
	t.Parallel()
	base, c := aggregateEnv(t)

	got := getAggregate(t, base, c, "indexers=gamma&q=one")
	if got.Total != 0 || len(got.Results) != 0 {
		t.Errorf("total = %d with %d results, want an empty window", got.Total, len(got.Results))
	}
	if len(got.Members) != 1 || got.Members[0].Status != "skipped" || got.Members[0].Reason != "disabled" {
		t.Errorf("members = %+v, want one skipped/disabled row", got.Members)
	}
}

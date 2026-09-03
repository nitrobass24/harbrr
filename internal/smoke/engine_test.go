package smoke

import (
	"context"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// results builds a Result slice from titles (size is irrelevant to the diff logic).
func results(titles ...string) []Result {
	out := make([]Result, 0, len(titles))
	for _, t := range titles {
		out = append(out, Result{Title: t})
	}
	return out
}

// nResults builds n results with distinct prefixed titles.
func nResults(prefix string, n int) []Result {
	out := make([]Result, 0, n)
	for i := range n {
		out = append(out, Result{Title: fmt.Sprintf("%s-%d", prefix, i)})
	}
	return out
}

func TestDiffPass(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		harbrr   []Result
		prowlarr []Result
		wantPass bool
		wantNote string // substring of the returned note
	}{
		{"both empty", nil, nil, true, "both empty"},
		{"harbrr misses everything", nil, results("a", "b"), false, "harbrr returned 0"},
		{"prowlarr cache miss", results("a"), nil, true, "Prowlarr 0"},
		{"count ratio below floor", nResults("h", 4), nResults("p", 10), false, "count ratio"},
		{
			"count ratio at floor with good jaccard",
			results("alpha", "bravo", "charlie", "delta", "echo"),
			results("alpha", "bravo", "charlie", "delta", "echo", "foxtrot", "golf", "hotel", "india", "juliet"),
			true, "title Jaccard",
		},
		{
			"good ratio good jaccard",
			results("alpha", "bravo", "charlie", "delta"),
			results("alpha", "bravo", "charlie", "echo"),
			true, "title Jaccard",
		},
		{
			"good ratio low jaccard below cap",
			nResults("h", 10), nResults("p", 10),
			false, "title Jaccard",
		},
		{
			"double-cap window: full page both sides, disjoint titles",
			nResults("h", resultCap), nResults("p", resultCap),
			true, "count parity",
		},
		{
			"uncapped oracle: harbrr full page vs unpaged Prowlarr superset",
			nResults("t", resultCap), nResults("t", 696),
			true, "clamped",
		},
		{
			"uncapped oracle: harbrr under-filled page still fails",
			nResults("t", 40), nResults("t", 696),
			false, "count ratio",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			pass, note := DiffPass(tt.harbrr, tt.prowlarr)
			if pass != tt.wantPass {
				t.Fatalf("DiffPass pass = %v, want %v (note %q)", pass, tt.wantPass, note)
			}
			if !strings.Contains(note, tt.wantNote) {
				t.Errorf("DiffPass note = %q, want substring %q", note, tt.wantNote)
			}
		})
	}
}

func TestDiffPassCountRatioBoundary(t *testing.T) {
	t.Parallel()
	// ratio exactly countRatioMin (5/10 = 0.50) must clear the ratio gate (it is a
	// >= comparison), so the verdict then hinges on the title Jaccard.
	atFloor := DiffPassRatio(5, 10)
	if atFloor < countRatioMin {
		t.Fatalf("expected 5/10 >= %.2f", countRatioMin)
	}
	// Just below the floor fails outright regardless of titles.
	pass, note := DiffPass(nResults("h", 4), nResults("p", 10))
	if pass || !strings.Contains(note, "count ratio") {
		t.Errorf("4 vs 10 should fail on count ratio, got pass=%v note=%q", pass, note)
	}
}

// DiffPassRatio is a tiny test helper mirroring the ratio the differential computes.
func DiffPassRatio(h, p int) float64 {
	return float64(min(h, p)) / float64(max(h, p))
}

func TestNormalizeTitle(t *testing.T) {
	t.Parallel()
	tests := []struct{ in, want string }{
		{"The.Matrix (1999)!!", "the matrix 1999"},
		{"  Multiple   Spaces  ", "multiple spaces"},
		{"UPPER_lower-123", "upper lower 123"},
		{"---", ""},
	}
	for _, tt := range tests {
		if got := normalizeTitle(tt.in); got != tt.want {
			t.Errorf("normalizeTitle(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestTitleJaccard(t *testing.T) {
	t.Parallel()
	if got := titleJaccard(nil, nil); got != 1 {
		t.Errorf("empty/empty jaccard = %v, want 1", got)
	}
	// {a,b,c} vs {a,b,d}: inter 2, union 4 -> 0.5.
	got := titleJaccard(results("a", "b", "c"), results("a", "b", "d"))
	if got < 0.49 || got > 0.51 {
		t.Errorf("jaccard = %v, want ~0.5", got)
	}
	// Disjoint sets -> 0.
	if got := titleJaccard(results("a"), results("b")); got != 0 {
		t.Errorf("disjoint jaccard = %v, want 0", got)
	}
}

func TestHarbrrSearchURL(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		nocache bool
		want    bool // want "nocache=1" present
	}{
		{"differential bypass adds nocache=1", true, true},
		{"cached-path check omits nocache", false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			u := harbrrSearchURL("http://harbrr:7478", "key", "slug", "test query", tt.nocache)
			got := strings.Contains(u, "nocache=1")
			if got != tt.want {
				t.Errorf("harbrrSearchURL(nocache=%v) = %q, contains nocache=1 = %v, want %v", tt.nocache, u, got, tt.want)
			}
			// The base request shape must be unaffected by the bypass flag.
			if !strings.Contains(u, "t=search") || !strings.Contains(u, "q=test+query") || !strings.HasPrefix(u, "http://harbrr:7478/api/indexers/slug/results/torznab/api?") {
				t.Errorf("harbrrSearchURL(nocache=%v) = %q, unexpected base shape", tt.nocache, u)
			}
		})
	}
}

func TestParseTorznab(t *testing.T) {
	t.Parallel()
	body := []byte(`<?xml version="1.0"?><rss><channel>
		<item><title>First Release</title><size>1024</size></item>
		<item><title>Second Release</title><enclosure url="http://x/dl" length="2048"/></item>
	</channel></rss>`)
	res, err := ParseTorznab(body)
	if err != nil {
		t.Fatalf("ParseTorznab: %v", err)
	}
	if len(res) != 2 {
		t.Fatalf("got %d items, want 2", len(res))
	}
	if res[0].Title != "First Release" || res[0].Size != 1024 {
		t.Errorf("item 0 = %+v", res[0])
	}
	// size falls back to the enclosure length when <size> is absent.
	if res[1].Size != 2048 {
		t.Errorf("item 1 size = %d, want 2048 (enclosure fallback)", res[1].Size)
	}
}

func TestParseTorznabInvalid(t *testing.T) {
	t.Parallel()
	if _, err := ParseTorznab([]byte("<<<not xml")); err == nil {
		t.Error("expected an error on malformed XML")
	}
}

func TestParseConfig(t *testing.T) {
	t.Parallel()
	fake := func(m map[string]string) func(string) string {
		return func(k string) string { return m[k] }
	}
	base := map[string]string{
		"SMOKE_HARBRR_URL":      "http://harbrr:7478/",
		"SMOKE_HARBRR_APIKEY":   "hk",
		"SMOKE_PROWLARR_URL":    "http://prowlarr:9696",
		"SMOKE_PROWLARR_APIKEY": "pk",
	}

	t.Run("required present, optional absent", func(t *testing.T) {
		t.Parallel()
		cfg, err := ParseConfig(fake(base))
		if err != nil {
			t.Fatalf("ParseConfig: %v", err)
		}
		if cfg.HarbrrURL != "http://harbrr:7478" {
			t.Errorf("HarbrrURL = %q, want trailing slash trimmed", cfg.HarbrrURL)
		}
		if cfg.Query != "" || cfg.FallbackQuery != "" {
			t.Errorf("unset queries should be empty (a category-aware default is chosen per tracker later): query=%q fallback=%q", cfg.Query, cfg.FallbackQuery)
		}
		if cfg.SonarrURL != "" || cfg.RadarrURL != "" || cfg.QuiURL != "" {
			t.Errorf("optional apps should be empty: %+v", cfg)
		}
	})

	t.Run("optional apps and query overrides", func(t *testing.T) {
		t.Parallel()
		m := map[string]string{}
		maps.Copy(m, base)
		m["SMOKE_SONARR_URL"] = "http://sonarr:8989/"
		m["SMOKE_SONARR_APIKEY"] = "sk"
		m["SMOKE_QUI_URL"] = "http://qui:7476"
		m["SMOKE_QUI_APIKEY"] = "qk"
		m["SMOKE_QUERY"] = "ubuntu"
		m["SMOKE_QUERY_FALLBACK"] = "debian"
		cfg, err := ParseConfig(fake(m))
		if err != nil {
			t.Fatalf("ParseConfig: %v", err)
		}
		if cfg.SonarrURL != "http://sonarr:8989" || cfg.SonarrKey != "sk" {
			t.Errorf("sonarr not parsed: %q %q", cfg.SonarrURL, cfg.SonarrKey)
		}
		if cfg.QuiURL != "http://qui:7476" || cfg.QuiKey != "qk" {
			t.Errorf("qui not parsed: %q %q", cfg.QuiURL, cfg.QuiKey)
		}
		if cfg.Query != "ubuntu" || cfg.FallbackQuery != "debian" {
			t.Errorf("query overrides not applied: %q %q", cfg.Query, cfg.FallbackQuery)
		}
	})

	t.Run("missing required errors", func(t *testing.T) {
		t.Parallel()
		for _, drop := range []string{"SMOKE_HARBRR_URL", "SMOKE_HARBRR_APIKEY", "SMOKE_PROWLARR_URL", "SMOKE_PROWLARR_APIKEY"} {
			m := map[string]string{}
			maps.Copy(m, base)
			delete(m, drop)
			if _, err := ParseConfig(fake(m)); err == nil {
				t.Errorf("dropping %s should error", drop)
			}
		}
	})
}

func TestGrabSucceeded(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		result string
		want   bool
	}{
		{"not attempted", "", true},
		{"torrent", "torrent", true},
		{"magnet", "magnet", true},
		{"nzb", "nzb", true},
		{"no download link", "no download link", false},
		{"unrecognized body", "not a torrent/magnet", false},
		{"download error", "download HTTP 500", false},
		{"download not found", "download HTTP 404", false},
		// Skipped is non-failing for the callers, but it is NOT a success either —
		// GrabSkipped is the separate question, and skipped must never render as a pass.
		{"skipped empty search", grabSkippedEmpty, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := GrabSucceeded(tt.result); got != tt.want {
				t.Errorf("GrabSucceeded(%q) = %v, want %v", tt.result, got, tt.want)
			}
		})
	}
}

func TestGrabSkipped(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		result string
		want   bool
	}{
		{"skipped empty search", grabSkippedEmpty, true},
		{"not attempted", "", false},
		{"torrent", "torrent", false},
		{"no download link", "no download link", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := GrabSkipped(tt.result); got != tt.want {
				t.Errorf("GrabSkipped(%q) = %v, want %v", tt.result, got, tt.want)
			}
		})
	}
}

func TestClassifyGrabBody(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		body string
		want string
	}{
		{"bencoded torrent", "d8:announce31:http://tracker.example/announcee4:infod4:name4:testee", "torrent"},
		{"nzb", `<?xml version="1.0" encoding="iso-8859-1" ?>` + "\n" +
			`<!DOCTYPE nzb PUBLIC "-//newzBin//DTD NZB 1.1//EN" "http://www.newzbin.com/DTD/nzb/nzb-1.1.dtd">` + "\n" +
			`<nzb xmlns="http://www.newzbin.com/DTD/2003/nzb"><file subject="x"/></nzb>`, "nzb"},
		{"html error page", "<!DOCTYPE html><html><body>login required</body></html>", "not a torrent/magnet"},
		{"empty", "", "not a torrent/magnet"},
		// The two shapes a marker-byte sniff waved through. A tracker answering 200
		// with prose starting with 'd' is the dangerous one: it would have passed the
		// grab assertion as a torrent.
		{"plain-text error starting with d", "download unavailable", "not a torrent/magnet"},
		{"bencoded but no info dict", "d8:announce31:http://tracker.example/announcee", "not a torrent/magnet"},
		{"html carrying a literal nzb tag", "<html><body>see <nzb> docs</body></html>", "not a torrent/magnet"},
		{"newznab error document", `<?xml version="1.0"?><error code="101" description="no such item"/>`, "not a torrent/magnet"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := classifyGrabBody([]byte(tt.body)); got != tt.want {
				t.Errorf("classifyGrabBody(%q) = %q, want %q", tt.body, got, tt.want)
			}
		})
	}
}

func TestValidateNoSecrets(t *testing.T) {
	t.Parallel()
	clean := EvidenceRecord{Tracker: "demo", Notes: "count ratio 0.80", HarbrrTitles: []string{"Ubuntu 24.04"}}
	if err := ValidateNoSecrets(clean); err != nil {
		t.Errorf("clean record should validate: %v", err)
	}
	leak := EvidenceRecord{Tracker: "demo", HarbrrTitles: []string{"show with a passkey in the title"}}
	if err := ValidateNoSecrets(leak); err == nil {
		t.Error("a title carrying a passkey token must error")
	}
}

func TestReportMarkdownRedaction(t *testing.T) {
	t.Parallel()
	const secret = "DEADBEEFSECRET123456"
	rep := Report{
		Query: "test",
		Findings: []Finding{
			{
				Indexer: "demo", Check: CheckAppSync, Status: StatusFail,
				Detail: "feed http://tracker.example/rss?passkey=" + secret + "&t=caps",
			},
			{Indexer: "demo", Check: CheckParity, Status: StatusPass, Detail: "q=\"test\" harbrr=5 prowlarr=5: ok"},
		},
	}
	md := rep.Markdown()
	if strings.Contains(md, secret) {
		t.Fatalf("report leaked a secret substring:\n%s", md)
	}
	if !strings.Contains(md, "REDACTED") {
		t.Errorf("expected the leaked value to be scrubbed to REDACTED:\n%s", md)
	}
	if !strings.Contains(md, "## Failures") {
		t.Errorf("failures-first section missing:\n%s", md)
	}
}

func TestReportHasFailures(t *testing.T) {
	t.Parallel()
	clean := Report{Findings: []Finding{{Status: StatusPass}, {Status: StatusNA}, {Status: StatusSkip}}}
	if clean.HasFailures() {
		t.Error("no FAIL findings should report no failures")
	}
	dirty := Report{Findings: []Finding{{Status: StatusPass}, {Status: StatusFail}}}
	if !dirty.HasFailures() {
		t.Error("a FAIL finding should report failures")
	}
}

// noLinkSentinel makes grabStubServer serve an item with an EMPTY <link> (the
// "nothing grabbable in the feed" case), which a plain empty override cannot express.
// emptyFeedSentinel makes it serve a feed with NO items at all (the "search matched
// nothing" case, issue #566) — distinct: rows without a link still fail (#429).
const (
	noLinkSentinel    = "-"
	emptyFeedSentinel = "--"
)

// grabFeed renders a Torznab feed carrying one item whose <link> is the given URL.
func grabFeed(link string) string {
	return `<?xml version="1.0" encoding="UTF-8"?><rss><channel><item><title>Release</title>` +
		`<link>` + strings.ReplaceAll(link, "&", "&amp;") + `</link></item></channel></rss>`
}

// grabStubServer serves both halves of the grab path: the harbrr Torznab feed (whose
// item link is the server's own /dl unless linkOverride names another) and the download
// endpoint, which answers with dlStatus/dlBody.
func grabStubServer(t *testing.T, linkOverride string, dlStatus int, dlBody string) *httptest.Server {
	t.Helper()
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/results/torznab/api") {
			w.Header().Set("Content-Type", "application/xml")
			if linkOverride == emptyFeedSentinel {
				fmt.Fprint(w, `<?xml version="1.0" encoding="UTF-8"?><rss><channel></channel></rss>`)
				return
			}
			link := srv.URL + "/dl?apikey=k"
			switch linkOverride {
			case noLinkSentinel:
				link = ""
			case "":
			default:
				link = linkOverride
			}
			fmt.Fprint(w, grabFeed(link))
			return
		}
		w.WriteHeader(dlStatus)
		fmt.Fprint(w, dlBody)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestGrabCheck pins the CLI download-path check against stubbed responses: a real
// payload passes, and every non-payload outcome (500, an HTML error page, no link at
// all) is a FAIL finding — the coverage gap issue #435 closes, where a broken download
// path hid behind a clean search differential.
func TestGrabCheck(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		link         string // "" = the stub's own /dl, "-" = an item with no link
		dlStatus     int
		dlBody       string
		wantStatus   string
		wantContains string
	}{
		{"bencoded torrent passes", "", 200, "d4:infod6:lengthi1eee", StatusPass, grabTorrent},
		{"nzb passes", "", 200, `<?xml version="1.0" encoding="iso-8859-1"?><nzb><file/></nzb>`, StatusPass, grabNZB},
		{"magnet link passes without a fetch", "magnet:?xt=urn:btih:abc", 200, "", StatusPass, grabMagnet},
		{"download 500 fails", "", 500, "boom", StatusFail, "download HTTP 500"},
		{"html error page fails", "", 200, "<html><body>login required</body></html>", StatusFail, grabUnknown},
		{"plain text starting with d fails", "", 200, "download unavailable", StatusFail, grabUnknown},
		{"feed item without a link fails", noLinkSentinel, 200, "", StatusFail, grabNoLink},
		{"empty search skips, never passes", emptyFeedSentinel, 200, "", StatusSkip, "search matched nothing to grab"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			srv := grabStubServer(t, tt.link, tt.dlStatus, tt.dlBody)
			cfg := Config{HarbrrURL: srv.URL, HarbrrKey: "k", Query: "q", Grab: true}
			got := grabCheck(context.Background(), srv.Client(), cfg, "tracker", nil)
			if len(got) != 1 {
				t.Fatalf("grabCheck returned %d findings, want 1", len(got))
			}
			if got[0].Status != tt.wantStatus {
				t.Errorf("status = %q, want %q (detail %q)", got[0].Status, tt.wantStatus, got[0].Detail)
			}
			if !strings.Contains(got[0].Detail, tt.wantContains) {
				t.Errorf("detail = %q, want it to mention %q", got[0].Detail, tt.wantContains)
			}
			if got[0].Check != CheckGrab || got[0].Indexer != "tracker" {
				t.Errorf("finding not addressed to the tracker's grab check: %+v", got[0])
			}
		})
	}
}

// deadServer returns a stopped httptest server — a guaranteed-refused address for the
// unreachable-host cases, and its client.
func deadServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	srv.Close()
	return srv
}

// TestHarbrrHasDownloadLinks pins the evidence probe the //go:build smoke front-end
// records as downloadLinksPresent: a feed item with a link is grabbable, an item without
// one is not, and a feed that cannot be fetched reports an error (and not-grabbable).
func TestHarbrrHasDownloadLinks(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		server  func(t *testing.T) *httptest.Server
		wantOK  bool
		wantErr bool
	}{
		{"feed with links", func(t *testing.T) *httptest.Server { return grabStubServer(t, "", 200, "") }, true, false},
		{"feed without links", func(t *testing.T) *httptest.Server { return grabStubServer(t, noLinkSentinel, 200, "") }, false, false},
		{"unreachable feed", deadServer, false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			srv := tt.server(t)
			cfg := Config{HarbrrURL: srv.URL, HarbrrKey: "k"}
			ok, err := harbrrHasDownloadLinks(context.Background(), srv.Client(), cfg, "tracker", "q")
			if ok != tt.wantOK || (err != nil) != tt.wantErr {
				t.Errorf("harbrrHasDownloadLinks = (%v, %v), want (%v, err!=nil=%v)", ok, err, tt.wantOK, tt.wantErr)
			}
		})
	}
}

// TestGrabCheckGatedOff pins the opt-in gate: with SMOKE_GRAB unset (cfg.Grab false) the
// CLI suite runs no grab at all, so a routine run never pulls a real payload.
func TestGrabCheckGatedOff(t *testing.T) {
	t.Parallel()
	srv := grabStubServer(t, "", 500, "boom")
	cfg := Config{HarbrrURL: srv.URL, HarbrrKey: "k", Query: "q"}
	if got := grabCheck(context.Background(), srv.Client(), cfg, "tracker", nil); len(got) != 0 {
		t.Errorf("grabCheck with Grab unset returned %+v, want no findings", got)
	}
}

// TestGrabCheckFindingCarriesNoSecret pins the redaction contract: the resolved download
// URL never reaches a Finding. The stub serves a link carrying a passkey (in the path AND
// the query) and points at a closed port, so the failure is the URL-bearing transport
// error — the detail must name neither the credential nor any secret token.
func TestGrabCheckFindingCarriesNoSecret(t *testing.T) {
	t.Parallel()
	dead := deadServer(t) // the download host refuses the connection
	srv := grabStubServer(t, dead.URL+"/download/SUPERSECRETPASSKEY?passkey=SUPERSECRETPASSKEY&apikey=hk", 200, "")
	cfg := Config{HarbrrURL: srv.URL, HarbrrKey: "hk", Query: "q", Grab: true}

	got := grabCheck(context.Background(), srv.Client(), cfg, "tracker", nil)
	if len(got) != 1 || got[0].Status != StatusFail {
		t.Fatalf("a transport error should degrade to one FAIL finding, got %+v", got)
	}
	if strings.Contains(got[0].Detail, "SUPERSECRETPASSKEY") {
		t.Errorf("finding detail leaked the download credential: %q", got[0].Detail)
	}
	low := strings.ToLower(got[0].Detail)
	for _, tok := range secretTokens {
		if strings.Contains(low, tok) {
			t.Errorf("finding detail carries the secret token %q: %q", tok, got[0].Detail)
		}
	}
	if md := (Report{Findings: got}).Markdown(); strings.Contains(md, "SUPERSECRETPASSKEY") {
		t.Errorf("rendered report leaked the download credential:\n%s", md)
	}
}

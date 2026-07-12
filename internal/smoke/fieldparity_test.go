package smoke

import (
	"strings"
	"testing"
	"time"
)

func intp(n int) *int { return &n }

// TestFieldParityMatching verifies fields are compared only on titles present — and
// unique — in both sets.
func TestFieldParityMatching(t *testing.T) {
	t.Parallel()
	harbrr := []Result{{Title: "Alpha", Size: 100}, {Title: "Bravo", Size: 100}}
	prowlarr := []Result{{Title: "Alpha", Size: 100}, {Title: "Charlie", Size: 100}}
	fp := fieldParity(harbrr, prowlarr, false)
	if fp.Compared != 1 {
		t.Fatalf("Compared = %d, want 1 (only Alpha is shared)", fp.Compared)
	}
	if len(fp.Divergences) != 0 {
		t.Errorf("unexpected divergences: %+v", fp.Divergences)
	}

	// A title duplicated on one side is ambiguous and must not be compared.
	dupHarbrr := []Result{{Title: "Alpha", Size: 100}, {Title: "Alpha", Size: 999}}
	fp = fieldParity(dupHarbrr, []Result{{Title: "Alpha", Size: 100}}, false)
	if fp.Compared != 0 {
		t.Errorf("Compared = %d, want 0 (Alpha is ambiguous on the harbrr side)", fp.Compared)
	}
}

func TestSizeDivergence(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		hSize      int64
		pSize      int64
		wantDiverg bool
	}{
		// "1.4" read as GB (1.4e9) vs as GiB (1.4*2^30) — a unit-confusion bug; diff
		// ~103 MB exceeds the 64 MiB absolute floor.
		{"gib-vs-gb unit bug", 1_400_000_000, 1_503_238_553, true},
		// A GiB/GB unit bug at 10 GB (~737 MB apart) trips the relative floor.
		{"gib-vs-gb unit bug at 10GB", 10_000_000_000, 10_737_418_240, true},
		// "1.4 GB" scraped text -> 1.4*2^30 vs the true exact 1.44 GB from the API:
		// ~43 MB apart, legitimate display rounding, must NOT flap (within the 64 MiB floor).
		{"gb display rounding within abs floor", 1_503_238_553, 1_546_188_226, false},
		{"within tolerance", 1000, 1015, false},
		{"exact", 5000, 5000, false},
		{"oracle missing size", 5000, 0, false}, // not comparable
		{"harbrr missing size", 0, 5000, false}, // not comparable
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			d, ok := sizeDivergence(Result{Size: tt.hSize}, Result{Size: tt.pSize})
			if ok != tt.wantDiverg {
				t.Fatalf("sizeDivergence ok = %v, want %v (%+v)", ok, tt.wantDiverg, d)
			}
			if ok && d.Field != "size" {
				t.Errorf("field = %q, want size", d.Field)
			}
		})
	}
}

func TestCategoryDivergence(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		hCats      []int
		pCats      []int
		wantDiverg bool
	}{
		{"disjoint majors (movie vs tv)", []int{2040}, []int{5000}, true},
		{"same major, different subcat", []int{2040}, []int{2000}, false},
		{"overlapping majors", []int{2040, 5030}, []int{5000}, false},
		{"harbrr empty", nil, []int{2000}, false},
		{"oracle empty", []int{2000}, nil, false},
		{"harbrr only custom cats", []int{100001}, []int{5000}, false}, // customs ignored -> not comparable
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			d, ok := categoryDivergence(Result{Categories: tt.hCats}, Result{Categories: tt.pCats})
			if ok != tt.wantDiverg {
				t.Fatalf("categoryDivergence ok = %v, want %v (%+v)", ok, tt.wantDiverg, d)
			}
		})
	}
}

func TestDownloadURLDivergence(t *testing.T) {
	t.Parallel()
	const secret = "DEADBEEFPASSKEY0123456789"
	tests := []struct {
		name       string
		url        string
		wantDiverg bool
	}{
		{"raw passkey link", "http://tracker.example/download.php?id=42&passkey=" + secret, true},
		{"torrent_pass link", "http://tracker.example/rss?torrent_pass=" + secret, true},
		{"sealed harbrr dl", "https://harbrr.example/dl?token=abc123&apikey=def456", false},
		{"magnet", "magnet:?xt=urn:btih:0123456789abcdef", false},
		{"empty", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			d, ok := downloadURLDivergence(Result{Title: "Rel", DownloadURL: tt.url}, Result{})
			if ok != tt.wantDiverg {
				t.Fatalf("downloadURLDivergence ok = %v, want %v", ok, tt.wantDiverg)
			}
			if ok && strings.Contains(d.Detail, secret) {
				t.Fatalf("divergence detail leaked the secret value: %q", d.Detail)
			}
		})
	}
}

func TestStrictFieldGating(t *testing.T) {
	t.Parallel()
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	h := Result{Title: "Rel", Size: 100, Categories: []int{2000}, Seeders: nil, PublishDate: base.Add(72 * time.Hour)}
	p := Result{Title: "Rel", Size: 100, Categories: []int{2000}, Seeders: intp(500), PublishDate: base}

	// Non-strict: volatile divergences (seeders absent, pubDate 72h off) are NOT reported.
	if ds := compareFields(h, p, false); len(ds) != 0 {
		t.Fatalf("non-strict reported volatile divergences: %+v", ds)
	}
	// Strict: both the seeders and publishDate divergences surface.
	ds := compareFields(h, p, true)
	fields := map[string]bool{}
	for _, d := range ds {
		fields[d.Field] = true
	}
	if !fields["seeders"] {
		t.Errorf("strict did not flag seeders (oracle 500, harbrr absent): %+v", ds)
	}
	if !fields["publishDate"] {
		t.Errorf("strict did not flag publishDate (72h apart): %+v", ds)
	}
}

func TestSeedersDivergence(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		hSeeders   *int
		pSeeders   *int
		wantDiverg bool
	}{
		{"harbrr absent, oracle high", nil, intp(500), true},
		{"harbrr zero, oracle high", intp(0), intp(500), true},
		{"both present", intp(10), intp(500), false}, // magnitude is volatile
		{"oracle below floor", nil, intp(3), false},
		{"oracle absent", nil, nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, ok := seedersDivergence(Result{Seeders: tt.hSeeders}, Result{Seeders: tt.pSeeders})
			if ok != tt.wantDiverg {
				t.Fatalf("seedersDivergence ok = %v, want %v", ok, tt.wantDiverg)
			}
		})
	}
}

func TestPubDateDivergence(t *testing.T) {
	t.Parallel()
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name       string
		h, p       time.Time
		wantDiverg bool
	}{
		{"far apart", base.Add(72 * time.Hour), base, true},
		{"within window", base.Add(24 * time.Hour), base, false},
		{"harbrr zero", time.Time{}, base, false},
		{"oracle zero", base, time.Time{}, false},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, ok := pubDateDivergence(Result{PublishDate: tt.h}, Result{PublishDate: tt.p})
			if ok != tt.wantDiverg {
				t.Fatalf("pubDateDivergence ok = %v, want %v", ok, tt.wantDiverg)
			}
		})
	}
}

// TestFieldParityFinding exercises the checks.go integration: SKIP / PASS / FAIL and
// secret-safety of a download-url divergence.
func TestFieldParityFinding(t *testing.T) {
	t.Parallel()
	const secret = "DEADBEEFPASSKEY0123456789"

	skip := fieldParityFinding("demo", "test", results("A"), results("B"), false)
	if skip.Status != StatusSkip {
		t.Errorf("no shared titles: status = %q, want SKIP", skip.Status)
	}

	pass := fieldParityFinding("demo", "test",
		[]Result{{Title: "A", Size: 1000, Categories: []int{2000}}},
		[]Result{{Title: "A", Size: 1000, Categories: []int{2040}}}, false)
	if pass.Status != StatusPass {
		t.Errorf("agreeing fields: status = %q detail=%q, want PASS", pass.Status, pass.Detail)
	}

	fail := fieldParityFinding("demo", "test",
		[]Result{{Title: "A", Size: 1_400_000_000}},
		[]Result{{Title: "A", Size: 1_503_238_553}}, false)
	if fail.Status != StatusFail || !strings.Contains(fail.Detail, "size") {
		t.Errorf("size divergence: status=%q detail=%q, want FAIL mentioning size", fail.Status, fail.Detail)
	}

	leak := fieldParityFinding("demo", "test",
		[]Result{{Title: "A", DownloadURL: "http://t.example/d?passkey=" + secret}},
		[]Result{{Title: "A"}}, false)
	if leak.Status != StatusFail {
		t.Fatalf("leaked passkey link: status = %q, want FAIL", leak.Status)
	}
	if strings.Contains(leak.Detail, secret) {
		t.Fatalf("field-parity finding leaked the secret value: %q", leak.Detail)
	}
}

// TestParseTorznabFields verifies the enriched parse extracts categories, seeders,
// pubDate and the download link, and that a malformed category does not fail the feed.
func TestParseTorznabFields(t *testing.T) {
	t.Parallel()
	body := []byte(`<?xml version="1.0"?>
<rss xmlns:torznab="http://torznab.com/schemas/2015/feed">
  <channel>
    <item>
      <title>Some Release 2160p</title>
      <link>https://harbrr.example/dl?token=abc&amp;apikey=def</link>
      <size>2147483648</size>
      <pubDate>Mon, 02 Jan 2006 15:04:05 -0700</pubDate>
      <category>2000</category>
      <category>2040</category>
      <category>notanumber</category>
      <enclosure url="https://harbrr.example/dl?token=abc" length="2147483648" type="application/x-bittorrent"/>
      <torznab:attr name="category" value="2040"/>
      <torznab:attr name="seeders" value="123"/>
      <torznab:attr name="peers" value="150"/>
    </item>
  </channel>
</rss>`)
	res, err := ParseTorznab(body)
	if err != nil {
		t.Fatalf("ParseTorznab: %v", err)
	}
	if len(res) != 1 {
		t.Fatalf("got %d items, want 1", len(res))
	}
	r := res[0]
	if r.Size != 2147483648 {
		t.Errorf("size = %d", r.Size)
	}
	if want := []int{2000, 2040}; !equalInts(r.Categories, want) {
		t.Errorf("categories = %v, want %v (non-numeric skipped)", r.Categories, want)
	}
	if r.Seeders == nil || *r.Seeders != 123 {
		t.Errorf("seeders = %v, want 123", r.Seeders)
	}
	if r.PublishDate.IsZero() {
		t.Error("pubDate did not parse")
	}
	if !strings.Contains(r.DownloadURL, "/dl?token=") {
		t.Errorf("downloadURL = %q, want the sealed /dl link", r.DownloadURL)
	}
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestFieldParityWindowed verifies that when both sets hit the page cap, field
// comparison is skipped (the titles are config-sorted windows, not a stable set).
func TestFieldParityWindowed(t *testing.T) {
	t.Parallel()
	h := nResults("x", resultCap)
	p := nResults("x", resultCap)
	fp := fieldParity(h, p, false)
	if !fp.Windowed {
		t.Fatalf("expected Windowed at the %d-result page cap", resultCap)
	}
	if fp.Compared != 0 || len(fp.Divergences) != 0 {
		t.Errorf("windowed run must not compare: %+v", fp)
	}
	if f := fieldParityFinding("demo", "q", h, p, false); f.Status != StatusSkip {
		t.Errorf("windowed finding status = %q, want SKIP", f.Status)
	}
	// Just under the cap on one side -> not windowed, comparison proceeds.
	if fp := fieldParity(nResults("x", resultCap-1), p, false); fp.Windowed {
		t.Errorf("one side under the cap must not be windowed")
	}
}

func TestChooseQueries(t *testing.T) {
	t.Parallel()
	// Explicit SMOKE_QUERY always wins, with its explicit fallback.
	if p, f := chooseQueries([]int{5000}, Config{Query: "custom", FallbackQuery: "fb"}); p != "custom" || f != "fb" {
		t.Errorf("explicit query: got (%q,%q), want (custom,fb)", p, f)
	}
	// Explicit query, no explicit fallback -> generic fallback.
	if p, f := chooseQueries([]int{2000}, Config{Query: "custom"}); p != "custom" || f != genericFallback {
		t.Errorf("explicit query w/o fallback: got (%q,%q)", p, f)
	}
	tests := []struct {
		name    string
		catIDs  []int
		primary string
	}{
		{"movies subcat", []int{2040}, "Oppenheimer 2023"},
		{"tv is a single episode not a series", []int{5000}, "The Last of Us S01E01"},
		{"general tracker prefers movies", []int{5000, 2000, 3000}, "Oppenheimer 2023"},
		{"books only (e.g. MyAnonamouse)", []int{7000}, "Project Hail Mary"},
		{"audio", []int{3030}, "Radiohead In Rainbows"},
		{"no recognized category -> generic", nil, genericPrimary},
		{"custom cats only -> generic", []int{100001}, genericPrimary},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if p, _ := chooseQueries(tt.catIDs, Config{}); p != tt.primary {
				t.Errorf("chooseQueries(%v) primary = %q, want %q", tt.catIDs, p, tt.primary)
			}
		})
	}
}

func TestParseProwlarrResults(t *testing.T) {
	t.Parallel()
	body := []byte(`[
		{"title":"Some Movie 2023 1080p","size":2147483648,"seeders":42,
		 "publishDate":"2026-01-02T15:04:05Z","downloadUrl":"http://prowlarr.example/download?apikey=x",
		 "categories":[{"id":2000},{"id":2040}]},
		{"title":"Magnet Only","size":100,"magnetUrl":"magnet:?xt=urn:btih:abc","categories":[]}
	]`)
	res, err := parseProwlarrResults(body)
	if err != nil {
		t.Fatalf("parseProwlarrResults: %v", err)
	}
	if len(res) != 2 {
		t.Fatalf("got %d results, want 2", len(res))
	}
	r0 := res[0]
	if r0.Size != 2147483648 {
		t.Errorf("size = %d", r0.Size)
	}
	if r0.Seeders == nil || *r0.Seeders != 42 {
		t.Errorf("seeders = %v, want 42", r0.Seeders)
	}
	if r0.PublishDate.IsZero() {
		t.Error("publishDate (RFC3339) did not parse")
	}
	if !equalInts(r0.Categories, []int{2000, 2040}) {
		t.Errorf("categories = %v, want [2000 2040]", r0.Categories)
	}
	if r0.DownloadURL == "" {
		t.Error("downloadUrl not captured")
	}
	// magnetUrl is the fallback when downloadUrl is absent; seeders absent -> nil.
	if res[1].DownloadURL != "magnet:?xt=urn:btih:abc" {
		t.Errorf("magnet fallback = %q", res[1].DownloadURL)
	}
	if res[1].Seeders != nil {
		t.Errorf("absent seeders should be nil, got %v", res[1].Seeders)
	}
	if _, err := parseProwlarrResults([]byte("not json")); err == nil {
		t.Error("expected an error on malformed JSON")
	}
}

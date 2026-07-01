package smoke

import (
	"fmt"
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
	for i := 0; i < n; i++ {
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
		"SMOKE_HARBRR_URL":      "http://harbrr:7474/",
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
		if cfg.HarbrrURL != "http://harbrr:7474" {
			t.Errorf("HarbrrURL = %q, want trailing slash trimmed", cfg.HarbrrURL)
		}
		if cfg.Query != "test" || cfg.FallbackQuery != "2024" {
			t.Errorf("defaults not applied: query=%q fallback=%q", cfg.Query, cfg.FallbackQuery)
		}
		if cfg.SonarrURL != "" || cfg.RadarrURL != "" || cfg.QuiURL != "" {
			t.Errorf("optional apps should be empty: %+v", cfg)
		}
	})

	t.Run("optional apps and query overrides", func(t *testing.T) {
		t.Parallel()
		m := map[string]string{}
		for k, v := range base {
			m[k] = v
		}
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
			for k, v := range base {
				m[k] = v
			}
			delete(m, drop)
			if _, err := ParseConfig(fake(m)); err == nil {
				t.Errorf("dropping %s should error", drop)
			}
		}
	})
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

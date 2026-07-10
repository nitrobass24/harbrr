package native

import (
	"bytes"
	"strings"
	"testing"

	"github.com/rs/zerolog"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/normalizer"
)

// TestTraceReleasesLogsFieldsNeverURLs proves TraceReleases emits one trace line per
// release carrying the descriptive fields, and NEVER the URL-bearing fields (Link/GUID/
// Details) — a native download link can embed a passkey, so those must stay out of logs.
func TestTraceReleasesLogsFieldsNeverURLs(t *testing.T) {
	// Not parallel: Trace is below zerolog's default global level, so it is toggled
	// process-wide for the duration of this test and restored after.
	prev := zerolog.GlobalLevel()
	zerolog.SetGlobalLevel(zerolog.TraceLevel)
	defer zerolog.SetGlobalLevel(prev)

	var buf bytes.Buffer
	log := zerolog.New(&buf).Level(zerolog.TraceLevel)

	const secret = "PASSKEYSECRET"
	rels := []*normalizer.Release{
		{
			Title: "First", Size: 1073741824, Seeders: 10, Leechers: 2,
			Categories: []int{2000}, PublishDate: "2026-01-02T03:04:05Z",
			Link:    "https://x.test/dl/" + secret + ".torrent",
			GUID:    "guid-" + secret,
			Details: "https://x.test/details/" + secret,
		},
		{
			Title: "Second", Size: 500, Seeders: 0, Leechers: 0,
			Categories: []int{5000, 5040}, PublishDate: "2026-02-03T04:05:06Z",
			Link: "https://x.test/dl/" + secret, GUID: secret, Details: secret,
		},
	}

	TraceReleases(log, "gazelle", rels)

	out := buf.String()
	if lines := strings.Count(strings.TrimSpace(out), "\n") + 1; lines != len(rels) {
		t.Fatalf("got %d trace lines, want %d:\n%s", lines, len(rels), out)
	}
	for _, want := range []string{
		`"driver":"gazelle"`, `"title":"First"`, `"title":"Second"`,
		`"size":1073741824`, `"seeders":10`, `"leechers":2`,
		`"categories":[2000]`, `"categories":[5000,5040]`,
		`"publish_date":"2026-01-02T03:04:05Z"`, `"message":"native: parsed release"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("trace output missing %s:\n%s", want, out)
		}
	}
	if strings.Contains(out, secret) {
		t.Errorf("trace output leaked a URL/GUID/Details secret %q:\n%s", secret, out)
	}
}

// TestTraceReleasesZeroLoggerNoop proves the zero-value logger (the Params.Logger default)
// discards the events: it must not panic and must emit nothing, so a driver constructed
// without a logger is unaffected.
func TestTraceReleasesZeroLoggerNoop(t *testing.T) {
	t.Parallel()
	TraceReleases(zerolog.Logger{}, "x", []*normalizer.Release{{Title: "t", Link: "secret", GUID: "g"}})
}

package gazelle

import (
	"bytes"
	"context"
	stdhttp "net/http"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/indexer/native"
)

// TestSearchEmitsReleaseTrace is the driver-level wiring test: it proves Params.Logger
// flows through New into the driver and that parseBrowse emits one trace line per parsed
// release during a real Search — carrying the driver id but never a release's download link.
func TestSearchEmitsReleaseTrace(t *testing.T) {
	// Not parallel: Trace is below zerolog's default global level, so enable it
	// process-wide for the duration and restore afterward.
	prev := zerolog.GlobalLevel()
	zerolog.SetGlobalLevel(zerolog.TraceLevel)
	defer zerolog.SetGlobalLevel(prev)

	var buf bytes.Buffer
	body := readFixture(t, "testdata/browse_music.json")
	def := familyByID(t, "redacted").Definition
	drv, err := New(native.Params{
		Def:    def,
		Cfg:    map[string]string{"apikey": credAPIKey},
		Doer:   &scriptDoer{resp: mkResp(stdhttp.StatusOK, string(body))},
		Clock:  func() time.Time { return fixedClock },
		Logger: zerolog.New(&buf).Level(zerolog.TraceLevel),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	rels, err := drv.(*driver).Search(context.Background(), search.Query{Keywords: "logistics"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(rels) == 0 {
		t.Fatal("want releases, got 0")
	}

	out := buf.String()
	if n := strings.Count(out, "native: parsed release"); n != len(rels) {
		t.Fatalf("got %d trace lines, want one per release (%d):\n%s", n, len(rels), out)
	}
	if !strings.Contains(out, `"driver":"`+def.ID+`"`) {
		t.Errorf("trace missing driver id %q:\n%s", def.ID, out)
	}
	// A release's download link (Link) must never be logged.
	for _, r := range rels {
		if r.Link != "" && strings.Contains(out, r.Link) {
			t.Errorf("trace leaked a download link %q:\n%s", r.Link, out)
		}
	}
}

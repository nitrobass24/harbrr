package cardigann

import (
	"errors"
	stdhttp "net/http"
	"strings"
	"testing"
)

// failDoer fails every round-trip, so the engine's online error path runs with a
// passkey-bearing URL.
type failDoer struct{}

func (failDoer) Do(*stdhttp.Request) (*stdhttp.Response, error) {
	return nil, errors.New("dial tcp: connection refused")
}

// TestSearch_RedactsPasskey is the engine-surface redaction gate (success
// criterion: secrets redacted in logs). A search input carries a passkey from
// config; when the transport fails, the error bubbling out of Engine.Search must
// never contain the passkey. The passkey-shaped value is built by concatenation
// so secret scanners do not flag the fixture.
func TestSearch_RedactsPasskey(t *testing.T) {
	t.Parallel()

	passkey := "PK" + "deadbeefdeadbeefdeadbeefdeadbeef"
	def := loadFixtureDef(t, "passkey_search.yml")
	eng, err := NewEngine(def, WithClock(fixedClock()), WithDoer(failDoer{}),
		WithConfig(map[string]string{"passkey": passkey}))
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	_, err = eng.Search(t.Context(), Query{Keywords: "linux"})
	if err == nil {
		t.Fatal("Search returned nil error, want transport failure")
	}
	if strings.Contains(err.Error(), passkey) {
		t.Errorf("engine error leaked passkey: %q", err.Error())
	}
}

// TestParseResponse_DegradesOnGarbage proves a broken response degrades cleanly
// (success criterion: broken indexers degrade cleanly — surfaced, never
// crashing). Invalid JSON for a JSON def must return a loud error, not panic;
// HTML with no matching rows must return zero releases, not panic.
func TestParseResponse_DegradesOnGarbage(t *testing.T) {
	t.Parallel()

	t.Run("invalid json errors", func(t *testing.T) {
		t.Parallel()
		eng := newFixtureEngine(t, "json_api.yml")
		_, err := eng.ParseResponse([]byte("{not valid json"), "json")
		if err == nil {
			t.Fatal("ParseResponse of invalid JSON = nil error, want a loud error")
		}
	})

	t.Run("html without rows yields no releases", func(t *testing.T) {
		t.Parallel()
		eng := newFixtureEngine(t, "html_scrape.yml")
		releases, err := eng.ParseResponse([]byte("<html><body><p>no results</p></body></html>"), "")
		if err != nil {
			t.Fatalf("ParseResponse of row-less HTML errored: %v", err)
		}
		if len(releases) != 0 {
			t.Errorf("releases = %d, want 0", len(releases))
		}
	})
}

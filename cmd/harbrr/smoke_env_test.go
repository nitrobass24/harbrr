package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnvFileRoundTrip(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "smoke.env")
	in := map[string]string{
		"SMOKE_HARBRR_URL":      "http://harbrr:7478",
		"SMOKE_HARBRR_APIKEY":   "key with spaces",
		"SMOKE_PROWLARR_URL":    "http://prowlarr:9696",
		"SMOKE_PROWLARR_APIKEY": `quote"inside`,
		"SMOKE_QUI_URL":         "http://qui:7476",
		"SMOKE_QUI_APIKEY":      "qk",
	}
	if err := writeEnvFile(path, in); err != nil {
		t.Fatalf("writeEnvFile: %v", err)
	}
	// Written at 0600 so keys never sit world-readable.
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("mode = %o, want 600", perm)
	}

	got, err := parseEnvFile(path)
	if err != nil {
		t.Fatalf("parseEnvFile: %v", err)
	}
	for k, v := range in {
		if got[k] != v {
			t.Errorf("round-trip %s = %q, want %q", k, got[k], v)
		}
	}
	// Keys not supplied are not written and so not present on read-back.
	if _, ok := got["SMOKE_SONARR_URL"]; ok {
		t.Errorf("unset key should not be written")
	}
}

func TestParseEnvFileMissing(t *testing.T) {
	t.Parallel()
	got, err := parseEnvFile(filepath.Join(t.TempDir(), "does-not-exist.env"))
	if err != nil {
		t.Fatalf("missing file should not error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("missing file should yield an empty map, got %v", got)
	}
}

func TestParseEnvFileFormats(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "hand.env")
	content := "" +
		"# a comment\n" +
		"\n" +
		"export SMOKE_HARBRR_URL=http://harbrr:7478\n" +
		"SMOKE_HARBRR_APIKEY=\"quoted-key\"\n" +
		"export SMOKE_PROWLARR_URL='single-quoted'\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := parseEnvFile(path)
	if err != nil {
		t.Fatalf("parseEnvFile: %v", err)
	}
	want := map[string]string{
		"SMOKE_HARBRR_URL":    "http://harbrr:7478",
		"SMOKE_HARBRR_APIKEY": "quoted-key",
		"SMOKE_PROWLARR_URL":  "single-quoted",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
}

func TestMissingRequired(t *testing.T) {
	t.Parallel()
	full := map[string]string{
		"SMOKE_HARBRR_URL":      "u",
		"SMOKE_HARBRR_APIKEY":   "k",
		"SMOKE_PROWLARR_URL":    "u",
		"SMOKE_PROWLARR_APIKEY": "k",
	}
	getenv := func(m map[string]string) func(string) string {
		return func(k string) string { return m[k] }
	}
	if missingRequired(getenv(full)) {
		t.Error("a full required set should not be missing")
	}
	partial := map[string]string{"SMOKE_HARBRR_URL": "u", "SMOKE_HARBRR_APIKEY": "k"}
	if !missingRequired(getenv(partial)) {
		t.Error("a partial set (no Prowlarr) should be missing")
	}
}

// TestWriteEnvFilePreservesHandAddedKeys covers bug (b): a hand-added key (SMOKE_QUERY,
// not in smokeEnvKeys) must survive writeEnvFile and round-trip, while the known app keys
// still lead in smokeEnvKeys order.
func TestWriteEnvFilePreservesHandAddedKeys(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "smoke.env")
	in := map[string]string{
		"SMOKE_HARBRR_URL":     "http://harbrr:7478",
		"SMOKE_HARBRR_APIKEY":  "hk",
		"SMOKE_QUERY":          "ubuntu",
		"SMOKE_QUERY_FALLBACK": "debian",
		"SMOKE_CUSTOM_EXTRA":   "keep me",
	}
	if err := writeEnvFile(path, in); err != nil {
		t.Fatalf("writeEnvFile: %v", err)
	}
	got, err := parseEnvFile(path)
	if err != nil {
		t.Fatalf("parseEnvFile: %v", err)
	}
	for k, v := range in {
		if got[k] != v {
			t.Errorf("round-trip %s = %q, want %q (hand-added key dropped?)", k, got[k], v)
		}
	}
	// The known app keys are emitted before any hand-added key, in smokeEnvKeys order.
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	body := string(raw)
	if iURL, iQuery := strings.Index(body, "SMOKE_HARBRR_URL"), strings.Index(body, "SMOKE_QUERY"); iURL == -1 || iQuery == -1 || iURL > iQuery {
		t.Errorf("known app keys should precede hand-added keys; got:\n%s", body)
	}
	// Hand-added keys are written in sorted order (SMOKE_CUSTOM_EXTRA < SMOKE_QUERY).
	if iCustom, iQuery := strings.Index(body, "SMOKE_CUSTOM_EXTRA"), strings.Index(body, "SMOKE_QUERY"); iCustom == -1 || iQuery == -1 || iCustom > iQuery {
		t.Errorf("hand-added keys should be sorted; got:\n%s", body)
	}
}

// stubPrompts drives buildReconfigureValues without a TTY: line returns the queued reply
// (or the shown default on blank), key returns the queued reply.
type stubPrompts struct {
	urls, keys []string
	uIdx, kIdx int
}

func (s *stubPrompts) prompts() reconfigurePrompts {
	return reconfigurePrompts{
		line: func(_, def string) string {
			v := s.urls[s.uIdx]
			s.uIdx++
			if v == "" {
				return def
			}
			return v
		},
		key: func(_ string, _ bool) (string, error) {
			v := s.keys[s.kIdx]
			s.kIdx++
			return v, nil
		},
	}
}

// TestBuildReconfigureValuesKeepsKeyOnBlank covers bug (a): keeping an app's URL (blank
// Enter) then a blank key must KEEP the saved key, not blank it. Required harbrr+Prowlarr
// have saved keys; the optional apps are skipped.
func TestBuildReconfigureValuesKeepsKeyOnBlank(t *testing.T) {
	t.Parallel()
	existing := map[string]string{
		"SMOKE_HARBRR_URL":      "http://harbrr:7478",
		"SMOKE_HARBRR_APIKEY":   "saved-harbrr-key",
		"SMOKE_PROWLARR_URL":    "http://prowlarr:9696",
		"SMOKE_PROWLARR_APIKEY": "saved-prowlarr-key",
		"SMOKE_QUERY":           "ubuntu",
	}
	// Keep both required URLs (blank), blank keys for both; skip the three optional apps.
	sp := &stubPrompts{
		urls: []string{"", "", "", "", ""},
		keys: []string{"", ""},
	}
	values, err := buildReconfigureValues(io.Discard, existing, sp.prompts())
	if err != nil {
		t.Fatalf("buildReconfigureValues: %v", err)
	}
	if got := values["SMOKE_HARBRR_APIKEY"]; got != "saved-harbrr-key" {
		t.Errorf("harbrr key = %q, want the saved key kept (bug a: blank blanks the key)", got)
	}
	if got := values["SMOKE_PROWLARR_APIKEY"]; got != "saved-prowlarr-key" {
		t.Errorf("prowlarr key = %q, want the saved key kept", got)
	}
	// Bug (b) carry-over: the hand-added key survives into the output values.
	if got := values["SMOKE_QUERY"]; got != "ubuntu" {
		t.Errorf("SMOKE_QUERY = %q, want it carried over from existing", got)
	}
}

// TestBuildReconfigureValuesRequiredBlankKeyErrors: a required app with NO saved key and a
// blank key input is still an error (the keep-on-blank must not mask a genuinely missing
// required key).
func TestBuildReconfigureValuesRequiredBlankKeyErrors(t *testing.T) {
	t.Parallel()
	existing := map[string]string{} // no saved keys at all
	sp := &stubPrompts{
		urls: []string{"http://harbrr:7478"},
		keys: []string{""}, // blank key, nothing saved to fall back to
	}
	_, err := buildReconfigureValues(io.Discard, existing, sp.prompts())
	if err == nil {
		t.Fatal("a required app with no saved key + blank key input should error")
	}
	if !strings.Contains(err.Error(), "API key is required") {
		t.Errorf("error = %v, want a required-key error", err)
	}
}

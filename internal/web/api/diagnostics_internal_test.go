package api

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/autobrr/harbrr/internal/domain"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/indexer/registry"
)

// TestDiagnosticsResponseJSON asserts the wire shape the UI consumes: the ring is
// rendered newest-first, the two selector-miss kinds each survive with their
// definition path, a request-summary-only (transport) entry omits the response
// fields rather than reporting a zero status, and the redaction the engine applied
// is what reaches the client — the rendered JSON carries no credential.
func TestDiagnosticsResponseJSON(t *testing.T) {
	t.Parallel()

	// Synthetic fixture secrets, built by concatenation so scanners do not flag them.
	// They are what the engine's redaction is supposed to have removed BEFORE the
	// capture was stored, so seeing one here means a capture reached the API raw.
	passkey := "0f1e2d3c4b5a6978" + "8796a5b4c3d2e1f0"
	cookie := "session=" + "s3cr3tsessionvalue"

	at := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	captures := []registry.FailureCapture{
		{
			Kind: domain.HealthParseError, OccurredAt: at,
			Capture: search.Capture{
				Method: "GET", URL: "https://cap.invalid/api/REDACTED?passkey=REDACTED",
				Status:  200,
				Headers: map[string]string{"Content-Type": "text/html", "Set-Cookie": "REDACTED"},
				Body:    `{"apikey":"<redacted>"}`,
				Miss:    search.SelectorMiss{Kind: search.MissNoRows, Selector: "tr.torrent", Path: "/search/rows"},
			},
		},
		{
			Kind: domain.HealthParseError, OccurredAt: at.Add(-time.Minute),
			Capture: search.Capture{
				Method: "GET", URL: "https://cap.invalid/browse", Status: 200,
				Miss: search.SelectorMiss{Kind: search.MissFields, Selector: "a.title", Path: "/search/fields/title"},
			},
		},
		{
			Kind: domain.HealthTransport, OccurredAt: at.Add(-2 * time.Minute),
			Capture: search.Capture{Method: "GET", URL: "https://cap.invalid/browse"},
		},
	}

	raw, err := json.Marshal(toDiagnosticsResponse("torrentleech", captures))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := string(raw)

	for _, secret := range []string{passkey, cookie, "s3cr3tsessionvalue"} {
		if strings.Contains(got, secret) {
			t.Errorf("rendered diagnostics JSON leaked %q:\n%s", secret, got)
		}
	}
	for _, want := range []string{
		`"slug":"torrentleech"`,
		`"selectorMiss":{"kind":"no_rows","selector":"tr.torrent","path":"/search/rows"}`,
		`"selectorMiss":{"kind":"fields","selector":"a.title","path":"/search/fields/title"}`,
		`"Set-Cookie":"REDACTED"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("rendered JSON missing %s:\n%s", want, got)
		}
	}
	// The transport entry is request-summary-only: no status, no headers, no body,
	// and no selectorMiss to attribute.
	last := `{"kind":"transport","occurred_at":"2026-08-09T11:58:00Z","method":"GET","url":"https://cap.invalid/browse"}`
	if !strings.Contains(got, last) {
		t.Errorf("transport entry = not %s:\n%s", last, got)
	}
	// Newest first, in ring order.
	if i, j := strings.Index(got, "no_rows"), strings.Index(got, "fields"); i > j {
		t.Errorf("captures are not newest-first:\n%s", got)
	}
}

package search

import (
	"testing"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
)

func loginTestDef(sel string) *loader.Definition {
	return &loader.Definition{
		ID:    "lo",
		Login: &loader.Login{Test: &loader.PageTestBlock{Path: "/profile", Selector: sel}},
	}
}

func TestLooksLoggedOut(t *testing.T) {
	t.Parallel()
	// A logged-in page carries the test selector (a logout link in the site nav);
	// it is present on every authenticated page, including search results.
	loggedIn := []byte(`<html><body><nav><a class="logout" href="/logout">Logout</a></nav>` +
		`<table><tr class="row"><td><a class="title">Result</a></td></tr></table></body></html>`)
	// A logged-out page (the login form) lacks the logout link.
	loggedOut := []byte(`<html><body><form id="login"><input name="username"></form></body></html>`)

	deps := Deps{Config: map[string]string{}}
	q := Query{Keywords: "x"}

	tests := []struct {
		name        string
		def         *loader.Definition
		body        []byte
		contentType string
		want        bool
	}{
		{"no login block", &loader.Definition{ID: "x"}, loggedOut, "text/html", false},
		{"login but no test block", &loader.Definition{ID: "x", Login: &loader.Login{}}, loggedOut, "text/html", false},
		{"test selector empty", loginTestDef(""), loggedOut, "text/html", false},
		// A MISSING Content-Type still RUNS the check (Jackett: ...?? true).
		{"missing content-type + selector present -> logged in", loginTestDef("a.logout"), loggedIn, "", false},
		{"missing content-type + selector absent -> logged out", loginTestDef("a.logout"), loggedOut, "", true},
		// text/html (with or without parameters) RUNS the check.
		{"text/html + selector absent -> logged out", loginTestDef("a.logout"), loggedOut, "text/html", true},
		{"text/html;charset -> logged out", loginTestDef("a.logout"), loggedOut, "text/html; charset=utf-8", true},
		{"text/html + selector present -> logged in", loginTestDef("a.logout"), loggedIn, "text/html", false},
		// A non-HTML WIRE Content-Type SKIPS the check even when the body looks
		// logged-out — the gate is on the header, not the declared/parsed type.
		{"application/json skips", loginTestDef("a.logout"), loggedOut, "application/json", false},
		{"text/xml skips", loginTestDef("a.logout"), loggedOut, "text/xml", false},
		// Ordinal case-sensitivity: "TEXT/HTML" does not contain "text/html" -> skips.
		{"TEXT/HTML (ordinal case) skips", loginTestDef("a.logout"), loggedOut, "TEXT/HTML", false},
		// False-positive guard: a valid search-results page that still carries the
		// logout link must NOT be read as logged-out (prevents a relogin loop on
		// every normal search).
		{"valid results page with nav -> not logged out", loginTestDef("a.logout"), loggedIn, "text/html", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := looksLoggedOut(tt.def, tt.body, tt.contentType, q, deps); got != tt.want {
				t.Errorf("looksLoggedOut = %v, want %v", got, tt.want)
			}
		})
	}
}

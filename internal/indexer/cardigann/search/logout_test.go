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
		name     string
		def      *loader.Definition
		body     []byte
		respType string
		want     bool
	}{
		{"no login block", &loader.Definition{ID: "x"}, loggedOut, "", false},
		{"login but no test block", &loader.Definition{ID: "x", Login: &loader.Login{}}, loggedOut, "", false},
		{"test selector empty", loginTestDef(""), loggedOut, "", false},
		{"selector present on HTML page -> logged in", loginTestDef("a.logout"), loggedIn, "", false},
		{"selector absent on HTML page -> logged out", loginTestDef("a.logout"), loggedOut, "", true},
		{"json response never triggers", loginTestDef("a.logout"), []byte(`{"x":1}`), responseTypeJSON, false},
		{"xml response never triggers", loginTestDef("a.logout"), []byte(`<x/>`), responseTypeXML, false},
		// False-positive guard: a valid search-results page that still carries the
		// logout link must NOT be read as logged-out (this is what prevents a
		// relogin loop on every normal search).
		{"valid results page with nav -> not logged out", loginTestDef("a.logout"), loggedIn, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := looksLoggedOut(tt.def, tt.body, tt.respType, q, deps); got != tt.want {
				t.Errorf("looksLoggedOut = %v, want %v", got, tt.want)
			}
		})
	}
}

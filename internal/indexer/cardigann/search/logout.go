package search

import (
	"errors"
	"strings"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/selector"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/template"
)

// ErrSearchLoggedOut signals that a search response looked logged-out, so the
// caller should re-login and retry the search once. It carries no response bytes
// or credentials.
var ErrSearchLoggedOut = errors.New("search: response looks logged-out (login.test selector absent)")

// looksLoggedOut reproduces the selector half of Jackett's CheckIfLoginIsNeeded:
// when the definition declares a login.test selector and an HTML search response
// does NOT contain it, the session has expired. The redirect half lives in
// resolveRedirect (redirect.go): search requests are never auto-followed, so a
// logged-out 3xx is detected from the raw redirect status itself, before any
// body check.
//
// Detection is skipped (returns false) when the def has no login.test selector,
// or when the response's wire Content-Type is present and not text/html — matching
// Jackett's contentType?.Contains("text/html") ?? true (ordinal, case-sensitive; a
// MISSING header still runs the check). The API trackers that return JSON authenticate
// with a stateless apikey and declare no login.test, so they never relogin.
//
// On any uncertainty (unparseable body, selector render/eval error) it returns
// false: a relogin is only triggered on a clear logged-out signal, so a parsing
// hiccup can never start a relogin loop.
func looksLoggedOut(def *loader.Definition, body []byte, contentType string, query Query, deps Deps) bool {
	if def.Login == nil || def.Login.Test == nil || def.Login.Test.Selector == "" {
		return false
	}
	// Jackett: contentType?.Contains("text/html") ?? true — ordinal and
	// case-sensitive; a missing header RUNS the check; the declared response type
	// plays no role.
	if contentType != "" && !strings.Contains(contentType, "text/html") {
		return false
	}
	eng := selector.New()
	doc, err := eng.ParseHTML(body)
	if err != nil {
		return false
	}
	rendered, err := template.Eval(def.Login.Test.Selector, requestContext(query, deps))
	if err != nil {
		return false
	}
	_, found, err := eng.Field(doc.Root(), loader.SelectorBlock{Selector: rendered})
	if err != nil {
		return false
	}
	return !found
}

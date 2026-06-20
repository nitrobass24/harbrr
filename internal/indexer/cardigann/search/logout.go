package search

import (
	"bytes"
	"errors"
	"fmt"
	"strings"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/selector"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/template"
)

// loggedOutSignature is a TEMPORARY diagnostic: a redaction-safe fingerprint of a
// search response that tripped the logged-out check, so a live failure can be
// pinned to login page vs anti-bot challenge vs a results page false-positive
// WITHOUT logging response bytes. It emits only the <title>, structural marker
// booleans, and the length — none of which carry a passkey/cookie/credential.
func loggedOutSignature(body []byte) string {
	lc := bytes.ToLower(body)
	has := func(s string) bool { return bytes.Contains(lc, []byte(s)) }
	return fmt.Sprintf("len=%d title=%q cloudflare=%t justmoment=%t pwdform=%t uidpwd=%t logoutphp=%t torrents=%t",
		len(body),
		debugTitle(body),
		has("cloudflare") || has("__cf") || has("/cdn-cgi/"),
		has("just a moment") || has("checking your browser"),
		has(`type="password"`) || has("type='password'"),
		has(`name="uid"`) || has(`name="pwd"`),
		has("logout.php"),
		has("page=torrents") || has("download.php") || has("torrenttable"),
	)
}

// debugTitle extracts the (truncated) <title> text for loggedOutSignature.
func debugTitle(body []byte) string {
	lc := bytes.ToLower(body)
	i := bytes.Index(lc, []byte("<title"))
	if i < 0 {
		return ""
	}
	gt := bytes.IndexByte(lc[i:], '>')
	if gt < 0 {
		return ""
	}
	start := i + gt + 1
	end := bytes.Index(lc[start:], []byte("</title>"))
	if end < 0 {
		return ""
	}
	t := strings.TrimSpace(string(body[start : start+end]))
	if len(t) > 80 {
		t = t[:80]
	}
	return t
}

// ErrSearchLoggedOut signals that a search response looked logged-out, so the
// caller should re-login and retry the search once. It carries no response bytes
// or credentials.
var ErrSearchLoggedOut = errors.New("search: response looks logged-out (login.test selector absent)")

// looksLoggedOut reproduces the selector half of Jackett's CheckIfLoginIsNeeded:
// when the definition declares a login.test selector and an HTML search response
// does NOT contain it, the session has expired. Jackett also treats an HTTP
// redirect as logged-out; harbrr's production client follows redirects, so a
// logged-out 3xx lands on the login page whose body likewise lacks the selector
// and this same check catches it.
//
// Detection is skipped (returns false) when the def has no login.test selector,
// or for JSON/XML responses — matching Jackett gating the selector check on an
// HTML content type. The API trackers that return JSON authenticate with a
// stateless apikey and declare no login.test, so they never relogin.
//
// On any uncertainty (unparseable body, selector render/eval error) it returns
// false: a relogin is only triggered on a clear logged-out signal, so a parsing
// hiccup can never start a relogin loop.
func looksLoggedOut(def *loader.Definition, body []byte, respType string, query Query, deps Deps) bool {
	if def.Login == nil || def.Login.Test == nil || def.Login.Test.Selector == "" {
		return false
	}
	if respType == responseTypeJSON || respType == responseTypeXML {
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

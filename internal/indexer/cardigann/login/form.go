package login

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strings"

	"github.com/PuerkitoBio/goquery"

	apphttp "github.com/autobrr/harbrr/internal/http"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/template"
)

// loginForm reproduces Jackett's Login.Method == "form" flow:
//  1. GET the landing page (Login.Path).
//  2. Detect anti-bot interstitials (fail loud; the solver clears them).
//  3. Extract SelectorInputs (CSRF/hidden tokens) and GetSelectorInps from the
//     landing document.
//  4. Assemble the POST body: definition Inputs (template-rendered) overlaid with
//     the extracted selector values.
//  5. Resolve the submit target (Login.SubmitPath, else the form's action attr,
//     else the landing path) and POST.
//  6. Run the error selectors.
//
// The cookie jar persists Set-Cookie from the landing GET into the POST.
func (e *Executor) loginForm(ctx context.Context, def *loader.Definition) error {
	// Jackett seeds Login.Cookies (e.g. ["JAVA=OK"] to avoid a jscheck redirect)
	// into the cookie header BEFORE the landing GET, so they are sent on both the
	// landing request and the submit POST (CardigannIndexer DoLogin form path).
	if err := e.seedStaticCookies(def.Login.Cookies); err != nil {
		return err
	}
	landingURL, err := e.resolvePath(def.Login.Path)
	if err != nil {
		return err
	}
	// Fetch the landing page, routing an anti-bot interstitial through the
	// configured solver (NoopSolver by default => fail loud, unchanged behaviour).
	body, err := e.fetchLandingPastAntiBot(ctx, landingURL, def.Login.Headers)
	if err != nil {
		return err
	}

	doc, err := goquery.NewDocumentFromReader(strings.NewReader(string(body)))
	if err != nil {
		return fmt.Errorf("parsing login page from %s: %w", apphttp.SchemeHost(landingURL), err)
	}

	form := selectForm(doc, def.Login.Form)
	pairs, err := e.assembleFormPairs(def.Login, form, doc, body)
	if err != nil {
		return err
	}
	getPairs, err := e.extractSelectorInputs(body, def.Login.GetSelectorInps)
	if err != nil {
		return err
	}

	target, err := e.resolveFormTarget(def.Login, form, landingURL)
	if err != nil {
		return err
	}
	if len(getPairs) > 0 {
		target, err = appendQuery(target, getPairs)
		if err != nil {
			return err
		}
	}
	return e.postFormAbsolute(ctx, def.Login, target, pairs)
}

// assembleFormPairs builds the POST body in Jackett's exact precedence order:
//  1. Harvest every <input> already present in the form (name -> value),
//     skipping nameless/disabled inputs and unchecked checkboxes/radios. This is
//     what carries pre-populated hidden CSRF fields that are NOT declared as
//     selectorinputs.
//  2. Overlay the definition Inputs (template-rendered).
//  3. Overlay the SelectorInputs (CSRF/hidden extracted by selector).
//
// Later phases override earlier ones on key collision, matching Jackett writing
// into a single pairs dictionary.
func (e *Executor) assembleFormPairs(l *loader.Login, form *goquery.Selection, doc *goquery.Document, body []byte) (url.Values, error) {
	pairs := harvestFormInputs(form)

	rendered, err := e.renderFormInputs(l, doc)
	if err != nil {
		return nil, err
	}
	for k, vs := range rendered {
		pairs[k] = vs
	}

	extracted, err := e.extractSelectorInputs(body, l.SelectorInputs)
	if err != nil {
		return nil, err
	}
	for k, vs := range extracted {
		pairs[k] = vs
	}
	return pairs, nil
}

// renderFormInputs renders Login.Inputs into form pairs. When Login.Selectors is
// true, each input KEY is a CSS selector resolving to an input element, and the
// posted field name is that element's `name` attribute (Jackett's
// Login.Selectors branch). Otherwise the key IS the field name.
func (e *Executor) renderFormInputs(l *loader.Login, doc *goquery.Document) (url.Values, error) {
	if !selectorsEnabled(l) {
		return e.renderInputs(l.Inputs)
	}
	out := url.Values{}
	for _, key := range sortedKeys(l.Inputs) {
		rendered, err := template.Eval(l.Inputs[key].String(), e.templateContext())
		if err != nil {
			return nil, fmt.Errorf("rendering login input %q: %w", key, err)
		}
		name, ok := doc.Find(key).First().Attr("name")
		if !ok || name == "" {
			return nil, fmt.Errorf("%w: no input matched selector %q (selectors: true)", ErrLoginFailed, key)
		}
		out.Set(name, rendered)
	}
	return out, nil
}

// selectorsEnabled reports whether Login.Selectors is set true.
func selectorsEnabled(l *loader.Login) bool {
	return l.Selectors != nil && *l.Selectors
}

// selectForm returns the form selection (Login.Form, defaulting to "form").
func selectForm(doc *goquery.Document, formSel string) *goquery.Selection {
	if formSel == "" {
		formSel = "form"
	}
	return doc.Find(formSel).First()
}

// harvestFormInputs reproduces Jackett's pre-population: every named, enabled
// <input> in the form contributes its current value; unchecked checkboxes/radios
// are skipped (their value is only submitted when checked). Harvested values may
// include CSRF tokens, so they are never logged.
func harvestFormInputs(form *goquery.Selection) url.Values {
	pairs := url.Values{}
	form.Find("input").Each(func(_ int, s *goquery.Selection) {
		name, ok := s.Attr("name")
		if !ok || name == "" {
			return
		}
		if _, disabled := s.Attr("disabled"); disabled {
			return
		}
		if isUncheckedToggle(s) {
			return
		}
		pairs.Set(name, s.AttrOr("value", ""))
	})
	return pairs
}

// isUncheckedToggle reports whether s is a checkbox/radio that is not checked,
// which Jackett excludes from the submitted pairs.
func isUncheckedToggle(s *goquery.Selection) bool {
	t := strings.ToLower(s.AttrOr("type", ""))
	if t != "checkbox" && t != "radio" {
		return false
	}
	_, checked := s.Attr("checked")
	return !checked
}

// extractSelectorInputs runs each SelectorBlock against the landing document and
// collects the resolved values keyed by input name. A required selector that
// matches nothing is a hard error (Jackett passes required: !Optional). Values
// may be CSRF tokens — secret-equivalent — so errors reference the key/selector
// only, never the value.
func (e *Executor) extractSelectorInputs(body []byte, inputs map[string]loader.SelectorBlock) (url.Values, error) {
	if len(inputs) == 0 {
		return url.Values{}, nil
	}
	doc, err := e.Selector.ParseHTML(body)
	if err != nil {
		return nil, fmt.Errorf("parsing login page for selector inputs: %w", err)
	}
	root := doc.Root()
	out := url.Values{}
	for _, name := range sortedSelectorKeys(inputs) {
		blk := inputs[name]
		val, found, ferr := e.Selector.Field(root, blk)
		if ferr != nil {
			return nil, fmt.Errorf("extracting selector input %q: %w", name, ferr)
		}
		if !found {
			if isOptional(blk) {
				continue
			}
			return nil, fmt.Errorf("%w: required selector input %q (%q) matched nothing", ErrLoginFailed, name, blk.Selector)
		}
		out.Set(name, val)
	}
	return out, nil
}

// resolveFormTarget picks the POST target: Login.SubmitPath wins; otherwise the
// form's action attribute (resolved against the landing URL); otherwise the
// landing URL itself (Jackett posts back to the same page when action is empty).
func (e *Executor) resolveFormTarget(l *loader.Login, form *goquery.Selection, landingURL string) (string, error) {
	if l.SubmitPath != "" {
		// Jackett resolves the submit path against the landing URL
		// (resolvePath(submitUrlstr, new Uri(loginUrl))), not the bare site link.
		rendered, err := template.Eval(l.SubmitPath, e.templateContext())
		if err != nil {
			return "", fmt.Errorf("rendering submitpath %q: %w", apphttp.SchemeHost(l.SubmitPath), err)
		}
		return resolveAgainst(landingURL, rendered)
	}
	action, _ := form.Attr("action")
	if action == "" {
		return landingURL, nil
	}
	return resolveAgainst(landingURL, action)
}

// postFormAbsolute POSTs an already-resolved absolute target, then runs the
// error selectors. Distinct from postForm (methods.go), which resolves a
// definition path; the form flow has already resolved its target via the form
// action.
//
// Form body uses url.Values.Encode — see postForm (methods.go) for the deliberate
// login form-encoding divergence note.
func (e *Executor) postFormAbsolute(ctx context.Context, l *loader.Login, target string, pairs url.Values) error {
	headers := mergeFormHeaders(l.Headers)
	body, status, err := e.do(ctx, "POST", target, strings.NewReader(pairs.Encode()), headers)
	if err != nil {
		return err
	}
	return e.checkErrors(l, target, body, status)
}

// selectorMatches reports whether sel matches at least one element in body. Used
// by CheckTest to reproduce Jackett's "selection.Length == 0 => login needed".
func (e *Executor) selectorMatches(body []byte, sel string) (bool, error) {
	doc, err := e.Selector.ParseHTML(body)
	if err != nil {
		return false, fmt.Errorf("parsing test page: %w", err)
	}
	rendered, err := template.Eval(sel, e.templateContext())
	if err != nil {
		return false, fmt.Errorf("rendering test selector: %w", err)
	}
	_, found, err := e.Selector.Field(doc.Root(), loader.SelectorBlock{Selector: rendered})
	if err != nil {
		// Report the ORIGINAL (un-rendered) selector text, never the rendered
		// form, which could interpolate a config value into the message.
		return false, fmt.Errorf("evaluating test selector %q: %w", sel, err)
	}
	return found, nil
}

// seedCookies parses a raw Cookie-header string ("a=1; b=2") and seeds the jar
// for the BaseURL host, implementing the manual-cookie fallback. The cookie
// values are secrets and are never logged. An empty/whitespace cookie is a hard
// error so a misconfigured manual cookie fails loud rather than silently
// "logging in" with no session.
func (e *Executor) seedCookies(raw string) error {
	host, err := url.Parse(e.BaseURL)
	if err != nil {
		return fmt.Errorf("parsing base URL %q for cookie seeding: %w", apphttp.SchemeHost(e.BaseURL), err)
	}
	cookies := parseCookieHeader(raw)
	if len(cookies) == 0 {
		return fmt.Errorf("%w: cookie method got an empty cookie value", ErrLoginFailed)
	}
	e.Jar.SetCookies(host, cookies)
	return nil
}

// seedStaticCookies seeds the definition's Login.Cookies (a list of "name=value"
// pairs) into the jar for the BaseURL host before the login round-trip, matching
// Jackett's `CookieHeader.Value = string.Join("; ", Login.Cookies)`. These are
// fixed, non-secret bypass cookies (e.g. "JAVA=OK" to skip a jscheck redirect),
// but their values are still kept out of logs on the redacted error path. An
// empty/absent list is a no-op.
func (e *Executor) seedStaticCookies(cookies []string) error {
	if len(cookies) == 0 {
		return nil
	}
	host, err := url.Parse(e.BaseURL)
	if err != nil {
		return fmt.Errorf("parsing base URL %q for static cookie seeding: %w", apphttp.SchemeHost(e.BaseURL), err)
	}
	parsed := parseCookieHeader(strings.Join(cookies, "; "))
	if len(parsed) == 0 {
		return nil
	}
	e.Jar.SetCookies(host, parsed)
	return nil
}

// sortedSelectorKeys returns map keys in deterministic order so extracted form
// fields are stable.
func sortedSelectorKeys(m map[string]loader.SelectorBlock) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// isOptional reports whether a selector block is marked optional.
func isOptional(b loader.SelectorBlock) bool {
	return b.Optional != nil && *b.Optional
}

// resolveAgainst resolves a possibly-relative reference against an absolute base.
func resolveAgainst(base, ref string) (string, error) {
	b, err := url.Parse(base)
	if err != nil {
		return "", fmt.Errorf("parsing base %s: %w", apphttp.SchemeHost(base), err)
	}
	r, err := url.Parse(ref)
	if err != nil {
		return "", fmt.Errorf("parsing form action %s: %w", apphttp.SchemeHost(ref), err)
	}
	return b.ResolveReference(r).String(), nil
}
